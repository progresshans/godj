// Package api provides GoDj's bounded JSON HTTP representation primitives.
// It deliberately owns no model persistence or implicit transaction policy.
package api

import "fmt"

// FailureCode identifies a non-wire API construction or request failure.
type FailureCode string

const (
	FailureInvalidConfig    FailureCode = "invalid_config"
	FailureInvalidRequest   FailureCode = "invalid_request"
	FailureUnsupportedMedia FailureCode = "unsupported_media_type"
	FailureNotAcceptable    FailureCode = "not_acceptable"
	FailureBodyTooLarge     FailureCode = "body_too_large"
	FailureBodyRead         FailureCode = "body_read_failure"
	FailureInvalidResponse  FailureCode = "invalid_response"
)

// Error is secret-free at its outer layer. Cause is retained for diagnostic
// ownership but must never be serialized to an API client.
type Error struct {
	Code   FailureCode
	Field  string
	Detail string
	Cause  error `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "api: <nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("api: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("api: %s: %s: %s", e.Code, e.Field, e.Detail)
}

// GoString keeps diagnostic %#v formatting on the same framework-owned,
// secret-free surface as Error while Unwrap retains Cause for errors.Is/As.
func (e Error) GoString() string { return (&e).Error() }

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

// ResponseCode is a stable machine-readable JSON response category.
type ResponseCode string

const (
	CodeParseError       ResponseCode = "parse_error"
	CodeValidationError  ResponseCode = "validation_error"
	CodeNotAuthenticated ResponseCode = "not_authenticated"
	CodePermissionDenied ResponseCode = "permission_denied"
	CodeCSRFRejected     ResponseCode = "csrf_rejected"
	CodeNotFound         ResponseCode = "not_found"
	CodeMethodNotAllowed ResponseCode = "method_not_allowed"
	CodeUnsupportedMedia ResponseCode = "unsupported_media_type"
	CodeNotAcceptable    ResponseCode = "not_acceptable"
	CodeRequestTooLarge  ResponseCode = "request_too_large"
)
