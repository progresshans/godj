package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/sessions"
)

// ErrInvalidResetProof deliberately combines absent/expired/replaced sessions,
// wrong targets and invalidated reset tokens. Wrapped refusals remain execution
// failures and must not be presented as an ordinary invalid link.
var ErrInvalidResetProof = errors.New("auth: invalid password reset proof")

// Reserved server-side values. Neither the target nor token is a client cookie.
const (
	SessionResetPrincipalIDKey = "_godj_reset_principal_id"
	SessionResetTokenKey       = "_godj_reset_token"
)

// PasswordResetPersistence couples a session-bound reset proof to identity
// writes. Sessions must return the exact configured manager without I/O.
// Starting a proof rotates an active prior session or creates an anonymous one;
// it never authenticates the target. Reads do not touch or clean up sessions.
// Completion rechecks the current session/proof and identity under one fence,
// changes password, revokes target sessions, removes this proof and records
// audit atomically. Only confirmed commits publish results; there is no retry
// of an uncertain outcome. Direct cause-free validation errors are input errors.
type PasswordResetPersistence interface {
	Sessions() *sessions.Manager
	StartPasswordReset(context.Context, sessions.ID, string, string) (sessions.Record, error)
	CheckPasswordReset(context.Context, sessions.ID, string, *string) error
	ResetPassword(context.Context, sessions.ID, string, string) (PasswordResetResult, error)
}

// Exactly one outcome is valid: ClearSession with a zero Record, or a rotated
// Record without reset values. Reset never grants a new authentication binding.
type PasswordResetResult struct {
	Record       sessions.Record `json:"-"`
	ClearSession bool
}

func (PasswordResetResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.PasswordResetResult{redacted}"))
}
