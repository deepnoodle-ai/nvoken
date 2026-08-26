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

func TestVerifyCallbackRequiresTargetToolName(t *testing.T) {
	const (
		key        = "0123456789abcdef0123456789abcdef"
		deliveryID = "dlvr_1"
		toolCallID = "call_1"
	)
	now := time.Unix(1784635200, 0)
	context := `"schema_version":2,"delivery_id":"` + deliveryID + `","tool_call_id":"` + toolCallID +
		`","turn_id":"turn_1","conversation_id":null,"memory_space_id":null,"content_expires_at":null,` +
		`"behavior_source":{"kind":"inline","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"},` +
		`"tenant_key":"acme","user_key":null`
	body := `{"nvoken":{` + context + `},"input":{}}`
	header := signedDeliveryHeader(key, []byte(body), deliveryID, toolCallID, now)
	if _, err := VerifyCallback([]byte(key), header, []byte(body), now); err == nil {
		t.Fatal("signed target callback with no tool_name was accepted")
	}

	named := `{"nvoken":{` + context + `,"tool_name":"open_ticket"},"input":{}}`
	header = signedDeliveryHeader(key, []byte(named), deliveryID, toolCallID, now)
	verified, err := VerifyCallback([]byte(key), header, []byte(named), now)
	if err != nil || verified.ToolName != "open_ticket" || verified.Envelope.Nvoken.TurnID != "turn_1" {
		t.Fatalf("verified callback = %#v err=%v", verified, err)
	}
}

func signedDeliveryHeader(key string, body []byte, deliveryID, idempotencyKey string, now time.Time) http.Header {
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
	header.Set("Idempotency-Key", idempotencyKey)
	return header
}
