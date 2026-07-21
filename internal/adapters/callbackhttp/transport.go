package callbackhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	signing "github.com/deepnoodle-ai/nvoken/internal/signing/v1"
)

const (
	ContentType      = "application/vnd.nvoken.tool-callback+json; version=1"
	MaxResponseBytes = 256 << 10
)

type Config struct {
	SigningKey     []byte
	SigningKeyID   string
	SigningVersion int64
	RequestTimeout time.Duration
	Client         *http.Client
	Now            func() time.Time
}

type Transport struct {
	key            []byte
	keyID          string
	keyVersion     int64
	requestTimeout time.Duration
	client         *http.Client
	now            func() time.Time
}

func New(config Config) (*Transport, error) {
	if len(config.SigningKey) < 32 {
		return nil, fmt.Errorf("callback signing key must be at least 32 bytes")
	}
	if strings.TrimSpace(config.SigningKeyID) == "" || len(config.SigningKeyID) > 255 ||
		strings.ContainsAny(config.SigningKeyID, "\r\n") {
		return nil, fmt.Errorf("callback signing key ID must be nonblank and at most 255 bytes")
	}
	if config.SigningVersion <= 0 {
		return nil, fmt.Errorf("callback signing key version must be positive")
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 10 * time.Second
	}
	if config.Client == nil {
		return nil, fmt.Errorf("callback HTTP client is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Transport{
		key:            append([]byte(nil), config.SigningKey...),
		keyID:          config.SigningKeyID,
		keyVersion:     config.SigningVersion,
		requestTimeout: config.RequestTimeout,
		client:         config.Client,
		now:            config.Now,
	}, nil
}

func (t *Transport) Send(ctx context.Context, request ports.CallbackTransportRequest) ports.CallbackTransportResult {
	endpoint, err := url.Parse(request.EndpointURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" ||
		endpoint.User != nil || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return transportFailure("request_invalid", false)
	}
	requestCtx, cancel := context.WithTimeout(ctx, t.requestTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		request.EndpointURL,
		bytes.NewReader(request.Body),
	)
	if err != nil {
		return transportFailure("request_invalid", false)
	}
	timestamp := t.now().UTC().Unix()
	httpRequest.Header.Set("Content-Type", ContentType)
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "nvoken-callback-delivery/1")
	signing.ApplyHeaders(httpRequest.Header, signing.HeaderParams{
		Signature:  signing.Sign(t.key, request.Body, request.DeliveryID, timestamp),
		Timestamp:  timestamp,
		DeliveryID: request.DeliveryID,
		ToolCallID: request.ToolCallID,
		KeyID:      t.keyID,
		KeyVersion: t.keyVersion,
	})
	response, err := t.client.Do(httpRequest)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				return ports.CallbackTransportResult{
					StatusCode: response.StatusCode,
					ErrorCode:  "redirect",
				}
			}
		}
		return transportFailure("transport_error", true)
	}
	defer func() { _ = response.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if readErr != nil {
		return transportFailure("response_read", true)
	}
	if len(body) > MaxResponseBytes {
		return ports.CallbackTransportResult{
			StatusCode: response.StatusCode,
			ErrorCode:  "response_too_large",
		}
	}
	result := ports.CallbackTransportResult{
		StatusCode:  response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        body,
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return result
	}
	result.ErrorCode = fmt.Sprintf("http_%d", response.StatusCode)
	result.Retryable = response.StatusCode == http.StatusRequestTimeout ||
		response.StatusCode == http.StatusTooEarly ||
		response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode >= 500
	result.Body = nil
	return result
}

func transportFailure(code string, retryable bool) ports.CallbackTransportResult {
	return ports.CallbackTransportResult{
		ErrorCode: code,
		Retryable: retryable,
	}
}
