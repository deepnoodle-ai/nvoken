package services

import "fmt"

type ErrorCode string

const (
	CodeInvalidRequest          ErrorCode = "invalid_request"
	CodeForbidden               ErrorCode = "forbidden"
	CodeNotFound                ErrorCode = "not_found"
	CodeIdempotencyConflict     ErrorCode = "idempotency_conflict"
	CodeSessionInvocationActive ErrorCode = "session_invocation_active"
	CodeInvocationNotWaiting    ErrorCode = "invocation_not_waiting"
	CodeToolResultConflict      ErrorCode = "tool_result_conflict"
	CodeToolResultExpired       ErrorCode = "tool_result_expired"
	CodeInternal                ErrorCode = "internal"
	CodeUnavailable             ErrorCode = "unavailable"
)

type PublicError struct {
	Code    ErrorCode
	Message string
	Details map[string]any
	Cause   error
}

func (e *PublicError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *PublicError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func invalidRequest(message string) error {
	return &PublicError{Code: CodeInvalidRequest, Message: message}
}

func forbidden(message string) error {
	return &PublicError{Code: CodeForbidden, Message: message}
}

func notFound() error {
	return &PublicError{Code: CodeNotFound, Message: "The requested resource was not found."}
}
