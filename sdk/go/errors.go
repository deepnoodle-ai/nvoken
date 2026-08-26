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
	ErrorPermission         ErrorCategory = "permission"
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

// TurnErasedError reports that a Turn's retained identity and
// lifecycle facts still exist, but its private content has been erased.
type TurnErasedError struct {
	Base     *Error
	TurnID   string
	ErasedAt *time.Time
}

func (e *TurnErasedError) Error() string { return e.Base.Error() }

func (e *TurnErasedError) Unwrap() error { return e.Base }

// TurnAdmissionError means a transport outcome was uncertain after the SDK
// had fixed the idempotency key. Repeating the exact admission with that key
// recovers the same Turn if the service accepted it.
type TurnAdmissionError struct {
	Base           *Error
	IdempotencyKey string
}

func (e *TurnAdmissionError) Error() string { return e.Base.Error() }

func (e *TurnAdmissionError) Unwrap() error { return e.Base }

// TurnTimeoutError is a local timeout, not a request to cancel durable work.
// Turn is present when admission completed; IdempotencyKey remains available
// even when admission itself had an uncertain timeout.
type TurnTimeoutError struct {
	Base           *Error
	Turn           *Turn
	IdempotencyKey string
}

func (e *TurnTimeoutError) Error() string { return e.Base.Error() }

func (e *TurnTimeoutError) Unwrap() error { return e.Base }

// TurnExecutionError reports a terminal failed or cancelled Turn while
// retaining its complete result snapshot for diagnostics and recovery.
type TurnExecutionError struct {
	Result *TurnResult
}

func (e *TurnExecutionError) Error() string {
	if e.Result == nil {
		return "Turn ended unsuccessfully"
	}
	message := fmt.Sprintf("Turn %s ended with status %s", e.Result.Resource.ID, e.Result.Resource.Status)
	if e.Result.Resource.Error != nil && e.Result.Resource.Error.Message != "" {
		message += ": " + e.Result.Resource.Error.Message
	}
	return message
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

// IsNotFound reports whether an SDK operation failed because the requested
// resource was not found or was outside the client's asserted scope.
func IsNotFound(err error) bool {
	var nvokenError *Error
	return errors.As(err, &nvokenError) && nvokenError.Category == ErrorNotFound
}

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
	case status == http.StatusUnauthorized:
		category = ErrorAuthentication
	case status == http.StatusForbidden:
		category = ErrorPermission
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		category = ErrorValidation
	case status == http.StatusNotFound || status == http.StatusGone:
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
	nvokenError := &Error{
		Category:   category,
		Status:     status,
		Code:       payload.Code,
		Message:    message,
		RequestID:  requestID,
		RetryAfter: parseRetryAfter(header.Get("Retry-After"), time.Now()),
		Details:    payload.Details,
	}
	if status == http.StatusGone && payload.Code == "turn_erased" {
		erased := &TurnErasedError{Base: nvokenError}
		if turnID, ok := payload.Details["turn_id"].(string); ok {
			erased.TurnID = turnID
		}
		if erasedAt, ok := payload.Details["erased_at"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, erasedAt); err == nil {
				erased.ErasedAt = &parsed
			}
		}
		return erased
	}
	return nvokenError
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

func turnAdmissionError(err error, idempotencyKey string) error {
	var sdkError *Error
	if !errors.As(err, &sdkError) {
		return err
	}
	switch sdkError.Category {
	case ErrorTimeout:
		return &TurnTimeoutError{Base: sdkError, IdempotencyKey: idempotencyKey}
	case ErrorTransport, ErrorCancelled:
		return &TurnAdmissionError{Base: sdkError, IdempotencyKey: idempotencyKey}
	default:
		return err
	}
}

func turnWaitError(err error, turn *Turn) error {
	var sdkError *Error
	if !errors.As(err, &sdkError) || sdkError.Category != ErrorTimeout {
		return err
	}
	return &TurnTimeoutError{
		Base:           sdkError,
		Turn:           turn,
		IdempotencyKey: turn.idempotencyKey,
	}
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
