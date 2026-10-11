package mail

import "fmt"

type ErrorCode string

const (
	CodeInvalidInput   ErrorCode = "invalid_input"
	CodeInvalidConfig  ErrorCode = "invalid_config"
	CodeUnsupported    ErrorCode = "unsupported"
	CodeNotSent        ErrorCode = "not_sent"
	CodeRejected       ErrorCode = "rejected"
	CodeOutcomeUnknown ErrorCode = "outcome_unknown"
	CodeCapacity       ErrorCode = "capacity"
)

// Error exposes only a stable stage and outcome. SMTP replies and message or
// authentication material remain in the private cause chain for explicit use.
type Error struct {
	Code  ErrorCode
	Stage string
	state *errorState
}
type errorState struct{ cause error }

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return "mail: " + string(e.Code) + ": " + e.Stage
}
func (e Error) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte(e.Error())) }
func (e *Error) Unwrap() error {
	if e == nil || e.state == nil {
		return nil
	}
	return e.state.cause
}
func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	return ok && e != nil && want != nil && (want.Code == "" || e.Code == want.Code) && (want.Stage == "" || e.Stage == want.Stage)
}
func failure(code ErrorCode, stage string, cause error) error {
	return &Error{code, stage, &errorState{cause}}
}
