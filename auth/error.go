// Package auth owns explicit principals, permissions and bounded credential
// verification without coupling authentication state to the Web request type.
package auth

import "fmt"

// ErrorCode identifies a stable, secret-free authentication failure category.
type ErrorCode string

const (
	CodeInvalidConfig ErrorCode = "invalid_config"
	CodeInvalidInput  ErrorCode = "invalid_input"
	CodeInvalidHash   ErrorCode = "invalid_password_hash"
	CodeCredential    ErrorCode = "credential_backend_failure"
	CodeEntropy       ErrorCode = "entropy_failure"
)

// Error omits Cause from its rendered form so injected hashers cannot leak a
// username, password, encoded hash or token through an HTTP diagnostic.
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
		return fmt.Sprintf("auth: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("auth: %s: %s: %s", e.Code, e.Field, e.Detail)
}

// GoString keeps diagnostic %#v formatting on the same framework-owned,
// secret-free surface as Error while Unwrap retains Cause for errors.Is/As.
func (e Error) GoString() string { return (&e).Error() }

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

type invalidCredentialsError struct{}

func (invalidCredentialsError) Error() string { return "auth: invalid credentials" }

// ErrInvalidCredentials is the uniform result for unknown, inactive and
// password-mismatched credentials. It carries no identity-specific detail.
var ErrInvalidCredentials error = invalidCredentialsError{}
