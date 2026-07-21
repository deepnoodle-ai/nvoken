package domain

import "time"

const (
	LiveEventGenerationDelta = "generation.delta"
	LiveEventStreamResync    = "stream.resync"
	LiveEventStreamEnd       = "stream.end"
)

const (
	StreamEndTerminal = "terminal"
	StreamEndRotate   = "rotate"
)

// GenerationDelta is the provider-neutral live subset of one model event.
// It is deliberately not a transcript record and carries no durable cursor.
type GenerationDelta struct {
	ContentIndex int    `json:"content_index"`
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	Thinking     string `json:"thinking,omitempty"`
}

// GenerationDeltaEvent is the public live-only SSE payload. DeltaSequence is
// fenced-attempt-local ordering evidence, not a replay cursor.
type GenerationDeltaEvent struct {
	EventType     string          `json:"event_type"`
	SessionID     string          `json:"session_id"`
	InvocationID  string          `json:"invocation_id"`
	LeaseAttempt  int64           `json:"lease_attempt"`
	DeltaSequence int64           `json:"delta_sequence"`
	Delta         GenerationDelta `json:"delta"`
	EmittedAt     time.Time       `json:"emitted_at"`
}

type StreamResyncEvent struct {
	EventType string `json:"event_type"`
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

type StreamEndEvent struct {
	EventType    string `json:"event_type"`
	SessionID    string `json:"session_id"`
	Reason       string `json:"reason"`
	ResumeCursor string `json:"resume_cursor"`
}
