package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/sessions"
)

// SessionLogin carries an already verified credential and the existing server
// session, never a raw password or client-provided identity. Admit is the same
// login policy checked before persistence; a durable owner invokes it on the
// current principal under its write fence. It must not reenter that owner.
type SessionLogin struct {
	Credential Credential      `json:"-"`
	Previous   sessions.Record `json:"-"`
	At         time.Time
	Admit      func(context.Context, Principal) error `json:"-"`
}

type SessionLoginResult struct {
	Credential Credential      `json:"-"`
	Record     sessions.Record `json:"-"`
}

func (SessionLogin) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.SessionLogin{redacted}"))
}
func (SessionLoginResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.SessionLoginResult{redacted}"))
}

// LoginPersistence owns any coupled identity/session effects. Sessions must
// return the exact manager it was prepared for without I/O. Login publishes
// its result only after confirmed persistence and never automatically retries
// an uncertain outcome. Only a confirmed admission rejection returns the exact
// ErrInvalidCredentials sentinel; cleanup/storage failures remain errors.
type LoginPersistence interface {
	Sessions() *sessions.Manager
	Login(context.Context, SessionLogin) (SessionLoginResult, error)
}

func (login SessionLogin) Validate() error {
	if !login.Credential.Principal().Authenticated() || login.Credential.SessionStamp() == "" || login.Admit == nil {
		return &Error{Code: CodeInvalidInput, Field: "login", Detail: "login context is incomplete"}
	}
	if _, err := temporal.Canonical(login.At); err != nil {
		return &Error{Code: CodeInvalidInput, Field: "login", Detail: "login timestamp is outside the supported range"}
	}
	return nil
}

// EstablishSession binds a verified credential to a session. Anonymous or
// same-credential data survives a fixation-safe rotation. A different principal
// or stale/partial authentication state gets an atomic fresh replacement so
// values belonging to another identity cannot leak into the new login.
// Admission and any durable last-login observation belong to the caller.
func EstablishSession(ctx context.Context, manager *sessions.Manager, credential Credential, previous sessions.Record) (sessions.Record, error) {
	if manager == nil || !credential.Principal().Authenticated() || credential.SessionStamp() == "" {
		return sessions.Record{}, &Error{Code: CodeInvalidInput, Field: "login", Detail: "login context is incomplete"}
	}
	values := map[string]string{SessionPrincipalIDKey: credential.Principal().ID(), SessionCredentialStampKey: credential.SessionStamp()}
	if !previous.ID().Valid() {
		return manager.Create(ctx, values)
	}
	principal, hasPrincipal := previous.Value(SessionPrincipalIDKey)
	stamp, hasStamp := previous.Value(SessionCredentialStampKey)
	if (hasPrincipal || hasStamp) && (principal != credential.Principal().ID() || !credential.MatchesSessionStamp(stamp)) {
		return manager.Replace(ctx, previous, values)
	}
	var err error
	previous, err = previous.WithValue(SessionPrincipalIDKey, credential.Principal().ID())
	if err != nil {
		return sessions.Record{}, err
	}
	previous, err = previous.WithValue(SessionCredentialStampKey, credential.SessionStamp())
	if err != nil {
		return sessions.Record{}, err
	}
	return manager.Rotate(ctx, previous)
}
