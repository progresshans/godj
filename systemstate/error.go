package systemstate

import "fmt"

// ErrorCode identifies one stable system-state failure category. Error text is
// deliberately assembled only from framework-owned metadata; stored payloads,
// password material, session bearers, and database URLs must stay in Cause and
// are never rendered.
type ErrorCode string

const (
	CodeInvalidConfig      ErrorCode = "invalid_config"
	CodeInvalidInput       ErrorCode = "invalid_input"
	CodeSchemaUnavailable  ErrorCode = "schema_unavailable"
	CodeCardinality        ErrorCode = "invalid_cardinality"
	CodeCorruptState       ErrorCode = "corrupt_state"
	CodePersistence        ErrorCode = "persistence_failure"
	CodeCredentialMismatch ErrorCode = "credential_mismatch"
)

// Error reports a secret-free system-state failure.
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
		return fmt.Sprintf("systemstate: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("systemstate: %s: %s: %s", e.Code, e.Field, e.Detail)
}

// GoString keeps diagnostic %#v formatting on the same framework-owned,
// secret-free surface as Error. Cause remains available through errors.Is/As
// but is never recursively formatted by fmt.
func (e *Error) GoString() string { return e.Error() }

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
