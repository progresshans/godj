package model

import (
	"fmt"
)

// ErrorCode classifies selected-account management failures without exposing
// target values, credentials or backend diagnostics in their formatted text.
type ErrorCode string

const (
	CodeInvalidInput   ErrorCode = "invalid_input"
	CodeInvalidConfig  ErrorCode = "invalid_config"
	CodePermission     ErrorCode = "permission_denied"
	CodeNotFound       ErrorCode = "not_found"
	CodeConflict       ErrorCode = "revision_conflict"
	CodePersistence    ErrorCode = "persistence_failure"
	CodeOutcomeUnknown ErrorCode = "outcome_unknown"
)

type Error struct {
	Code  ErrorCode
	state *managementFailure
}
type managementFailure struct{ cause error }

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return "identity model: " + string(e.Code)
}
func (e Error) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(e.Error())) }
func (e Error) GoString() string               { return e.Error() }
func (e *Error) Unwrap() error {
	if e == nil || e.state == nil {
		return nil
	}
	return e.state.cause
}
func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	return ok && e != nil && want != nil && (want.Code == "" || e.Code == want.Code)
}
func managementError(code ErrorCode, cause error) error {
	return &Error{Code: code, state: &managementFailure{cause: cause}}
}
