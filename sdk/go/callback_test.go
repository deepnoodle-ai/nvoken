package nvoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// tool_name is required on the wire, and a receiver dispatches on it. A body
// without one is not a callback this SDK can hand to a handler, so verification
// rejects it rather than passing an empty name down and leaving every receiver
// to write the same guard.
func TestVerifyCallbackRequiresToolName(t *testing.T) {
	const (
		key        = "0123456789abcdef0123456789abcdef"
		deliveryID = "cbdy_019b0a12-8d51-7f34-aed2-0e07c1bdb326"
		toolCallID = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
	)
	now := time.Unix(1784635200, 0)
	body := `{"nvoken":{"schema_version":1,"delivery_id":"` + deliveryID +
		`","tool_call_id":"` + toolCallID +
		`","invocation_id":"invk_1","session_id":"sesn_1","agent_key":"support"},"input":{}}`
	header := signedCallbackHeader(key, []byte(body), deliveryID, toolCallID, now)
	if _, err := VerifyCallback([]byte(key), header, []byte(body), now); err == nil {
		t.Fatal("a correctly signed envelope with no tool_name was accepted")
	}

	named := `{"nvoken":{"schema_version":1,"delivery_id":"` + deliveryID +
		`","tool_call_id":"` + toolCallID +
		`","tool_name":"open_ticket","invocation_id":"invk_1","session_id":"sesn_1","agent_key":"support"},"input":{}}`
	header = signedCallbackHeader(key, []byte(named), deliveryID, toolCallID, now)
	verified, err := VerifyCallback([]byte(key), header, []byte(named), now)
	if err != nil || verified.ToolName != "open_ticket" {
		t.Fatalf("verified tool name = %q err=%v", verified.ToolName, err)
	}
}

func signedCallbackHeader(key string, body []byte, deliveryID, toolCallID string, now time.Time) http.Header {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = fmt.Fprintf(mac, "v1.%s.%d.", deliveryID, now.Unix())
	_, _ = mac.Write(body)
	header := make(http.Header)
	header.Set("X-Nvoken-Signature-Version", "v1")
	header.Set("X-Nvoken-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	header.Set("X-Nvoken-Timestamp", strconv.FormatInt(now.Unix(), 10))
	header.Set("X-Nvoken-Delivery-Id", deliveryID)
	header.Set("X-Nvoken-Signing-Key-Id", "key_1")
	header.Set("X-Nvoken-Signing-Key-Version", "1")
	header.Set("Idempotency-Key", toolCallID)
	return header
}
