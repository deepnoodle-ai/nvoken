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

// DeliverySigningKey is one secret a receiver will accept deliveries signed
// with.
//
// The key id names the App and the purpose and does not change; the version
// selects the secret within it. Holding two versions is what makes a rotation
// survivable — nvoken mints the next version while still signing with the
// current one, and a signature a receiver cannot verify fails its delivery
// outright rather than retrying, so there is no forgiveness to lean on.
//
// Version is an integer rather than the string it arrives as in configuration
// on purpose. A version that cannot be read as a positive integer makes the
// receiver refuse to be built, which is loud, instead of refusing live
// deliveries, which is permanent.
type DeliverySigningKey struct {
	KeyID   string
	Version int64
	// Secret is at least 32 bytes.
	Secret []byte
}

// DeliveryKeyError says why a receiver would not accept a delivery's signing
// identity.
//
// Retryable is the whole point of the distinction. An unconfigured receiver is
// an operator error this deployment may still fix inside nvoken's retry window.
// A configured receiver that does not know this key version is a real
// signing-identity failure, and asking for redelivery only reproduces it.
type DeliveryKeyError struct {
	Reason    string
	Retryable bool
}

func (e *DeliveryKeyError) Error() string { return "delivery signing key " + e.Reason }

type deliveryKeySlot struct {
	keyID   string
	version int64
}

// deliverySigningKeys normalizes a receiver's key table, refusing at build time
// what would otherwise be refused at delivery time.
//
// Two entries with the same key id and version are a configuration mistake
// rather than a redundancy: which secret wins would decide whether deliveries
// verify, and nothing in the pair says which was meant.
func deliverySigningKeys(keys []DeliverySigningKey) (map[deliveryKeySlot][]byte, error) {
	table := make(map[deliveryKeySlot][]byte, len(keys))
	for _, key := range keys {
		if key.KeyID == "" {
			return nil, fmt.Errorf("delivery signing key is missing a key id")
		}
		if key.Version <= 0 {
			return nil, fmt.Errorf("delivery signing key %s has a non-positive version", key.KeyID)
		}
		if len(key.Secret) < 32 {
			return nil, fmt.Errorf("delivery signing secret for %s v%d must be at least 32 bytes", key.KeyID, key.Version)
		}
		slot := deliveryKeySlot{keyID: key.KeyID, version: key.Version}
		if _, duplicate := table[slot]; duplicate {
			return nil, fmt.Errorf("delivery signing key %s v%d is configured twice", key.KeyID, key.Version)
		}
		table[slot] = append([]byte(nil), key.Secret...)
	}
	return table, nil
}

// selectDeliveryKey picks the secret a delivery says it was signed with, before
// anything parses the body.
//
// Selection reads only the two identity headers, so a delivery signed by an
// identity this receiver does not hold is refused without its body ever being
// decoded, logged, or dispatched on.
func selectDeliveryKey(table map[deliveryKeySlot][]byte, header http.Header) ([]byte, error) {
	if len(table) == 0 {
		return nil, &DeliveryKeyError{Reason: "not_configured", Retryable: true}
	}
	keyID := header.Get("X-Nvoken-Signing-Key-Id")
	version, err := strconv.ParseInt(header.Get("X-Nvoken-Signing-Key-Version"), 10, 64)
	if keyID == "" || err != nil || version <= 0 {
		return nil, &DeliveryKeyError{Reason: "unknown_key"}
	}
	secret, held := table[deliveryKeySlot{keyID: keyID, version: version}]
	if !held {
		return nil, &DeliveryKeyError{Reason: "unknown_key"}
	}
	return secret, nil
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
