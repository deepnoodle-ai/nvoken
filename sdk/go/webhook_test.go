package nvoken

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	webhookKey        = "0123456789abcdef0123456789abcdef"
	webhookDeliveryID = "dlvr_1"
)

var webhookNow = time.Unix(1784635200, 0)

func endedWebhookBody(deliveryID string, sequence string) string {
	return `{"nvoken":{"schema_version":2,"delivery_id":"` + deliveryID +
		`","event":"turn.ended","sequence":` + sequence + `,"turn_id":"turn_1",` +
		`"conversation_id":null,"memory_space_id":null,"content_expires_at":null,` +
		`"behavior_source":{"kind":"inline","digest":"sha256:` + strings.Repeat("0", 64) + `"},` +
		`"tenant_key":"acme","user_key":null},` +
		`"turn":{"status":"failed","failure_code":"context_window_exceeded"}}`
}

func TestVerifyWebhookReadsTargetTurnBody(t *testing.T) {
	body := endedWebhookBody(webhookDeliveryID, "2")
	header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
	verified, err := VerifyWebhook([]byte(webhookKey), header, []byte(body), webhookNow)
	if err != nil {
		t.Fatalf("verify webhook: %v", err)
	}
	if verified.Event != WebhookEventEnded || verified.Sequence != 2 || verified.TurnID != "turn_1" {
		t.Fatalf("verified webhook = %#v", verified)
	}
	if verified.Envelope.Turn.Status != TurnStatus("failed") || verified.Envelope.Turn.FailureCode == nil {
		t.Fatalf("webhook subject = %#v", verified.Envelope.Turn)
	}
}

func TestVerifyWebhookRefusesTamperingAndUnknownEvent(t *testing.T) {
	body := endedWebhookBody(webhookDeliveryID, "2")
	header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
	if _, err := VerifyWebhook([]byte(webhookKey), header, []byte(endedWebhookBody(webhookDeliveryID, "3")), webhookNow); err == nil {
		t.Fatal("tampered body was accepted")
	}
	unknown := strings.Replace(body, "turn.ended", "turn.running", 1)
	header = signedDeliveryHeader(webhookKey, []byte(unknown), webhookDeliveryID, webhookDeliveryID, webhookNow)
	if _, err := VerifyWebhook([]byte(webhookKey), header, []byte(unknown), webhookNow); err == nil {
		t.Fatal("unknown event was accepted")
	}
}

func TestWebhookOrderingAndReplies(t *testing.T) {
	body := endedWebhookBody(webhookDeliveryID, "2")
	header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
	verified, err := VerifyWebhook([]byte(webhookKey), header, []byte(body), webhookNow)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Supersedes(1) || verified.Supersedes(2) {
		t.Fatal("webhook sequence folding is wrong")
	}
	if accept := AcceptWebhook(); accept.Status != http.StatusOK || WebhookStatusIsRetried(accept.Status) {
		t.Fatalf("accept reply = %#v", accept)
	}
	if retry := RetryWebhook(); !WebhookStatusIsRetried(retry.Status) {
		t.Fatalf("retry reply = %#v", retry)
	}
}
