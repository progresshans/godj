package web

import "fmt"

// ErrorCode identifies a stable Web Core failure category.
type ErrorCode string

const (
	CodeInvalidConfig       ErrorCode = "invalid_config"
	CodeInvalidRoute        ErrorCode = "invalid_route"
	CodeDuplicateRoute      ErrorCode = "duplicate_route"
	CodeRouteNotFound       ErrorCode = "route_not_found"
	CodeMethodNotAllowed    ErrorCode = "method_not_allowed"
	CodeReverseNotFound     ErrorCode = "reverse_not_found"
	CodeInvalidRequest      ErrorCode = "invalid_request"
	CodeInvalidResponse     ErrorCode = "invalid_response"
	CodeResponseTooLarge    ErrorCode = "response_too_large"
	CodeMiddlewareViolation ErrorCode = "middleware_violation"
	CodeHandlerFailure      ErrorCode = "handler_failure"
	CodeServerState         ErrorCode = "server_state"
)

// Error reports a Web Core configuration or runtime failure. Handler errors
// are logged internally and are never serialized to clients.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Cause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("web: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("web: %s: %s: %s", e.Code, e.Field, e.Detail)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	if !ok || e == nil || want == nil {
		return false
	}
	return (want.Code == "" || e.Code == want.Code) &&
		(want.Field == "" || e.Field == want.Field)
}
