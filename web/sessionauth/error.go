// Package sessionauth adapts explicit sessions and authentication to GoDj's
// borrowed synchronous Web request without adding auth state to lower web.
package sessionauth

import "fmt"

type ErrorCode string

const (
	CodeInvalidConfig  ErrorCode = "invalid_config"
	CodeInvalidRequest ErrorCode = "invalid_request"
	CodeSession        ErrorCode = "session_failure"
	CodeAuthentication ErrorCode = "authentication_failure"
	CodeAuthorization  ErrorCode = "authorization_failure"
	CodeCSRFRejected   ErrorCode = "csrf_rejected"
	CodeEntropy        ErrorCode = "entropy_failure"
	CodeResponse       ErrorCode = "response_failure"
)

// Error is deliberately secret-free. Cause is available to errors.Is/As but
// is not rendered, preventing cookie, credential, token and redirect input
// from appearing in a default HTTP diagnostic.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Cause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("sessionauth: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("sessionauth: %s: %s: %s", e.Code, e.Field, e.Detail)
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
