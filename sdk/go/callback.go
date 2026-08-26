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

type CallbackEnvelope struct {
	Nvoken generated.ToolCallbackContext `json:"nvoken"`
	Input  json.RawMessage               `json:"input"`
}

type VerifiedCallback struct {
	Envelope   CallbackEnvelope
	RawBody    []byte
	DeliveryID string
	ToolCallID string
	ToolName   string
	KeyID      string
	KeyVersion int64
	Timestamp  time.Time
}

// VerifyCallback checks one tool-callback delivery and returns its signed
// body. The signature scheme is shared with VerifyWebhook; only the checks
// below, which are about what a callback body must say, are particular to it.
func VerifyCallback(key []byte, header http.Header, rawBody []byte, now time.Time) (VerifiedCallback, error) {
	delivery, err := verifySignedDelivery(key, header, rawBody, now)
	if err != nil {
		return VerifiedCallback{}, err
	}
	deliveryID := delivery.DeliveryID
	toolCallID := delivery.IdempotencyKey
	var envelope CallbackEnvelope
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return VerifiedCallback{}, fmt.Errorf("decode verified callback: %w", err)
	}
	if envelope.Nvoken.SchemaVersion != 2 {
		return VerifiedCallback{}, fmt.Errorf("unsupported callback schema version")
	}
	if envelope.Nvoken.DeliveryID != deliveryID || envelope.Nvoken.ToolCallID != toolCallID {
		return VerifiedCallback{}, fmt.Errorf("callback identity header does not match signed body")
	}
	// tool_name is required on the wire, so a missing one is a sender that is
	// not nvoken or a body that is not a callback. Failing here keeps the
	// dispatch below it total: no receiver needs a branch for "no name".
	if envelope.Nvoken.ToolName == "" {
		return VerifiedCallback{}, fmt.Errorf("callback envelope is missing tool_name")
	}
	return VerifiedCallback{
		Envelope:   envelope,
		RawBody:    append([]byte(nil), rawBody...),
		DeliveryID: deliveryID,
		ToolCallID: toolCallID,
		ToolName:   envelope.Nvoken.ToolName,
		KeyID:      delivery.KeyID,
		KeyVersion: delivery.KeyVersion,
		Timestamp:  delivery.Timestamp,
	}, nil
}

// CallbackReply is the HTTP answer to one callback delivery. Rendering it is
// left to the host's web framework: write Status, and Body when it is not
// empty.
type CallbackReply struct {
	Status int
	Body   []byte
}

// CallbackResult settles the ToolCall inline. Content may be any JSON value,
// encoded to at most 256 KiB and 32 levels of nesting. The turn resumes as soon
// as nvoken records the reply.
func CallbackResult(content any, isError bool) (CallbackReply, error) {
	body := map[string]any{"content": content}
	if isError {
		body["is_error"] = true
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return CallbackReply{}, fmt.Errorf("encode callback result: %w", err)
	}
	return CallbackReply{Status: http.StatusOK, Body: encoded}, nil
}

// AcknowledgeCallback accepts delivery without settling the ToolCall, for work
// that will outlive this tool's reply deadline — its declared
// timeout_seconds, or the App's default when it declares none. Settle it later
// with Raw().SubmitHostToolResults, reusing the delivery's ToolCall ID.
//
// This trades away the fail-loud guarantee. nvoken marks an unacknowledged
// delivery failed once its retries are exhausted, so the turn always moves on.
// An acknowledged call instead waits under the host's responsibility, bounded
// only by the Turn's Limits.WaitingTimeoutSeconds. Acknowledge only when
// something durable will settle the call.
func AcknowledgeCallback() CallbackReply {
	return CallbackReply{Status: http.StatusAccepted}
}

// CallbackResultStore is where a receiver records what it already answered, so
// a redelivery returns that answer instead of running the tool again.
//
// Both operations are needed and they are needed in this order. Find runs
// before the tool does, because a redelivery that re-runs it repeats every
// effect it had. PutIfAbsent runs after, because two deliveries of one ToolCall
// can be in flight at once and only one answer may win.
type CallbackResultStore interface {
	Find(ctx context.Context, toolCallID string) (reply CallbackReply, found bool, err error)
	PutIfAbsent(ctx context.Context, toolCallID string, reply CallbackReply) (stored CallbackReply, inserted bool, err error)
}

// CallbackToolHandler runs one tool for one delivery. Return the reply —
// CallbackResult to settle the call, AcknowledgeCallback to take it away and
// settle it later.
//
// A tool that failed still returns: CallbackResult(reason, true) settles the
// call carrying is_error, which the model can read and correct itself against.
// Returning an error means something in the receiver failed, not the tool, and
// answers 503 so nvoken redelivers.
type CallbackToolHandler func(ctx context.Context, delivery VerifiedCallback) (CallbackReply, error)

// CallbackOutcome is what a receiver did with one delivery.
//
// It is what the status alone cannot say — a 200 that replayed a recorded
// answer did no work.
type CallbackOutcome string

const (
	CallbackSettled      CallbackOutcome = "settled"
	CallbackAcknowledged CallbackOutcome = "acknowledged"
	CallbackReplayed     CallbackOutcome = "replayed"
	CallbackRefused      CallbackOutcome = "refused"
	CallbackFailed       CallbackOutcome = "failed"
)

// CallbackDelivery is one answered delivery: the reply the host writes, and
// enough about what happened to log it.
//
// Reason is a stable token for a log line and is never echoed into the reply
// body, because nvoken is not the audience for it and a refused sender should
// learn nothing.
type CallbackDelivery struct {
	Reply   CallbackReply
	Outcome CallbackOutcome
	Reason  string
	// Delivery is set once the signature checked out, whatever happened after.
	Delivery VerifiedCallback
	Verified bool
	// Cause is the error behind a refused or failed outcome, for the host's
	// logger. It is never rendered into the reply.
	Cause error
}

// CallbackReceiver answers a tool-callback endpoint: key selection, signature
// verification, dispatch on the signed tool name, deduplication, and the reply
// discipline nvoken reads.
//
// That discipline is the part worth having in one place, because every status
// here is a decision about whether nvoken tries again:
//
//	no keys configured                        503  an operator error, still fixable inside the retry window
//	signing identity not held                 401  a real identity failure; redelivery reproduces it
//	signature, timestamp, or envelope invalid 401  the same bytes fail the same way
//	no handler for the signed tool name       400  nothing here can ever run it
//	handler returned a reply             200/202  the tool answered, or took the call away
//	handler returned an error                 503  the receiver failed, not the tool — and the store makes retrying safe
//
// A tool that failed is not a receiver that failed. Settle it with
// CallbackResult(reason, true): the model can read a tool error and correct
// itself, while a 5xx only has nvoken deliver the same doomed call again.
//
// The endpoint is public because nvoken must reach it, and it is not anonymous:
// nothing below the signature check runs until the HMAC over the raw bytes
// verifies.
type CallbackReceiver struct {
	keys  map[deliveryKeySlot][]byte
	tools map[string]CallbackToolHandler
	store CallbackResultStore
	now   func() time.Time
}

// CallbackReceiverOptions configures a receiver.
type CallbackReceiverOptions struct {
	// Keys is every secret this endpoint accepts. Two entries span a rotation.
	Keys []DeliverySigningKey
	// Tools maps the tool name nvoken signs into the body to its handler.
	Tools map[string]CallbackToolHandler
	// Store is where answered ToolCalls are recorded. Leave it nil only when
	// every tool here is safe to run twice: without a store, a redelivery runs
	// the tool again.
	Store CallbackResultStore
	Now   func() time.Time
}

// NewCallbackReceiver builds a receiver, refusing a key table that could only
// fail later at delivery time.
func NewCallbackReceiver(options CallbackReceiverOptions) (*CallbackReceiver, error) {
	table, err := deliverySigningKeys(options.Keys)
	if err != nil {
		return nil, err
	}
	tools := make(map[string]CallbackToolHandler, len(options.Tools))
	for name, handler := range options.Tools {
		if name == "" || handler == nil {
			return nil, fmt.Errorf("callback tool handler is missing a name or a function")
		}
		tools[name] = handler
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &CallbackReceiver{keys: table, tools: tools, store: options.Store, now: now}, nil
}

// Handle answers one delivery. It never returns an error: everything that can
// go wrong is a status nvoken understands, and the outcome says which.
func (r *CallbackReceiver) Handle(ctx context.Context, header http.Header, rawBody []byte) CallbackDelivery {
	secret, err := selectDeliveryKey(r.keys, header)
	if err == nil {
		var verified VerifiedCallback
		verified, err = VerifyCallback(secret, header, rawBody, r.now())
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
		return CallbackDelivery{
			Reply:   CallbackReply{Status: http.StatusServiceUnavailable},
			Outcome: CallbackFailed,
			Reason:  reason,
			Cause:   err,
		}
	}
	return CallbackDelivery{
		Reply:   CallbackReply{Status: http.StatusUnauthorized},
		Outcome: CallbackRefused,
		Reason:  reason,
		Cause:   err,
	}
}

func (r *CallbackReceiver) dispatch(ctx context.Context, verified VerifiedCallback) CallbackDelivery {
	answered := CallbackDelivery{Delivery: verified, Verified: true}
	handler, known := r.tools[verified.ToolName]
	if !known {
		answered.Reply = CallbackReply{Status: http.StatusBadRequest}
		answered.Outcome, answered.Reason = CallbackRefused, "unknown_tool"
		return answered
	}

	failed := func(reason string, err error) CallbackDelivery {
		answered.Reply = CallbackReply{Status: http.StatusServiceUnavailable}
		answered.Outcome, answered.Reason, answered.Cause = CallbackFailed, reason, err
		return answered
	}

	if r.store != nil {
		recorded, found, err := r.store.Find(ctx, verified.ToolCallID)
		if err != nil {
			return failed("store_unreadable", err)
		}
		if found {
			answered.Reply = recorded
			answered.Outcome, answered.Reason = CallbackReplayed, "recorded"
			return answered
		}
	}

	reply, err := handler(ctx, verified)
	if err != nil {
		return failed("handler_failed", err)
	}
	if r.store == nil {
		answered.Reply = reply
		answered.Outcome, answered.Reason = settledOrAcknowledged(reply), "ran"
		return answered
	}

	stored, inserted, err := r.store.PutIfAbsent(ctx, verified.ToolCallID, reply)
	if err != nil {
		return failed("store_unwritable", err)
	}
	answered.Reply = stored
	if inserted {
		answered.Outcome, answered.Reason = settledOrAcknowledged(stored), "ran"
		return answered
	}
	// Another delivery of the same ToolCall answered first. Its reply is the one
	// nvoken already has, so returning ours would be a second answer to a call
	// that has one.
	answered.Outcome, answered.Reason = CallbackReplayed, "raced"
	return answered
}

func settledOrAcknowledged(reply CallbackReply) CallbackOutcome {
	if reply.Status == http.StatusAccepted {
		return CallbackAcknowledged
	}
	return CallbackSettled
}
