package services

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeCallbackResponse(t *testing.T) {
	content, isError, err := decodeCallbackResponse(
		"application/json; charset=utf-8",
		[]byte(`{"content":{"answer":42},"is_error":true}`),
	)
	if err != nil || !isError || !jsonEqual(content, json.RawMessage(`{"answer":42}`)) {
		t.Fatalf("decode valid response = %s, %t, %v", content, isError, err)
	}
	for name, body := range map[string]string{
		"missing content":  `{}`,
		"unknown member":   `{"content":{},"extra":true}`,
		"duplicate member": `{"content":{},"content":true}`,
		"invalid error":    `{"content":{},"is_error":"yes"}`,
		"trailing":         `{"content":{}} {}`,
		"oversized depth":  `{"content":` + strings.Repeat("[", 33) + `null` + strings.Repeat("]", 33) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeCallbackResponse("application/json", []byte(body)); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	if _, _, err := decodeCallbackResponse("text/plain", []byte(`{"content":{}}`)); err == nil {
		t.Fatal("non-JSON media type accepted")
	}
}

func TestCallbackRetryDelayIsBounded(t *testing.T) {
	service := &CallbackDeliveryService{
		config: CallbackDeliveryConfig{
			RetryBase:    time.Second,
			RetryMaximum: 5 * time.Second,
		},
	}
	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		5 * time.Second,
		5 * time.Second,
	}
	for index, expected := range want {
		if got := service.retryDelay(int64(index + 1)); got != expected {
			t.Fatalf("retryDelay(%d) = %s, want %s", index+1, got, expected)
		}
	}
}
