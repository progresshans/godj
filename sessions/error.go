// Package sessions owns bounded, process-local server-side session state.
package sessions

import "fmt"

// ErrorCode identifies a stable session failure category.
type ErrorCode string

const (
	CodeInvalidConfig ErrorCode = "invalid_config"
	CodeInvalidInput  ErrorCode = "invalid_input"
	CodeInvalidRecord ErrorCode = "invalid_record"
	CodeStoreFailure  ErrorCode = "store_failure"
	CodeStoreFull     ErrorCode = "store_full"
	CodeNotFound      ErrorCode = "not_found"
	CodeEntropy       ErrorCode = "entropy_failure"
)

// Error reports a secret-free session failure. Error deliberately omits Cause
// from its rendered text so an external store cannot accidentally add a
// session identifier or value to an HTTP diagnostic.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Cause  error `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("sessions: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("sessions: %s: %s: %s", e.Code, e.Field, e.Detail)
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
