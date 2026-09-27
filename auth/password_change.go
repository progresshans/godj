package auth

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/sessions"
)

// PasswordChangePersistence owns self-service password confirmation, current
// credential/session fencing, password policy, session rotation, revocation of
// other sessions and audit as one mutation. It identifies the caller only from
// its own stored session. Sessions returns its exact manager without I/O.
// Only confirmed commits publish results; uncertain outcomes are never retried.
// An expected input refusal must be a direct, cause-free validation rejection;
// wrapped or caused rejections remain execution errors at the Web boundary.
type PasswordChangePersistence interface {
	Sessions() *sessions.Manager
	ChangePassword(context.Context, sessions.ID, string, string) (PasswordChangeResult, error)
}

type PasswordChangeResult struct {
	Credential Credential      `json:"-"`
	Record     sessions.Record `json:"-"`
}

func (PasswordChangeResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.PasswordChangeResult{redacted}"))
}
