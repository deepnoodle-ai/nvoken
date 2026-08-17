package nvoken

import (
	"net/http"
	"testing"
	"time"
)

const (
	webhookKey        = "0123456789abcdef0123456789abcdef"
	webhookDeliveryID = "dlvr_019b0a12-8d51-7f34-aed2-0e07c1bdb326"
)

var webhookNow = time.Unix(1784635200, 0)

func endedWebhookBody(deliveryID string, sequence string) string {
	return `{"nvoken":{"schema_version":1,"delivery_id":"` + deliveryID +
		`","event":"invocation.ended","sequence":` + sequence +
		`,"invocation_id":"inv_1","session_id":"sess_1","agent_key":"support","tenant_key":"acme"},` +
		`"invocation":{"status":"failed","failure_code":"context_window_exceeded"}}`
}

// A callback and a webhook are signed identically, so verification is one path
// in this SDK. This drives the webhook half of it end to end.
func TestVerifyWebhookReadsTheSignedBody(t *testing.T) {
	body := endedWebhookBody(webhookDeliveryID, "2")
	header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
	verified, err := VerifyWebhook([]byte(webhookKey), header, []byte(body), webhookNow)
	if err != nil {
		t.Fatalf("verify webhook: %v", err)
	}
	if verified.Event != WebhookEventEnded || verified.Sequence != 2 {
		t.Fatalf("event = %q sequence = %d", verified.Event, verified.Sequence)
	}
	if verified.InvocationID != "inv_1" || verified.DeliveryID != webhookDeliveryID {
		t.Fatalf("invocation = %q delivery = %q", verified.InvocationID, verified.DeliveryID)
	}
	if verified.Envelope.Invocation.Status != InvocationFailed {
		t.Fatalf("status = %q", verified.Envelope.Invocation.Status)
	}
	if verified.Envelope.Invocation.FailureCode == nil ||
		*verified.Envelope.Invocation.FailureCode != "context_window_exceeded" {
		t.Fatalf("failure code = %v", verified.Envelope.Invocation.FailureCode)
	}
	if verified.Envelope.Nvoken.TenantKey == nil || *verified.Envelope.Nvoken.TenantKey != "acme" {
		t.Fatalf("tenant key = %v", verified.Envelope.Nvoken.TenantKey)
	}
}

// A signature checked against the wrong body is the whole point of signing, so
// a tampered body must not reach a handler even when every header is intact.
func TestVerifyWebhookRefusesATamperedBody(t *testing.T) {
	body := endedWebhookBody(webhookDeliveryID, "2")
	header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
	tampered := endedWebhookBody(webhookDeliveryID, "3")
	if _, err := VerifyWebhook([]byte(webhookKey), header, []byte(tampered), webhookNow); err == nil {
		t.Fatal("a body that was not the one signed was accepted")
	}
}

// An event outside the closed set is refused rather than handed down with a
// name no receiver has a branch for. Answering such a delivery successfully
// would settle a transition the host in fact ignored.
func TestVerifyWebhookRefusesAnUnknownEventOrSequence(t *testing.T) {
	for name, body := range map[string]string{
		"unknown event": `{"nvoken":{"schema_version":1,"delivery_id":"` + webhookDeliveryID +
			`","event":"invocation.running","sequence":1,"invocation_id":"inv_1",` +
			`"session_id":"sess_1","agent_key":"support"},"invocation":{"status":"running"}}`,
		"absent event": `{"nvoken":{"schema_version":1,"delivery_id":"` + webhookDeliveryID +
			`","sequence":1,"invocation_id":"inv_1","session_id":"sess_1",` +
			`"agent_key":"support"},"invocation":{"status":"completed"}}`,
		"zero sequence": endedWebhookBody(webhookDeliveryID, "0"),
	} {
		t.Run(name, func(t *testing.T) {
			header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
			if _, err := VerifyWebhook([]byte(webhookKey), header, []byte(body), webhookNow); err == nil {
				t.Fatal("the delivery was accepted")
			}
		})
	}
}

// The delivery id appears in a header, in the idempotency key, and inside the
// signed body. Only the body is signed, so the headers are checked against it
// rather than trusted.
func TestVerifyWebhookRefusesIdentityThatDisagreesWithTheBody(t *testing.T) {
	body := endedWebhookBody("dlvr_019b0a12-8d51-7f34-aed2-000000000099", "1")
	header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
	if _, err := VerifyWebhook([]byte(webhookKey), header, []byte(body), webhookNow); err == nil {
		t.Fatal("a delivery id disagreeing with the signed body was accepted")
	}

	// On a webhook the idempotency key is the delivery id. A different value
	// there means this is not a webhook delivery.
	body = endedWebhookBody(webhookDeliveryID, "1")
	header = signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, "call_1", webhookNow)
	if _, err := VerifyWebhook([]byte(webhookKey), header, []byte(body), webhookNow); err == nil {
		t.Fatal("an idempotency key that was not the delivery id was accepted")
	}
}

// Delivery is at least once and a redelivery can land after a later
// transition, so folding is by sequence rather than by arrival.
func TestWebhookSupersedesFoldsBySequenceNotArrival(t *testing.T) {
	body := endedWebhookBody(webhookDeliveryID, "2")
	header := signedDeliveryHeader(webhookKey, []byte(body), webhookDeliveryID, webhookDeliveryID, webhookNow)
	verified, err := VerifyWebhook([]byte(webhookKey), header, []byte(body), webhookNow)
	if err != nil {
		t.Fatalf("verify webhook: %v", err)
	}
	if !verified.Supersedes(0) || !verified.Supersedes(1) {
		t.Fatal("a later transition did not supersede an earlier applied one")
	}
	if verified.Supersedes(2) || verified.Supersedes(3) {
		t.Fatal("a repeat or a stale redelivery superseded what was already applied")
	}
}

// The reply discipline decides whether a transition is ever delivered again,
// so the two constructors must land on the right side of it.
func TestWebhookRepliesLandOnTheRightSideOfRedelivery(t *testing.T) {
	if accept := AcceptWebhook(); accept.Status != http.StatusOK || WebhookStatusIsRetried(accept.Status) {
		t.Fatalf("accept = %d, retried = %v", accept.Status, WebhookStatusIsRetried(accept.Status))
	}
	if retry := RetryWebhook(); !WebhookStatusIsRetried(retry.Status) {
		t.Fatalf("retry = %d is not redelivered", retry.Status)
	}
}
