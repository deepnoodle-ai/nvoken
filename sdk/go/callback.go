package nvoken

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const CallbackTimestampWindow = 5 * time.Minute

type CallbackEnvelope struct {
	Nvoken struct {
		SchemaVersion int     `json:"schema_version"`
		DeliveryID    string  `json:"delivery_id"`
		ToolCallID    string  `json:"tool_call_id"`
		InvocationID  string  `json:"invocation_id"`
		SessionID     string  `json:"session_id"`
		AgentRef      string  `json:"agent_ref"`
		TenantRef     *string `json:"tenant_ref,omitempty"`
	} `json:"nvoken"`
	Input json.RawMessage `json:"input"`
}

type VerifiedCallback struct {
	Envelope   CallbackEnvelope
	RawBody    []byte
	DeliveryID string
	ToolCallID string
	KeyID      string
	KeyVersion int64
	Timestamp  time.Time
}

func VerifyCallback(key []byte, header http.Header, rawBody []byte, now time.Time) (VerifiedCallback, error) {
	if len(key) < 32 {
		return VerifiedCallback{}, fmt.Errorf("callback signing key must be at least 32 bytes")
	}
	if header.Get("X-Nvoken-Signature-Version") != "v1" {
		return VerifiedCallback{}, fmt.Errorf("unsupported callback signature version")
	}
	timestamp, err := strconv.ParseInt(header.Get("X-Nvoken-Timestamp"), 10, 64)
	if err != nil {
		return VerifiedCallback{}, fmt.Errorf("invalid callback timestamp")
	}
	when := time.Unix(timestamp, 0)
	if now.Sub(when) > CallbackTimestampWindow || when.Sub(now) > CallbackTimestampWindow {
		return VerifiedCallback{}, fmt.Errorf("callback timestamp is outside the accepted window")
	}
	deliveryID := header.Get("X-Nvoken-Delivery-Id")
	toolCallID := header.Get("Idempotency-Key")
	keyID := header.Get("X-Nvoken-Signing-Key-Id")
	keyVersion, err := strconv.ParseInt(header.Get("X-Nvoken-Signing-Key-Version"), 10, 64)
	if deliveryID == "" || toolCallID == "" || keyID == "" || err != nil || keyVersion <= 0 {
		return VerifiedCallback{}, fmt.Errorf("callback identity headers are invalid")
	}
	provided := header.Get("X-Nvoken-Signature")
	if !strings.HasPrefix(provided, "sha256=") {
		return VerifiedCallback{}, fmt.Errorf("callback signature must use sha256 prefix")
	}
	providedBytes, err := hex.DecodeString(strings.TrimPrefix(provided, "sha256="))
	if err != nil {
		return VerifiedCallback{}, fmt.Errorf("callback signature must be hexadecimal")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(mac, "v1.%s.%d.", deliveryID, timestamp)
	_, _ = mac.Write(rawBody)
	if !hmac.Equal(providedBytes, mac.Sum(nil)) {
		return VerifiedCallback{}, fmt.Errorf("callback signature mismatch")
	}
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
	return VerifiedCallback{
		Envelope:   envelope,
		RawBody:    append([]byte(nil), rawBody...),
		DeliveryID: deliveryID,
		ToolCallID: toolCallID,
		KeyID:      keyID,
		KeyVersion: keyVersion,
		Timestamp:  when,
	}, nil
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
