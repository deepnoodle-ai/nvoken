package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type ErrorCategory string

const (
	ErrorAuthentication     ErrorCategory = "authentication"
	ErrorValidation         ErrorCategory = "validation"
	ErrorNotFound           ErrorCategory = "not_found"
	ErrorConflict           ErrorCategory = "conflict"
	ErrorRateLimit          ErrorCategory = "rate_limit"
	ErrorServer             ErrorCategory = "server"
	ErrorTransport          ErrorCategory = "transport"
	ErrorCancelled          ErrorCategory = "cancelled"
	ErrorTimeout            ErrorCategory = "timeout"
	ErrorUnexpectedResponse ErrorCategory = "unexpected_response"
)

type Error struct {
	Category   ErrorCategory
	Status     int
	Code       string
	Message    string
	RequestID  string
	RetryAfter time.Duration
	Details    map[string]any
	Cause      error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Category)
}

func (e *Error) Unwrap() error { return e.Cause }

func errorFromResponse(status int, header http.Header, body []byte) error {
	payload := struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		RequestID string         `json:"request_id"`
		Details   map[string]any `json:"details"`
	}{}
	_ = json.Unmarshal(body, &payload)
	requestID := payload.RequestID
	if requestID == "" {
		requestID = header.Get("X-Request-Id")
	}
	category := ErrorUnexpectedResponse
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		category = ErrorAuthentication
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		category = ErrorValidation
	case status == http.StatusNotFound:
		category = ErrorNotFound
	case status == http.StatusConflict:
		category = ErrorConflict
	case status == http.StatusTooManyRequests:
		category = ErrorRateLimit
	case status >= 500:
		category = ErrorServer
	}
	message := payload.Message
	if message == "" {
		message = fmt.Sprintf("nvoken returned HTTP %d", status)
	}
	return &Error{
		Category:   category,
		Status:     status,
		Code:       payload.Code,
		Message:    message,
		RequestID:  requestID,
		RetryAfter: parseRetryAfter(header.Get("Retry-After"), time.Now()),
		Details:    payload.Details,
	}
}

func transportError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Category: ErrorTimeout, Message: "local wait or request timed out", Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Category: ErrorCancelled, Message: "local wait or request was cancelled", Cause: err}
	}
	return &Error{Category: ErrorTransport, Message: "nvoken transport failed", Cause: err}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}
