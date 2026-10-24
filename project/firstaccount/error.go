package firstaccount

import "fmt"

type Code string

const (
	InvalidConfig Code = "invalid_config"
	InvalidInput  Code = "invalid_input"
	OpenFailed    Code = "open_failed"
	Consumed      Code = "input_consumed"
)

// Error formats only the closed code. Causes remain available to the trusted
// process owner for classification, never for command output or diagnostics.
type Error struct {
	Code  Code
	cause error
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return "firstaccount: " + string(err.Code)
}
func (err Error) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(err.Error())) }
func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}
func (err *Error) Is(target error) bool {
	want, ok := target.(*Error)
	return ok && err != nil && want != nil && (want.Code == "" || err.Code == want.Code)
}
func failure(code Code, cause error) error { return &Error{Code: code, cause: cause} }
