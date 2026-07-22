package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/observability"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

const transcriptSnapshotEvent = "transcript.snapshot"

func (h *handler) streamSessionTranscript(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	query, err := strictQuery(r, "cursor")
	if err != nil {
		h.writeError(w, requestID, invalidQuery(err))
		return
	}
	cursor := ""
	if values, present := query["cursor"]; present {
		cursor = values[0]
	} else {
		lastEventIDs := r.Header.Values("Last-Event-ID")
		if len(lastEventIDs) > 1 {
			h.writeError(w, requestID, invalidQuery(errors.New("Last-Event-ID must appear at most once")))
			return
		}
		if len(lastEventIDs) == 1 {
			cursor = strings.TrimSpace(lastEventIDs[0])
			if cursor == "" {
				h.writeError(w, requestID, invalidQuery(errors.New("Last-Event-ID must not be blank")))
				return
			}
		}
	}
	if h.runtime == nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeUnavailable, Message: "The service is temporarily unavailable."})
		return
	}
	sessionID := r.PathValue("session_id")
	if _, err := h.runtime.GetSessionTranscriptStreamState(r.Context(), auth, sessionID); err != nil {
		h.writeError(w, requestID, err)
		return
	}

	var subscription ports.LiveSubscription
	if h.liveEvents != nil {
		// Subscribe before the first durable read so publication concurrent with
		// bootstrap is either buffered here or visible in the fixed-cut drain.
		subscription = h.liveEvents.Subscribe(r.Context(), auth.AccountID, sessionID)
		defer subscription.Close()
	}
	first, err := h.runtime.GetSessionTranscript(r.Context(), auth, sessionID, services.TranscriptInput{
		Cursor: cursor, Limit: services.MaxRecoveryPageSize,
	})
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := writeSSEControl(w, h.stream.WriteTimeout, "retry: 1000\n\n"); err != nil {
		h.logStreamClose(requestID, sessionID, "write_error", err)
		return
	}

	delivered := cursor
	drain := func(initial *services.TranscriptSnapshot) error {
		page := initial
		pageToken := ""
		for {
			var snapshot services.TranscriptSnapshot
			if page != nil {
				snapshot = *page
				page = nil
			} else {
				input := services.TranscriptInput{Cursor: delivered, PageToken: pageToken, Limit: services.MaxRecoveryPageSize}
				if pageToken != "" {
					input.Cursor = ""
				}
				var err error
				snapshot, err = h.runtime.GetSessionTranscript(r.Context(), auth, sessionID, input)
				if err != nil {
					return err
				}
			}
			// Empty pages echo the delivered watermark under the PRD 007
			// contract. Updating the in-memory position is safe, but only a
			// nonempty frame exposes that position as an SSE ID.
			delivered = snapshot.ResumeCursor
			if len(snapshot.Messages) != 0 || len(snapshot.InvocationChanges) != 0 {
				response := transcriptSnapshotResponseFromService(snapshot)
				if err := writeSSEEvent(w, h.stream.WriteTimeout, transcriptSnapshotEvent, snapshot.ResumeCursor, response); err != nil {
					return err
				}
			}
			if !snapshot.HasMore {
				return nil
			}
			if snapshot.NextPageToken == nil || *snapshot.NextPageToken == "" {
				return errors.New("transcript snapshot has_more without next_page_token")
			}
			pageToken = *snapshot.NextPageToken
		}
	}

	terminalAfterReconcile := func() (bool, error) {
		state, err := h.runtime.GetSessionTranscriptStreamState(r.Context(), auth, sessionID)
		if err != nil || state.Active {
			return false, err
		}
		// Close the drain/read race: a settlement may commit after the previous
		// fixed cut but before the idle observation.
		if err := drain(nil); err != nil {
			return false, err
		}
		state, err = h.runtime.GetSessionTranscriptStreamState(r.Context(), auth, sessionID)
		return err == nil && !state.Active, err
	}

	if err := drain(&first); err != nil {
		h.logStreamClose(requestID, sessionID, "durable_drain_error", err)
		return
	}
	terminal, err := terminalAfterReconcile()
	if err != nil {
		h.logStreamClose(requestID, sessionID, "terminal_reconcile_error", err)
		return
	}
	if terminal {
		if err := h.endStream(w, sessionID, delivered, domain.StreamEndTerminal); err != nil {
			h.logStreamClose(requestID, sessionID, "write_timeout", err)
		} else {
			h.logStreamClose(requestID, sessionID, domain.StreamEndTerminal, nil)
		}
		return
	}

	poll := time.NewTicker(h.stream.PollInterval)
	keepalive := time.NewTicker(h.stream.KeepaliveInterval)
	lifetime := time.NewTimer(h.stream.MaxLifetime)
	defer poll.Stop()
	defer keepalive.Stop()
	defer lifetime.Stop()

	for {
		select {
		case <-r.Context().Done():
			h.logStreamClose(requestID, sessionID, "client_disconnect", r.Context().Err())
			return
		case <-h.streamShutdown.Done():
			if err := h.endStream(w, sessionID, delivered, domain.StreamEndRotate); err != nil {
				h.logStreamClose(requestID, sessionID, "shutdown_write_timeout", err)
			} else {
				h.logStreamClose(requestID, sessionID, "rotate_shutdown", nil)
			}
			return
		case <-lifetime.C:
			if err := h.endStream(w, sessionID, delivered, domain.StreamEndRotate); err != nil {
				h.logStreamClose(requestID, sessionID, "write_timeout", err)
			} else {
				h.logStreamClose(requestID, sessionID, "rotate_lifetime", nil)
			}
			return
		case <-keepalive.C:
			gap, err := h.writePendingStreamGap(w, subscription, requestID, sessionID)
			if err != nil {
				h.logStreamClose(requestID, sessionID, "write_timeout", err)
				return
			}
			if gap {
				if err := drain(nil); err != nil {
					h.logStreamClose(requestID, sessionID, "durable_drain_error", err)
					return
				}
			}
			if err := writeSSEControl(w, h.stream.WriteTimeout, ": keepalive\n\n"); err != nil {
				h.logStreamClose(requestID, sessionID, "write_timeout", err)
				return
			}
		case <-poll.C:
			if _, err := h.writePendingStreamGap(w, subscription, requestID, sessionID); err != nil {
				h.logStreamClose(requestID, sessionID, "write_timeout", err)
				return
			}
			if err := drain(nil); err != nil {
				h.logStreamClose(requestID, sessionID, "durable_drain_error", err)
				return
			}
			terminal, err := terminalAfterReconcile()
			if err != nil {
				h.logStreamClose(requestID, sessionID, "terminal_reconcile_error", err)
				return
			}
			if terminal {
				if err := h.endStream(w, sessionID, delivered, domain.StreamEndTerminal); err != nil {
					h.logStreamClose(requestID, sessionID, "write_timeout", err)
				} else {
					h.logStreamClose(requestID, sessionID, domain.StreamEndTerminal, nil)
				}
				return
			}
		case event, ok := <-liveEvents(subscription):
			if !ok {
				subscription = nil
				continue
			}
			if event.AccountID != auth.AccountID || event.SessionID != sessionID {
				continue
			}
			if subscription.TakeGap() || event.Type == domain.LiveEventStreamResync {
				if err := h.writeStreamResync(w, requestID, sessionID); err != nil {
					h.logStreamClose(requestID, sessionID, "write_timeout", err)
					return
				}
				if err := drain(nil); err != nil {
					h.logStreamClose(requestID, sessionID, "durable_drain_error", err)
					return
				}
				continue
			}
			if event.Type != domain.LiveEventGenerationDelta {
				continue
			}
			var delta domain.GenerationDeltaEvent
			if err := json.Unmarshal(event.Payload, &delta); err != nil || !validGenerationDeltaEvent(delta, sessionID) {
				continue
			}
			if err := writeSSEEvent(w, h.stream.WriteTimeout, event.Type, "", delta); err != nil {
				h.logStreamClose(requestID, sessionID, "write_timeout", err)
				return
			}
		}
	}
}

func validGenerationDeltaEvent(event domain.GenerationDeltaEvent, sessionID string) bool {
	if event.EventType != domain.LiveEventGenerationDelta || event.SessionID != sessionID ||
		event.InvocationID == "" || event.LeaseAttempt < 1 || event.DeltaSequence < 1 ||
		event.Delta.ContentIndex < 0 || event.EmittedAt.IsZero() {
		return false
	}
	switch event.Delta.Type {
	case "text":
		return event.Delta.Text != "" && event.Delta.Thinking == ""
	case "thinking":
		return event.Delta.Thinking != "" && event.Delta.Text == ""
	default:
		return false
	}
}

func (h *handler) writePendingStreamGap(
	w http.ResponseWriter,
	subscription ports.LiveSubscription,
	requestID string,
	sessionID string,
) (bool, error) {
	if subscription == nil || !subscription.TakeGap() {
		return false, nil
	}
	return true, h.writeStreamResync(w, requestID, sessionID)
}

func (h *handler) writeStreamResync(w http.ResponseWriter, requestID, sessionID string) error {
	err := writeSSEEvent(w, h.stream.WriteTimeout, domain.LiveEventStreamResync, "", domain.StreamResyncEvent{
		EventType: domain.LiveEventStreamResync, SessionID: sessionID, Reason: "live_delivery_gap",
	})
	if err == nil {
		h.logger.Warn("Session transcript stream resync",
			"event", observability.EventStreamResync,
			"request_id", requestID, "session_id", sessionID, "reason", "live_delivery_gap")
	}
	return err
}

func (h *handler) endStream(w http.ResponseWriter, sessionID, cursor, reason string) error {
	return writeSSEEvent(w, h.stream.WriteTimeout, domain.LiveEventStreamEnd, "", domain.StreamEndEvent{
		EventType: domain.LiveEventStreamEnd, SessionID: sessionID, Reason: reason, ResumeCursor: cursor,
	})
}

func (h *handler) logStreamClose(requestID, sessionID, reason string, err error) {
	arguments := []any{
		"event", observability.EventStreamClosed,
		"request_id", requestID,
		"session_id", sessionID,
		"reason", reason,
	}
	if err != nil {
		arguments = append(arguments, "error_class", observability.ErrorClass(err))
		h.logger.Warn("Session transcript stream closed", arguments...)
		return
	}
	h.logger.Info("Session transcript stream closed", arguments...)
}

func liveEvents(subscription ports.LiveSubscription) <-chan ports.LiveEvent {
	if subscription == nil {
		return nil
	}
	return subscription.Events()
}

func transcriptSnapshotResponseFromService(snapshot services.TranscriptSnapshot) transcriptSnapshotResponse {
	messages := make([]sessionMessageResponse, len(snapshot.Messages))
	for index, item := range snapshot.Messages {
		messages[index] = sessionMessageResponseFromDomain(item)
	}
	changes := make([]invocationChangeResponse, len(snapshot.InvocationChanges))
	for index, item := range snapshot.InvocationChanges {
		changes[index] = invocationChangeResponseFromDomain(item)
	}
	return transcriptSnapshotResponse{
		Messages: messages, InvocationChanges: changes, HasMore: snapshot.HasMore,
		ResumeCursor: snapshot.ResumeCursor, NextPageToken: snapshot.NextPageToken,
	}
}

func writeSSEEvent(w http.ResponseWriter, timeout time.Duration, event, id string, data any) error {
	if strings.ContainsAny(event, "\r\n") || strings.ContainsAny(id, "\r\n") {
		return errors.New("invalid SSE metadata")
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode SSE event: %w", err)
	}
	var frame strings.Builder
	if id != "" {
		fmt.Fprintf(&frame, "id: %s\n", id)
	}
	fmt.Fprintf(&frame, "event: %s\ndata: %s\n\n", event, payload)
	return writeSSEControl(w, timeout, frame.String())
}

func writeSSEControl(w http.ResponseWriter, timeout time.Duration, value string) error {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprint(w, value); err != nil {
		return err
	}
	if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}
