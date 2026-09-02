package nvoken

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

// StreamEvent is one decoded SSE frame. Data contains the exact target event
// JSON, while ID is the durable resume cursor when the frame has one.
type StreamEvent struct {
	ID    string          `json:"id,omitempty"`
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
	Retry time.Duration   `json:"retry,omitempty"`
}

var ErrStopStream = errors.New("stop reading the stream")

type UpdatesOptions struct {
	Cursor *string
	Deltas *bool
}

type StreamPreview struct {
	TurnID       string `json:"turn_id"`
	Attempt      int64  `json:"attempt"`
	MessageID    string `json:"message_id"`
	ContentIndex int    `json:"content_index"`
	Kind         string `json:"kind"`
	Delta        string `json:"delta"`
	ToolCallID   string `json:"tool_call_id,omitempty"`
	Name         string `json:"name,omitempty"`
}

type ReducedSnapshot struct {
	Messages    []ConversationMessage `json:"messages"`
	TurnChanges []TurnChange          `json:"turn_changes"`
	Previews    []StreamPreview       `json:"previews"`
	Cursor      string                `json:"cursor,omitempty"`
}

// TurnUpdate is the high-level reduced view yielded while following one Turn.
// Previews are provisional deltas; Snapshot.Messages contains durable saved
// messages and Snapshot becomes authoritative after the final point read.
type TurnUpdate struct {
	Snapshot TurnSnapshot
	Previews []StreamPreview
	Cursor   string
}

type streamPreviewKey struct {
	messageID    string
	contentIndex int
}

// Reducer folds replayable durable frames and provisional previews into one
// current view. Durable frames are idempotent by identity and revision.
type Reducer struct {
	messages       map[int64]ConversationMessage
	changes        map[string]TurnChange
	previews       map[streamPreviewKey]StreamPreview
	latestAttempts map[string]int64
	terminalTurns  map[string]struct{}
	cursor         string
}

func NewReducer() *Reducer {
	return &Reducer{
		messages:       make(map[int64]ConversationMessage),
		changes:        make(map[string]TurnChange),
		previews:       make(map[streamPreviewKey]StreamPreview),
		latestAttempts: make(map[string]int64),
		terminalTurns:  make(map[string]struct{}),
	}
}

func (r *Reducer) seed(messages []ConversationMessage) {
	for _, message := range messages {
		r.messages[message.Sequence] = message
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
		if resync.TurnID == nil {
			clear(r.previews)
			clear(r.latestAttempts)
		} else {
			r.discardPreviews(*resync.TurnID)
		}
		return nil
	case "transcript.update":
		if err := requireTranscriptUpdateKeys(event.Data); err != nil {
			return err
		}
		var update generated.TranscriptUpdateEvent
		if err := json.Unmarshal(event.Data, &update); err != nil {
			return fmt.Errorf("decode transcript update: %w", err)
		}
		// Messages land before terminal changes so a snapshot never settles a
		// Turn before its final saved message is visible. A saved message
		// replaces only the preview that was building it: the Turn's next
		// message may already be accumulating, and dropping that prefix loses
		// text no later delta restores.
		for _, message := range update.Messages {
			r.messages[message.Sequence] = message
			r.discardMessagePreviews(string(message.ID))
		}
		for _, change := range update.TurnChanges {
			key := fmt.Sprintf("%s:%d", change.TurnID, change.Revision)
			r.changes[key] = change
			if change.Terminal {
				r.terminalTurns[change.TurnID] = struct{}{}
				r.discardPreviews(change.TurnID)
			}
		}
		if event.ID != "" {
			r.cursor = event.ID
		} else if update.Cursor != "" {
			r.cursor = update.Cursor
		}
	}
	return nil
}

func (r *Reducer) Settled(turnID string) bool {
	_, ok := r.terminalTurns[turnID]
	return ok
}

func (r *Reducer) Snapshot() ReducedSnapshot {
	messages := make([]ConversationMessage, 0, len(r.messages))
	for _, message := range r.messages {
		messages = append(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Sequence < messages[j].Sequence })
	changes := make([]TurnChange, 0, len(r.changes))
	for _, change := range r.changes {
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].TurnID == changes[j].TurnID {
			return changes[i].Revision < changes[j].Revision
		}
		return changes[i].TurnID < changes[j].TurnID
	})
	previews := make([]StreamPreview, 0, len(r.previews))
	for _, preview := range r.previews {
		previews = append(previews, preview)
	}
	sort.Slice(previews, func(i, j int) bool {
		if previews[i].MessageID == previews[j].MessageID {
			return previews[i].ContentIndex < previews[j].ContentIndex
		}
		return previews[i].MessageID < previews[j].MessageID
	})
	return ReducedSnapshot{Messages: messages, TurnChanges: changes, Previews: previews, Cursor: r.cursor}
}

func (r *Reducer) appendPreview(delta generated.MessageDeltaEvent) {
	turnID := string(delta.TurnID)
	if _, terminal := r.terminalTurns[turnID]; terminal {
		return
	}
	if latest, ok := r.latestAttempts[turnID]; ok {
		if delta.Attempt < latest {
			return
		}
		if delta.Attempt > latest {
			r.discardPreviews(turnID)
		}
	}
	r.latestAttempts[turnID] = delta.Attempt
	key := streamPreviewKey{messageID: delta.MessageID, contentIndex: delta.ContentIndex}
	preview := r.previews[key]
	preview.TurnID = turnID
	preview.Attempt = delta.Attempt
	preview.MessageID = delta.MessageID
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

func (r *Reducer) discardPreviews(turnID string) {
	for key, preview := range r.previews {
		if preview.TurnID == turnID {
			delete(r.previews, key)
		}
	}
	delete(r.latestAttempts, turnID)
}

// discardMessagePreviews drops every content index of one previewed message
// once the saved message carrying that ID has landed.
func (r *Reducer) discardMessagePreviews(messageID string) {
	for key := range r.previews {
		if key.messageID == messageID {
			delete(r.previews, key)
		}
	}
}

// Updates follows this Turn as reduced snapshots, reconnecting from the last
// durable cursor until its terminal change arrives or the consumer stops it
// with ErrStopStream. Raw SSE frames remain available through Raw().
func (t *Turn) Updates(ctx context.Context, options UpdatesOptions, consume func(TurnUpdate) error) error {
	if err := t.validateAccess(); err != nil {
		return err
	}
	current, err := t.Status(ctx)
	if err != nil {
		return turnWaitError(err, t)
	}
	if current.Resource.Status == generated.TurnStatusWaiting {
		if _, err := t.settleHostTools(ctx, current.Resource.ToolCalls); err != nil {
			return err
		}
	}
	if consume != nil {
		if err := consume(TurnUpdate{Snapshot: *current}); err != nil {
			if errors.Is(err, ErrStopStream) {
				return nil
			}
			return err
		}
	}
	if current.Resource.EndedAt != nil {
		return nil
	}
	reducer := NewReducer()
	reducer.seed(current.Messages)
	retryDelay := time.Second
	for {
		cursor := options.Cursor
		lastEventID := reducer.Snapshot().Cursor
		params := &generated.StreamTurnParams{Deltas: options.Deltas}
		if lastEventID != "" {
			params.LastEventID = &lastEventID
		} else if cursor != nil {
			value := generated.Cursor(*cursor)
			params.Cursor = &value
		}
		response, err := t.client.raw.ClientInterface.StreamTurn(ctx, t.id, params, t.requestEditor())
		if err != nil {
			if err := waitForReconnect(ctx, retryDelay); err != nil {
				return turnWaitError(transportError(err), t)
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
				return nil
			}
			if err := reducer.Apply(event); err != nil {
				return err
			}
			if event.Type == "transcript.update" {
				var update generated.TranscriptUpdateEvent
				if err := json.Unmarshal(event.Data, &update); err != nil {
					return fmt.Errorf("decode transcript update for tools: %w", err)
				}
				for _, change := range update.TurnChanges {
					if change.TurnID != t.id {
						continue
					}
					applyTurnChange(&current.Resource, change)
					if change.Status == generated.TurnStatusWaiting && change.ToolCalls != nil {
						if _, err := t.settleHostTools(ctx, *change.ToolCalls); err != nil {
							return err
						}
					}
				}
			}
			reduced := reducer.Snapshot()
			current.Messages = reduced.Messages
			if consume != nil {
				if err := consume(TurnUpdate{
					Snapshot: *current,
					Previews: reduced.Previews,
					Cursor:   reduced.Cursor,
				}); err != nil {
					return err
				}
			}
			if reducer.Settled(t.id) {
				final, err := t.Status(ctx)
				if err != nil {
					return turnWaitError(err, t)
				}
				if consume != nil {
					if err := consume(TurnUpdate{Snapshot: *final, Cursor: reduced.Cursor}); err != nil {
						return err
					}
				}
				return ErrStopStream
			}
			return nil
		})
		_ = response.Body.Close()
		if errors.Is(err, ErrStopStream) {
			return nil
		}
		if err != nil && err != io.EOF {
			return err
		}
		if err := waitForReconnect(ctx, retryDelay); err != nil {
			return turnWaitError(transportError(err), t)
		}
	}
}

func applyTurnChange(turn *TurnResource, change TurnChange) {
	turn.Status = change.Status
	turn.ConversationID = change.ConversationID
	turn.ContentExpiresAt = change.ContentExpiresAt
	turn.CreditBlock = change.CreditBlock
	turn.Error = change.Error
	turn.Provenance = change.Provenance
	turn.StopReason = change.StopReason
	turn.StructuredOutput = change.StructuredOutput
	turn.StructuredOutputProvenance = change.StructuredOutputProvenance
	turn.Usage = change.Usage
	turn.UpdatedAt = change.OccurredAt
	if change.ToolCalls != nil {
		turn.ToolCalls = append([]ToolCallSummary(nil), (*change.ToolCalls)...)
	}
	if change.Terminal && turn.EndedAt == nil {
		endedAt := change.OccurredAt
		turn.EndedAt = &endedAt
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
			if milliseconds, err := strconv.Atoi(value); err == nil && milliseconds >= 0 {
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
