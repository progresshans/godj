// Package serializers provides reflection-free, database-independent JSON
// values and ordered validation for explicit application adapters.
package serializers

import "fmt"

// ErrorCode identifies a stable serializer construction or document failure.
type ErrorCode string

const (
	CodeInvalidConfig   ErrorCode = "invalid_config"
	CodeInvalidDocument ErrorCode = "invalid_document"
	CodeInvalidValue    ErrorCode = "invalid_value"
	CodeResourceLimit   ErrorCode = "resource_limit"
)

// Error reports a secret-free serializer failure. Callers should render its
// Code and Field rather than exposing Detail or Cause to an HTTP client.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Cause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "serializers: <nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("serializers: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("serializers: %s: %s: %s", e.Code, e.Field, e.Detail)
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
