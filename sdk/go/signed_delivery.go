package nvoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SignatureTimestampWindow is how far a delivery's signed timestamp may sit
// from the receiver's clock before the delivery is refused.
const SignatureTimestampWindow = 5 * time.Minute

// signedDelivery is one delivery whose signature has been checked, before its
// body is interpreted.
//
// nvoken signs tool callbacks and Invocation webhooks the same way, so
// everything up to and including the HMAC comparison lives here once rather
// than in two copies that have to be kept in step. What differs is only what
// the verified body then means: a callback settles a named ToolCall, a webhook
// reports a transition that already happened.
type signedDelivery struct {
	DeliveryID string
	// IdempotencyKey is the ToolCall id on a callback and the delivery id on a
	// webhook. In both it is the value a receiver deduplicates on.
	IdempotencyKey string
	KeyID          string
	KeyVersion     int64
	Timestamp      time.Time
}

// verifySignedDelivery checks the signature headers and the HMAC over the raw
// body. It reads nothing out of the body, so it is total over both delivery
// kinds and cannot acquire a requirement that belongs to one of them.
func verifySignedDelivery(
	key []byte,
	header http.Header,
	rawBody []byte,
	now time.Time,
) (signedDelivery, error) {
	if len(key) < 32 {
		return signedDelivery{}, fmt.Errorf("delivery signing key must be at least 32 bytes")
	}
	if header.Get("X-Nvoken-Signature-Version") != "v1" {
		return signedDelivery{}, fmt.Errorf("unsupported delivery signature version")
	}
	timestamp, err := strconv.ParseInt(header.Get("X-Nvoken-Timestamp"), 10, 64)
	if err != nil {
		return signedDelivery{}, fmt.Errorf("invalid delivery timestamp")
	}
	when := time.Unix(timestamp, 0)
	if now.Sub(when) > SignatureTimestampWindow || when.Sub(now) > SignatureTimestampWindow {
		return signedDelivery{}, fmt.Errorf("delivery timestamp is outside the accepted window")
	}
	deliveryID := header.Get("X-Nvoken-Delivery-Id")
	idempotencyKey := header.Get("Idempotency-Key")
	keyID := header.Get("X-Nvoken-Signing-Key-Id")
	keyVersion, err := strconv.ParseInt(header.Get("X-Nvoken-Signing-Key-Version"), 10, 64)
	if deliveryID == "" || idempotencyKey == "" || keyID == "" || err != nil || keyVersion <= 0 {
		return signedDelivery{}, fmt.Errorf("delivery identity headers are invalid")
	}
	provided := header.Get("X-Nvoken-Signature")
	if !strings.HasPrefix(provided, "sha256=") {
		return signedDelivery{}, fmt.Errorf("delivery signature must use sha256 prefix")
	}
	providedBytes, err := hex.DecodeString(strings.TrimPrefix(provided, "sha256="))
	if err != nil {
		return signedDelivery{}, fmt.Errorf("delivery signature must be hexadecimal")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(mac, "v1.%s.%d.", deliveryID, timestamp)
	_, _ = mac.Write(rawBody)
	if !hmac.Equal(providedBytes, mac.Sum(nil)) {
		return signedDelivery{}, fmt.Errorf("delivery signature mismatch")
	}
	return signedDelivery{
		DeliveryID:     deliveryID,
		IdempotencyKey: idempotencyKey,
		KeyID:          keyID,
		KeyVersion:     keyVersion,
		Timestamp:      when,
	}, nil
}
