package apps

import "fmt"

// ErrorCode identifies an app configuration or lifecycle failure.
type ErrorCode string

const (
	CodeInvalidConfig      ErrorCode = "invalid_config"
	CodeDuplicateName      ErrorCode = "duplicate_name"
	CodeDuplicateLabel     ErrorCode = "duplicate_label"
	CodeDependencyCycle    ErrorCode = "dependency_cycle"
	CodeStartupFailed      ErrorCode = "startup_failed"
	CodeShutdownFailed     ErrorCode = "shutdown_failed"
	CodeShutdownInProgress ErrorCode = "shutdown_in_progress"
	CodeCallbackAborted    ErrorCode = "callback_aborted"
)

// Error reports an app configuration or lifecycle failure. Callback causes
// remain available to errors.Is/As but are excluded from default diagnostics.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	// AppLabel identifies the installed application owning a lifecycle failure.
	AppLabel string
	causes   []error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("apps: %s: %s", e.Code, e.Detail)
	}
	if e.AppLabel != "" {
		return fmt.Sprintf("apps: %s: %s: %s: %s", e.Code, e.Field, e.AppLabel, e.Detail)
	}
	return fmt.Sprintf("apps: %s: %s: %s", e.Code, e.Field, e.Detail)
}

// Format keeps callback causes out of ordinary diagnostics, including %#v.
func (e Error) Format(state fmt.State, _ rune) { fmt.Fprint(state, e.Error()) }

// Unwrap preserves explicit error inspection without exposing causes in text.
func (e *Error) Unwrap() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.causes...)
}

func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	if !ok || e == nil || want == nil {
		return false
	}
	return (want.Code == "" || e.Code == want.Code) &&
		(want.Field == "" || e.Field == want.Field) &&
		(want.AppLabel == "" || e.AppLabel == want.AppLabel)
}
