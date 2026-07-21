package callbackhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	signing "github.com/deepnoodle-ai/nvoken/internal/signing/v1"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestTransportSignsStableIdentitiesAndFreshTimestamp(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"nvoken":{"schema_version":1},"input":{"value":1}}`)
	deliveryID := "cbdy_018f0000-0000-7000-8000-000000000001"
	toolCallID := "tcal_018f0000-0000-7000-8000-000000000002"
	now := time.Unix(1750000000, 0).UTC()
	requests := 0
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			readBody, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			if string(readBody) != string(body) {
				t.Fatalf("request body = %s", readBody)
			}
			if request.Header.Get(signing.DeliveryIDHeader) != deliveryID ||
				request.Header.Get(signing.IdempotencyKeyHeader) != toolCallID ||
				request.Header.Get(signing.SigningKeyIDHeader) != "installation/key" ||
				request.Header.Get(signing.SigningKeyVersionHeader) != "7" {
				t.Fatalf("identity headers = %#v", request.Header)
			}
			if err := signing.Verify(
				key,
				readBody,
				deliveryID,
				now.Unix(),
				request.Header.Get(signing.SignatureHeader),
			); err != nil {
				t.Fatalf("verify signature: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(`{"content":{"ok":true}}`)),
			}, nil
		}),
	}
	transport, err := New(Config{
		SigningKey:     key,
		SigningKeyID:   "installation/key",
		SigningVersion: 7,
		RequestTimeout: time.Second,
		Client:         client,
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result := transport.Send(context.Background(), ports.CallbackTransportRequest{
		EndpointURL: "https://callbacks.example.test/tool",
		DeliveryID:  deliveryID,
		ToolCallID:  toolCallID,
		Body:        body,
	})
	if result.ErrorCode != "" || result.StatusCode != http.StatusOK || requests != 1 {
		t.Fatalf("Send() = %#v, requests = %d", result, requests)
	}
	// A retry keeps the durable identities/body while generating a new timestamp.
	now = now.Add(time.Second)
	result = transport.Send(context.Background(), ports.CallbackTransportRequest{
		EndpointURL: "https://callbacks.example.test/tool",
		DeliveryID:  deliveryID,
		ToolCallID:  toolCallID,
		Body:        body,
	})
	if result.ErrorCode != "" || requests != 2 {
		t.Fatalf("retry Send() = %#v, requests = %d", result, requests)
	}
}

func TestTransportClassifiesResponsesAndBoundsBody(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		retryable bool
	}{
		{name: "bad request", status: http.StatusBadRequest},
		{name: "timeout", status: http.StatusRequestTimeout, retryable: true},
		{name: "too early", status: http.StatusTooEarly, retryable: true},
		{name: "rate limited", status: http.StatusTooManyRequests, retryable: true},
		{name: "server error", status: http.StatusBadGateway, retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := testTransport(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("ignored")),
				}, nil
			}))
			result := transport.Send(context.Background(), callbackTransportRequest())
			if result.ErrorCode == "" || result.Retryable != test.retryable || result.Body != nil {
				t.Fatalf("Send() = %#v", result)
			}
		})
	}

	transport := testTransport(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			Body: io.NopCloser(strings.NewReader(strings.Repeat("x", MaxResponseBytes+1))),
		}, nil
	}))
	result := transport.Send(context.Background(), callbackTransportRequest())
	if result.ErrorCode != "response_too_large" || result.Retryable {
		t.Fatalf("oversized Send() = %#v", result)
	}
}

func TestTransportRejectsUnsafeEndpointBeforeHTTP(t *testing.T) {
	requests := 0
	transport := testTransport(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, nil
	}))
	for _, endpoint := range []string{
		"http://callbacks.example.test/tool",
		"https://secret@callbacks.example.test/tool",
		"https://callbacks.example.test/tool#fragment",
	} {
		request := callbackTransportRequest()
		request.EndpointURL = endpoint
		result := transport.Send(context.Background(), request)
		if result.ErrorCode != "request_invalid" || result.Retryable {
			t.Fatalf("Send(%q) = %#v", endpoint, result)
		}
	}
	if requests != 0 {
		t.Fatalf("unsafe endpoints made %d HTTP requests", requests)
	}
}

func testTransport(t *testing.T, roundTripper http.RoundTripper) *Transport {
	t.Helper()
	transport, err := New(Config{
		SigningKey:     []byte("0123456789abcdef0123456789abcdef"),
		SigningKeyID:   "installation/key",
		SigningVersion: 1,
		RequestTimeout: time.Second,
		Client: &http.Client{
			Transport: roundTripper,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return transport
}

func callbackTransportRequest() ports.CallbackTransportRequest {
	return ports.CallbackTransportRequest{
		EndpointURL: "https://callbacks.example.test/tool",
		DeliveryID:  "cbdy_018f0000-0000-7000-8000-000000000001",
		ToolCallID:  "tcal_018f0000-0000-7000-8000-000000000002",
		Body:        []byte(`{"input":{}}`),
	}
}
