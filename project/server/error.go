package server

import "fmt"

type Code string

const (
	CodeInvalidConfig       Code = "invalid_config"
	CodeStartFailed         Code = "start_failed"
	CodeListenFailed        Code = "listen_failed"
	CodeReadyFailed         Code = "ready_failed"
	CodeServeFailed         Code = "serve_failed"
	CodeStopFailed          Code = "stop_failed"
	CodeListenerCloseFailed Code = "listener_close_failed"
	CodeCallbackAborted     Code = "callback_aborted"
)

// Error exposes a closed startup/cleanup stage. Explicit errors.Is/As may
// inspect its cause; ordinary formatting and JSON omit callback error text,
// listener paths, settings and panic values.
type Error struct {
	Code  Code `json:"code"`
	cause error
}

func (err *Error) Error() string {
	if err == nil {
		return "project server: invalid error"
	}
	return "project server: " + string(err.Code)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func (err *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && err != nil && other != nil && other.Code == err.Code
}

func (err Error) Format(state fmt.State, _ rune) { fmt.Fprint(state, (&err).Error()) }

func failure(code Code, cause error) error { return &Error{Code: code, cause: cause} }
