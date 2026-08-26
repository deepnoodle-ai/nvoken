package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

// WebhookEnvelope is the signed body of one Turn webhook.
//
// It mirrors CallbackEnvelope: everything nvoken asserts sits under Nvoken,
// and the subject of the delivery sits beside it.
type WebhookEnvelope struct {
	Nvoken WebhookContext `json:"nvoken"`
	Turn   WebhookSubject `json:"turn"`
}

type WebhookContext = generated.TurnWebhookContext

// WebhookSubject is a pointer to the turn, not a projection of it. It carries
// no transcript content, tool arguments, structured output, usage, provenance,
// or failure message: read GetTurn or GetTurnResult for anything
// beyond what is here.
type WebhookSubject = generated.TurnWebhookSubject

// VerifiedWebhook is one Turn webhook whose signature has been checked.
type VerifiedWebhook struct {
	Envelope   WebhookEnvelope
	RawBody    []byte
	DeliveryID string
	// Event is read from the signed body. The endpoint URL may carry an
	// unsigned per-event suffix; that belongs in logs, not in a dispatch
	// decision.
	Event          WebhookEvent
	Sequence       int64
	TurnID         string
	ConversationID *string
	KeyID          string
	KeyVersion     int64
	Timestamp      time.Time
}

// VerifyWebhook checks one Turn webhook delivery and returns its signed
// body. It shares its signature scheme with VerifyCallback, so a host that
// receives both implements verification once and dispatches on what the
// verified body says.
//
// The key is the App's webhook-purpose signing key. Callbacks are signed with
// the callback-purpose key, so a receiver serving both endpoints holds two
// keys and must not try either against the other's deliveries.
func VerifyWebhook(key []byte, header http.Header, rawBody []byte, now time.Time) (VerifiedWebhook, error) {
	delivery, err := verifySignedDelivery(key, header, rawBody, now)
	if err != nil {
		return VerifiedWebhook{}, err
	}
	var envelope WebhookEnvelope
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return VerifiedWebhook{}, fmt.Errorf("decode verified webhook: %w", err)
	}
	if envelope.Nvoken.SchemaVersion != 2 {
		return VerifiedWebhook{}, fmt.Errorf("unsupported webhook schema version")
	}
	// The Idempotency-Key on a webhook is the delivery id, so both headers
	// pin the same fact and both must agree with the body that was signed.
	if envelope.Nvoken.DeliveryID != delivery.DeliveryID ||
		delivery.IdempotencyKey != delivery.DeliveryID {
		return VerifiedWebhook{}, fmt.Errorf("webhook identity header does not match signed body")
	}
	// Refusing an unknown event here keeps the dispatch below it total. A new
	// event nvoken adds later reaches a receiver that has no branch for it,
	// and answering it as if it were understood would settle a delivery the
	// host in fact ignored.
	if !envelope.Nvoken.Event.Valid() {
		return VerifiedWebhook{}, fmt.Errorf("unsupported webhook event %q", envelope.Nvoken.Event)
	}
	if envelope.Nvoken.Sequence < 1 {
		return VerifiedWebhook{}, fmt.Errorf("webhook sequence must be positive")
	}
	return VerifiedWebhook{
		Envelope:       envelope,
		RawBody:        append([]byte(nil), rawBody...),
		DeliveryID:     delivery.DeliveryID,
		Event:          WebhookEvent(envelope.Nvoken.Event),
		Sequence:       envelope.Nvoken.Sequence,
		TurnID:         envelope.Nvoken.TurnID,
		ConversationID: envelope.Nvoken.ConversationID,
		KeyID:          delivery.KeyID,
		KeyVersion:     delivery.KeyVersion,
		Timestamp:      delivery.Timestamp,
	}, nil
}

// Supersedes reports whether this delivery describes a later transition of its
// Turn than the one already applied.
//
// Delivery is at least once, so the same transition can arrive twice and a
// redelivery can land after a later one. Keep the highest sequence applied per
// Turn and fold only what supersedes it; a receiver that applies
// whichever arrived last rolls its own state backwards. Pass 0 for an
// Turn nothing has been applied for yet.
//
// This is also the dedup: a repeat carries a sequence already applied, so
// nothing further is needed to make handling idempotent. Answer it with
// AcceptWebhook all the same — it was delivered, and asking for redelivery of
// something already handled only produces the same repeat.
func (w VerifiedWebhook) Supersedes(appliedSequence int64) bool {
	return w.Sequence > appliedSequence
}

// WebhookReply is the HTTP answer to one webhook delivery. nvoken ignores the
// response body, so only the status carries meaning, and no answer ever
// affects the Turn the webhook describes.
type WebhookReply struct {
	Status int
}

// AcceptWebhook takes responsibility for the delivery. nvoken will not send it
// again.
func AcceptWebhook() WebhookReply {
	return WebhookReply{Status: http.StatusOK}
}

// RetryWebhook asks nvoken to deliver again, for a receiver that could not
// record the transition right now — its store was unreachable, or it is
// shedding load. Retries are bounded, so a receiver that answers this forever
// still ends with a transition nobody recorded; reconcile ended Turns from
// the exact Turn-list endpoint as a backstop.
func RetryWebhook() WebhookReply {
	return WebhookReply{Status: http.StatusServiceUnavailable}
}

// WebhookStatusIsRetried reports whether nvoken redelivers after a receiver
// answers with this status.
//
// Any 5xx is retried, as are 408, 425, and 429. Every other non-2xx answer —
// 400, 401, 403, 404, 409, 410, 422 among them — is permanent, and the
// transition it described is never delivered again. Refusing a body that
// genuinely failed verification with 401 is therefore right: redelivering it
// would fail the same way. Refusing one because the signing key could not be
// read is not, and should answer RetryWebhook instead, since the two are
// indistinguishable to nvoken and only one of them is the sender's fault.
func WebhookStatusIsRetried(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}

// WebhookEventHandler records one transition. Returning an error asks nvoken to
// deliver it again, so return one when the receiver could not record it and nil
// when it did.
type WebhookEventHandler func(ctx context.Context, delivery VerifiedWebhook) error

// WebhookOutcome is what a receiver did with one webhook delivery.
type WebhookOutcome string

const (
	WebhookHandled WebhookOutcome = "handled"
	WebhookIgnored WebhookOutcome = "ignored"
	WebhookRefused WebhookOutcome = "refused"
	WebhookFailed  WebhookOutcome = "failed"
)

// WebhookDelivery is one answered delivery: the reply the host writes, and
// enough about what happened to log it.
type WebhookDelivery struct {
	Reply   WebhookReply
	Outcome WebhookOutcome
	// Reason is a stable token for a log line. It is never echoed to nvoken,
	// which ignores webhook bodies.
	Reason   string
	Delivery VerifiedWebhook
	Verified bool
	Cause    error
}

// WebhookReceiver answers a Turn-webhook endpoint. It is the callback
// receiver's twin — same key table, same reply discipline — because nvoken
// signs both deliveries the same way.
//
// It is a separate receiver rather than a mode of one, because the two
// endpoints hold different keys: callbacks are signed with the App's
// callback-purpose key and webhooks with its webhook-purpose key, and neither
// may be tried against the other's deliveries.
//
//	no keys configured                        503  an operator error, still fixable inside the retry window
//	signing identity not held                 401  a real identity failure; redelivery reproduces it
//	signature, timestamp, or envelope invalid 401  the same bytes fail the same way
//	no handler for the signed event           200  it was delivered; redelivering finds the same absent handler
//	handler returned nil                      200  the transition is recorded
//	handler returned an error                 503  the receiver could not record it, so ask for it again
//
// Ordering stays yours. Delivery is at least once and out of order, so the
// highest applied sequence per Turn has to be read and written in the same
// transaction as the state it guards — which is the host's transaction, not one
// this kit can open. Call VerifiedWebhook.Supersedes inside it. A superseded
// delivery is still a delivery: record nothing and return nil, so it answers
// 200.
type WebhookReceiver struct {
	keys   map[deliveryKeySlot][]byte
	events map[WebhookEvent]WebhookEventHandler
	now    func() time.Time
}

// WebhookReceiverOptions configures a receiver.
type WebhookReceiverOptions struct {
	// Keys is every secret this endpoint accepts. Two entries span a rotation.
	Keys []DeliverySigningKey
	// Events maps the event nvoken signs into the body to its handler.
	Events map[WebhookEvent]WebhookEventHandler
	Now    func() time.Time
}

// NewWebhookReceiver builds a receiver, refusing a key table that could only
// fail later at delivery time.
func NewWebhookReceiver(options WebhookReceiverOptions) (*WebhookReceiver, error) {
	table, err := deliverySigningKeys(options.Keys)
	if err != nil {
		return nil, err
	}
	events := make(map[WebhookEvent]WebhookEventHandler, len(options.Events))
	for event, handler := range options.Events {
		if event == "" || handler == nil {
			return nil, fmt.Errorf("webhook event handler is missing an event or a function")
		}
		events[event] = handler
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &WebhookReceiver{keys: table, events: events, now: now}, nil
}

// Handle answers one delivery. It never returns an error: everything that can
// go wrong is a status nvoken understands, and the outcome says which.
func (r *WebhookReceiver) Handle(ctx context.Context, header http.Header, rawBody []byte) WebhookDelivery {
	secret, err := selectDeliveryKey(r.keys, header)
	if err == nil {
		var verified VerifiedWebhook
		verified, err = VerifyWebhook(secret, header, rawBody, r.now())
		if err == nil {
			return r.dispatch(ctx, verified)
		}
	}
	keyErr := &DeliveryKeyError{}
	reason, retryable := "invalid_signature", false
	if errors.As(err, &keyErr) {
		reason, retryable = keyErr.Reason, keyErr.Retryable
	}
	if retryable {
		return WebhookDelivery{Reply: RetryWebhook(), Outcome: WebhookFailed, Reason: reason, Cause: err}
	}
	return WebhookDelivery{
		Reply:   WebhookReply{Status: http.StatusUnauthorized},
		Outcome: WebhookRefused,
		Reason:  reason,
		Cause:   err,
	}
}

func (r *WebhookReceiver) dispatch(ctx context.Context, verified VerifiedWebhook) WebhookDelivery {
	answered := WebhookDelivery{Delivery: verified, Verified: true}
	handler, known := r.events[verified.Event]
	// A subscribed event with no handler is a gap in this receiver, not a failure
	// of the delivery. Answering 503 would only spend nvoken's bounded retries
	// reaching the same absent handler, and lose it anyway.
	if !known {
		answered.Reply = AcceptWebhook()
		answered.Outcome, answered.Reason = WebhookIgnored, "unhandled_event"
		return answered
	}
	if err := handler(ctx, verified); err != nil {
		answered.Reply = RetryWebhook()
		answered.Outcome, answered.Reason, answered.Cause = WebhookFailed, "handler_failed", err
		return answered
	}
	answered.Reply = AcceptWebhook()
	answered.Outcome, answered.Reason = WebhookHandled, "recorded"
	return answered
}
