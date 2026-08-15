package nvoken

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

type StreamEvent struct {
	ID    string          `json:"id,omitempty"`
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
	Retry time.Duration   `json:"retry,omitempty"`
}

// ErrStopStream ends a Session subscription from inside its consumer. The
// unfiltered stream never ends on its own, so leaving it is the caller's
// decision to make.
var ErrStopStream = errors.New("stop reading the stream")

type ReducedSnapshot struct {
	Messages          []SessionMessage             `json:"messages"`
	InvocationChanges []generated.InvocationChange `json:"invocation_changes"`
	Previews          []StreamPreview              `json:"previews"`
	Cursor            string                       `json:"cursor,omitempty"`
}

// StreamPreview is one message the model is writing, accumulated from the
// fragments of one content block. One field carries every kind of fragment,
// because one accumulator handles all of them.
type StreamPreview struct {
	InvocationID string `json:"invocation_id"`
	Attempt      int64  `json:"attempt"`
	// MessageID names the saved message this preview is building. It is the
	// key: the handoff to the saved message updates a row that already has its
	// permanent identity, rather than one row disappearing and another taking
	// its place.
	MessageID    string `json:"message_id"`
	ContentIndex int    `json:"content_index"`
	Kind         string `json:"kind"`
	Delta        string `json:"delta"`
	// ToolCallID and Name are present on tool_arguments previews and name the
	// call the fragments belong to.
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

type streamPreviewKey struct {
	messageID    string
	contentIndex int
}

type Reducer struct {
	messages            map[int64]SessionMessage
	changes             map[string]generated.InvocationChange
	previews            map[streamPreviewKey]StreamPreview
	latestAttempts      map[string]int64
	terminalInvocations map[string]struct{}
	cursor              string
}

func NewReducer() *Reducer {
	return &Reducer{
		messages:            make(map[int64]SessionMessage),
		changes:             make(map[string]generated.InvocationChange),
		previews:            make(map[streamPreviewKey]StreamPreview),
		latestAttempts:      make(map[string]int64),
		terminalInvocations: make(map[string]struct{}),
	}
}

func (r *Reducer) Apply(event StreamEvent) error {
	switch event.Type {
	case "message.delta":
		if err := requireFrameKeys("MessageDeltaEvent", event.Data); err != nil {
			return err
		}
		var delta generated.MessageDeltaEvent
		if err := json.Unmarshal(event.Data, &delta); err != nil {
			return fmt.Errorf("decode message delta: %w", err)
		}
		r.appendPreview(delta)
		return nil
	case "stream.resync":
		if err := requireFrameKeys("StreamResyncEvent", event.Data); err != nil {
			return err
		}
		var resync generated.StreamResyncEvent
		if err := json.Unmarshal(event.Data, &resync); err != nil {
			return fmt.Errorf("decode stream resync: %w", err)
		}
		// An absent Invocation is scope: discard previews for the whole Session.
		if resync.InvocationID == nil {
			clear(r.previews)
			clear(r.latestAttempts)
		} else {
			r.discardPreviews(string(*resync.InvocationID))
		}
		return nil
	}
	if event.Type != "transcript.update" {
		return nil
	}
	if err := requireTranscriptUpdateKeys(event.Data); err != nil {
		return err
	}
	var update generated.TranscriptUpdateEvent
	if err := json.Unmarshal(event.Data, &update); err != nil {
		return fmt.Errorf("decode transcript update: %w", err)
	}
	// Messages before changes, so a turn is never marked settled before its
	// final message exists.
	for _, message := range update.Messages {
		r.messages[message.Sequence] = message
		if message.Role == generated.SessionMessageRoleAssistant && message.InvocationID != nil {
			r.discardPreviews(*message.InvocationID)
		}
	}
	for _, change := range update.InvocationChanges {
		key := fmt.Sprintf("%s:%d", change.InvocationID, change.Revision)
		r.changes[key] = change
		if IsTurnOver(change) {
			r.terminalInvocations[change.InvocationID] = struct{}{}
			r.discardPreviews(change.InvocationID)
		}
	}
	if event.ID != "" {
		r.cursor = event.ID
	} else if update.Cursor != "" {
		r.cursor = update.Cursor
	}
	return nil
}

// Settled reports whether a change carrying a terminal status has arrived for
// this turn. That is the terminal signal, and there is no other.
func (r *Reducer) Settled(invocationID string) bool {
	_, settled := r.terminalInvocations[invocationID]
	return settled
}

func (r *Reducer) Snapshot() ReducedSnapshot {
	messages := make([]SessionMessage, 0, len(r.messages))
	for _, message := range r.messages {
		messages = append(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Sequence < messages[j].Sequence })
	changes := make([]generated.InvocationChange, 0, len(r.changes))
	for _, change := range r.changes {
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].InvocationID == changes[j].InvocationID {
			return changes[i].Revision < changes[j].Revision
		}
		return changes[i].InvocationID < changes[j].InvocationID
	})
	previews := make([]StreamPreview, 0, len(r.previews))
	for _, preview := range r.previews {
		previews = append(previews, preview)
	}
	sort.Slice(previews, func(i, j int) bool {
		if previews[i].MessageID != previews[j].MessageID {
			return previews[i].MessageID < previews[j].MessageID
		}
		return previews[i].ContentIndex < previews[j].ContentIndex
	})
	return ReducedSnapshot{
		Messages:          messages,
		InvocationChanges: changes,
		Previews:          previews,
		Cursor:            r.cursor,
	}
}

func (r *Reducer) appendPreview(delta generated.MessageDeltaEvent) {
	invocationID := string(delta.InvocationID)
	if _, terminal := r.terminalInvocations[invocationID]; terminal {
		return
	}
	if latest, ok := r.latestAttempts[invocationID]; ok {
		if delta.Attempt < latest {
			return
		}
		if delta.Attempt > latest {
			r.discardPreviews(invocationID)
		}
	}
	r.latestAttempts[invocationID] = delta.Attempt
	key := streamPreviewKey{
		messageID:    string(delta.MessageID),
		contentIndex: delta.ContentIndex,
	}
	preview := r.previews[key]
	preview.InvocationID = invocationID
	preview.Attempt = delta.Attempt
	preview.MessageID = string(delta.MessageID)
	preview.ContentIndex = delta.ContentIndex
	preview.Kind = string(delta.Kind)
	preview.Delta += delta.Delta
	if delta.ToolCallID != nil {
		preview.ToolCallID = *delta.ToolCallID
	}
	if delta.Name != nil {
		preview.Name = *delta.Name
	}
	r.previews[key] = preview
}

func (r *Reducer) discardPreviews(invocationID string) {
	for key, preview := range r.previews {
		if preview.InvocationID == invocationID {
			delete(r.previews, key)
		}
	}
	delete(r.latestAttempts, invocationID)
}

// Stream follows this turn and returns once a change for it carries a terminal
// status. It is the one stream, filtered to one Invocation.
func (h *InvocationHandle) Stream(ctx context.Context, consume func(StreamEvent) error) error {
	return h.StreamWithOptions(ctx, StreamOptions{}, consume)
}

func (h *InvocationHandle) StreamWithOptions(
	ctx context.Context,
	options StreamOptions,
	consume func(StreamEvent) error,
) error {
	if h.SessionID == "" {
		if _, err := h.Refresh(ctx); err != nil {
			return err
		}
	}
	invocationID := generated.InvocationID(h.InvocationID)
	return h.client.readStream(ctx, h.SessionID, &invocationID, options, func(event StreamEvent, reducer *Reducer) error {
		if consume != nil {
			if err := consume(event); err != nil {
				return err
			}
		}
		if reducer.Settled(h.InvocationID) {
			return ErrStopStream
		}
		return nil
	})
}

// StreamSession subscribes to everything in a Session. It stays open while the
// Session is idle and a turn started later appears on it, so it returns only
// when the consumer says to stop with ErrStopStream, when the consumer fails,
// or when the context ends.
func (c *Client) StreamSession(ctx context.Context, sessionID string, consume func(StreamEvent, ReducedSnapshot) error) error {
	return c.StreamSessionWithOptions(ctx, sessionID, StreamOptions{}, consume)
}

func (c *Client) StreamSessionWithOptions(
	ctx context.Context,
	sessionID string,
	options StreamOptions,
	consume func(StreamEvent, ReducedSnapshot) error,
) error {
	return c.readStream(ctx, sessionID, nil, options, func(event StreamEvent, reducer *Reducer) error {
		return consume(event, reducer.Snapshot())
	})
}

// readStream is the one read loop. It reconnects from its last durable cursor
// on any connection end, because stream.end never says a turn is over and a
// silent drop says nothing at all.
func (c *Client) readStream(
	ctx context.Context,
	sessionID string,
	invocationID *generated.InvocationID,
	options StreamOptions,
	consume func(StreamEvent, *Reducer) error,
) error {
	reducer := NewReducer()
	retryDelay := time.Second
	for {
		params := &generated.StreamSessionParams{
			Deltas:       options.Deltas,
			InvocationID: invocationID,
		}
		if cursor := reducer.Snapshot().Cursor; cursor != "" {
			params.LastEventID = &cursor
		}
		response, err := c.raw.ClientInterface.StreamSession(ctx, generated.SessionID(sessionID), params)
		if err != nil {
			if err := waitForReconnect(ctx, retryDelay); err != nil {
				return err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			return errorFromResponse(response.StatusCode, response.Header, body)
		}
		err = readSSE(response.Body, func(event StreamEvent) error {
			if event.Retry > 0 {
				retryDelay = event.Retry
			}
			if len(event.Data) == 0 {
				// A frame carrying only `retry:` is a control frame, not an
				// event. The runtime opens every stream with one. Its
				// bookkeeping is applied above; there is nothing to consume.
				return nil
			}
			if err := reducer.Apply(event); err != nil {
				return err
			}
			return consume(event, reducer)
		})
		_ = response.Body.Close()
		if errors.Is(err, ErrStopStream) {
			return nil
		}
		if err != nil && err != io.EOF {
			return err
		}
		if err := waitForReconnect(ctx, retryDelay); err != nil {
			return err
		}
	}
}

func readSSE(reader io.Reader, consume func(StreamEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	event := StreamEvent{}
	var data []string
	dispatch := func() error {
		if event.Type == "" && len(data) == 0 && event.ID == "" && event.Retry == 0 {
			return nil
		}
		event.Data = json.RawMessage(strings.Join(data, "\n"))
		if err := consume(event); err != nil {
			return err
		}
		event = StreamEvent{}
		data = nil
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			event.Type = value
		case "id":
			event.ID = value
		case "data":
			data = append(data, value)
		case "retry":
			milliseconds, err := strconv.Atoi(value)
			if err == nil && milliseconds >= 0 {
				event.Retry = time.Duration(milliseconds) * time.Millisecond
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(data) > 0 || event.Type != "" || event.ID != "" || event.Retry > 0 {
		if err := dispatch(); err != nil {
			return err
		}
	}
	return io.EOF
}

func waitForReconnect(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		delay = time.Second
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return transportError(ctx.Err())
	case <-timer.C:
		return nil
	}
}

var _ = http.MethodGet
