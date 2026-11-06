// Package bearerauth adapts an injected opaque Bearer verifier to GoDj's JSON
// API authentication contract. It owns credential transport and challenge
// policy, not token issuance, persistence, JWT, or OAuth behavior.
package bearerauth

import (
	"fmt"
	"io"
)

// ErrorCode identifies one stable, secret-free Bearer adapter failure.
type ErrorCode string

const (
	CodeInvalidConfig  ErrorCode = "invalid_config"
	CodeInvalidRequest ErrorCode = "invalid_request"
	CodeVerification   ErrorCode = "verification_failure"
	CodeAuthorization  ErrorCode = "authorization_failure"
	CodeResponse       ErrorCode = "response_failure"
)

// Error retains an injected cause for errors.Is/As while exposing only
// framework-owned fields in ordinary formatting and JSON.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Cause  error `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "api/bearerauth: <nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("api/bearerauth: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("api/bearerauth: %s: %s: %s", e.Code, e.Field, e.Detail)
}

// GoString keeps diagnostic %#v formatting on the same secret-free surface.
func (e Error) GoString() string { return (&e).Error() }

// Format prevents unusual fmt verbs from falling back to structural Cause
// formatting while retaining the same stable Error text.
func (e Error) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, (&e).Error())
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
