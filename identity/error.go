package identity

import (
	"fmt"
)

type ErrorCode string

const (
	CodeInvalidInput   ErrorCode = "invalid_input"
	CodeInvalidConfig  ErrorCode = "invalid_config"
	CodePersistence    ErrorCode = "persistence_failure"
	CodeDelivery       ErrorCode = "delivery_failure"
	CodeOutcomeUnknown ErrorCode = "outcome_unknown"
)

// Error exposes only framework-owned classification. Cause remains reachable
// through errors.Is/As, behind a pointer that diagnostic fallback cannot expand.
type Error struct {
	Code  ErrorCode `json:"code"`
	Field string    `json:"field,omitempty"`
	state *identityErrorState
}

type identityErrorState struct{ cause error }

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return "identity: " + string(e.Code) + ": " + e.Field
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
	return ok && e != nil && want != nil && (want.Code == "" || e.Code == want.Code) && (want.Field == "" || e.Field == want.Field)
}

func identityFailure(code ErrorCode, field string, cause error) error {
	return &Error{Code: code, Field: field, state: &identityErrorState{cause: cause}}
}
