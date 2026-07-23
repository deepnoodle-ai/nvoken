package domain

import "time"

const (
	LiveEventOutputTextDelta = "output_text.delta"
	LiveEventThinkingDelta   = "thinking.delta"
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
	Iteration    int    `json:"iteration,omitempty"`
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	Thinking     string `json:"thinking,omitempty"`
}

// GenerationDeltaEvent is the public live-only SSE payload. It carries no
// replay cursor; consumers discard previews when Attempt increases.
type GenerationDeltaEvent struct {
	Type         string    `json:"type"`
	SessionID    string    `json:"session_id"`
	InvocationID string    `json:"invocation_id"`
	Attempt      int64     `json:"attempt"`
	Iteration    int       `json:"iteration"`
	ContentIndex int       `json:"content_index"`
	Text         string    `json:"text,omitempty"`
	Thinking     string    `json:"thinking,omitempty"`
	EmittedAt    time.Time `json:"emitted_at"`
}

type StreamResyncEvent struct {
	Type         string  `json:"type"`
	SessionID    string  `json:"session_id"`
	InvocationID *string `json:"invocation_id"`
	Reason       string  `json:"reason"`
}

type StreamEndEvent struct {
	Type         string  `json:"type"`
	SessionID    string  `json:"session_id"`
	InvocationID *string `json:"invocation_id"`
	Reason       string  `json:"reason"`
	ResumeCursor string  `json:"resume_cursor"`
}
