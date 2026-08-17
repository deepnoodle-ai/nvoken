package nvoken

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type CallbackEnvelope struct {
	Nvoken struct {
		SchemaVersion int    `json:"schema_version"`
		DeliveryID    string `json:"delivery_id"`
		ToolCallID    string `json:"tool_call_id"`
		// ToolName is the tool this delivery is for. It is inside the signed
		// body, so a receiver serving several tools dispatches on it directly.
		// Any per-tool path or query suffix on the endpoint URL is unsigned and
		// belongs in logs, not in a dispatch decision.
		ToolName     string  `json:"tool_name"`
		InvocationID string  `json:"invocation_id"`
		SessionID    string  `json:"session_id"`
		AgentKey     string  `json:"agent_key"`
		TenantKey    *string `json:"tenant_key,omitempty"`
	} `json:"nvoken"`
	Input json.RawMessage `json:"input"`
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
	if envelope.Nvoken.SchemaVersion != 1 {
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
// with Client.SubmitToolResults, reusing the delivery's ToolCall ID.
//
// This trades away the fail-loud guarantee. nvoken marks an unacknowledged
// delivery failed once its retries are exhausted, so the turn always moves on.
// An acknowledged call instead waits under the host's responsibility, bounded
// only by the Invocation's Limits.WaitingTimeoutSeconds. Acknowledge only when
// something durable will settle the call.
func AcknowledgeCallback() CallbackReply {
	return CallbackReply{Status: http.StatusAccepted}
}

type CallbackResultStore interface {
	PutIfAbsent(ctx context.Context, toolCallID string, result json.RawMessage) (stored json.RawMessage, inserted bool, err error)
}

func DeduplicateCallbackResult(ctx context.Context, store CallbackResultStore, toolCallID string, result json.RawMessage) (json.RawMessage, bool, error) {
	if store == nil {
		return nil, false, fmt.Errorf("callback result store is required")
	}
	stored, inserted, err := store.PutIfAbsent(ctx, toolCallID, append(json.RawMessage(nil), result...))
	if err != nil {
		return nil, false, err
	}
	return stored, !inserted, nil
}
