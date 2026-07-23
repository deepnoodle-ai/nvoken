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

var errInvocationStreamSettled = errors.New("Invocation stream settled")

type ReducedSnapshot struct {
	Messages          []SessionMessage             `json:"messages"`
	InvocationChanges []generated.InvocationChange `json:"invocation_changes"`
	ResumeCursor      string                       `json:"resume_cursor,omitempty"`
}

type Reducer struct {
	messages map[int64]SessionMessage
	changes  map[string]generated.InvocationChange
	cursor   string
}

func NewReducer() *Reducer {
	return &Reducer{messages: make(map[int64]SessionMessage), changes: make(map[string]generated.InvocationChange)}
}

func (r *Reducer) Apply(event StreamEvent) error {
	if event.Type != "transcript.update" {
		return nil
	}
	var update generated.TranscriptUpdate
	if err := json.Unmarshal(event.Data, &update); err != nil {
		return fmt.Errorf("decode transcript update: %w", err)
	}
	for _, message := range update.Messages {
		r.messages[message.Sequence] = message
	}
	for _, change := range update.InvocationChanges {
		key := fmt.Sprintf("%s:%d", change.InvocationID, change.Revision)
		r.changes[key] = change
	}
	if event.ID != "" {
		r.cursor = event.ID
	} else if update.ResumeCursor != "" {
		r.cursor = update.ResumeCursor
	}
	return nil
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
	return ReducedSnapshot{Messages: messages, InvocationChanges: changes, ResumeCursor: r.cursor}
}

func (h *InvocationHandle) Stream(ctx context.Context, consume func(StreamEvent) error) error {
	retryDelay := time.Second
	cursor := ""
	for {
		params := &generated.StreamInvocationParams{}
		if cursor != "" {
			params.LastEventID = &cursor
		}
		response, err := h.client.raw.ClientInterface.StreamInvocation(ctx, h.InvocationID, params)
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
		settled := false
		err = readSSE(response.Body, func(event StreamEvent) error {
			if event.Retry > 0 {
				retryDelay = event.Retry
			}
			if event.ID != "" {
				cursor = event.ID
			}
			if err := consume(event); err != nil {
				return err
			}
			if event.Type == "invocation.result" {
				settled = true
				return errInvocationStreamSettled
			}
			return nil
		})
		_ = response.Body.Close()
		if errors.Is(err, errInvocationStreamSettled) {
			return nil
		}
		if err != nil && err != io.EOF {
			return err
		}
		if settled {
			return nil
		}
		if err := waitForReconnect(ctx, retryDelay); err != nil {
			return err
		}
	}
}

func (c *Client) StreamSession(ctx context.Context, sessionID string, consume func(StreamEvent, ReducedSnapshot) error) error {
	reducer := NewReducer()
	retryDelay := time.Second
	for {
		cursor := reducer.Snapshot().ResumeCursor
		params := &generated.StreamSessionTranscriptParams{}
		if cursor != "" {
			params.LastEventID = &cursor
		}
		response, err := c.raw.ClientInterface.StreamSessionTranscript(ctx, sessionID, params)
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
		terminalEnd := false
		err = readSSE(response.Body, func(event StreamEvent) error {
			if event.Retry > 0 {
				retryDelay = event.Retry
			}
			if err := reducer.Apply(event); err != nil {
				return err
			}
			if err := consume(event, reducer.Snapshot()); err != nil {
				return err
			}
			if event.Type == "stream.end" {
				var end generated.StreamEndEvent
				if json.Unmarshal(event.Data, &end) == nil && end.Reason == generated.Terminal {
					terminalEnd = true
				}
			}
			return nil
		})
		_ = response.Body.Close()
		if err != nil && err != io.EOF {
			return err
		}
		if terminalEnd {
			return nil
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
