package ports

import (
	"context"
	"encoding/json"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

// GenerationDeltaEmitter receives normalized, live-only model output. An
// emitter must return quickly and must not make model execution depend on live
// delivery.
type GenerationDeltaEmitter func(domain.GenerationDelta)

// StreamingModelGenerator is the additive live-output extension. Generators
// that do not implement it continue through ModelGenerator.Generate.
type StreamingModelGenerator interface {
	GenerateStream(context.Context, domain.GenerationRequest, GenerationDeltaEmitter) (domain.GenerationResponse, error)
}

// LiveEvent is a lossy internal fan-out envelope. AccountID is a routing and
// isolation boundary and is not included in the public payload.
type LiveEvent struct {
	Type      string
	AccountID string
	SessionID string
	Payload   json.RawMessage
}

// LiveEventPublisher never grants execution ownership and may drop events.
type LiveEventPublisher interface {
	Publish(context.Context, LiveEvent)
}

// LiveEventBus fans ephemeral events to local SSE consumers. Subscribe must
// establish delivery before returning so callers can subscribe before their
// authoritative Postgres drain.
type LiveEventBus interface {
	LiveEventPublisher
	Subscribe(context.Context, string, string) LiveSubscription
}

type LiveSubscription interface {
	Events() <-chan LiveEvent
	TakeGap() bool
	Close()
}
