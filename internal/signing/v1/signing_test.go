package signing

import "testing"

func TestSignVerifyRejectsTampering(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"input":{"value":1}}`)
	deliveryID := "cbdy_018f0000-0000-7000-8000-000000000001"
	timestamp := int64(1750000000)
	signature := Sign(key, body, deliveryID, timestamp)
	if err := Verify(key, body, deliveryID, timestamp, signature); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := Verify([]byte("abcdef0123456789abcdef0123456789"), body, deliveryID, timestamp, signature); err == nil {
		t.Fatal("Verify() accepted a different signing key")
	}
	for name, test := range map[string]struct {
		body       []byte
		deliveryID string
		timestamp  int64
	}{
		"body": {
			body:       []byte(`{"input":{"value":2}}`),
			deliveryID: deliveryID,
			timestamp:  timestamp,
		},
		"delivery": {
			body:       body,
			deliveryID: deliveryID + "x",
			timestamp:  timestamp,
		},
		"timestamp": {
			body:       body,
			deliveryID: deliveryID,
			timestamp:  timestamp + 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(key, test.body, test.deliveryID, test.timestamp, signature); err == nil {
				t.Fatal("Verify() accepted tampering")
			}
		})
	}
}
