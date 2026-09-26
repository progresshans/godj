package identity

import (
	"errors"
	"fmt"

	"github.com/progresshans/godj/query"
)

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

// Error exposes only framework-owned classification. Cause remains reachable
// through errors.Is/As, behind a pointer that diagnostic fallback cannot expand.
type Error struct {
	Code  ErrorCode `json:"code"`
	Field string    `json:"field,omitempty"`
	state *managementFailure
}

type managementFailure struct{ cause error }

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

func managementError(code ErrorCode, field string, cause error) error {
	return &Error{Code: code, Field: field, state: &managementFailure{cause: cause}}
}

func managementWriteFailure(err error) error {
	// An uncertain rollback/commit takes precedence over a callback's ordinary
	// rejection. A caller must inspect durable state before issuing new work.
	for _, code := range []string{query.CodeCommitOutcomeUnknown, query.CodeTransactionOutcomeUnknown} {
		if errors.Is(err, &query.Error{Code: code}) {
			return managementError(CodeOutcomeUnknown, "transaction", err)
		}
	}
	var classified *Error
	if errors.As(err, &classified) && classified != nil && safeManagementClassification(classified.Code, classified.Field) {
		return managementError(classified.Code, classified.Field, err)
	}
	return managementError(CodePersistence, "transaction", err)
}

func safeManagementClassification(code ErrorCode, field string) bool {
	switch code {
	case CodeInvalidInput, CodeInvalidConfig, CodePermission, CodeNotFound, CodeConflict, CodePersistence, CodeOutcomeUnknown:
	default:
		return false
	}
	switch field {
	case "password_change", "context", "manager", "actor", "user", "password", "password_hasher", "snapshot_contract", "transaction_contract", "transaction":
		return true
	default:
		return false
	}
}
