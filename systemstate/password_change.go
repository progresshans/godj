package systemstate

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

type identityPasswordChangePersistence struct {
	runtime *Runtime
	manager *sessions.Manager
	changer *identity.PasswordChanger
}

func (*identityPasswordChangePersistence) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.PasswordChangePersistence{redacted}"))
}

// PasswordChangePersistence prepares self-service changes for this native
// identity runtime and exact session manager. Policy is explicit and should be
// shared with the host's administrative password policy. Preparation performs
// no I/O or password work; legacy operator runtimes do not provide this feature.
func (runtime *Runtime) PasswordChangePersistence(manager *sessions.Manager, validators ...identity.PasswordValidator) (auth.PasswordChangePersistence, error) {
	if runtime == nil || runtime.passwordConfirmer == nil || runtime.passwordHasher == nil || runtime.sessionStore == nil || manager == nil || manager.Store() != runtime.sessionStore {
		return nil, &Error{Code: CodeInvalidConfig, Field: "password_change", Detail: "password change requires this identity runtime's session manager"}
	}
	changer, err := identity.NewPasswordChanger(runtime, runtime.passwordConfirmer, runtime.passwordHasher, validators...)
	if err != nil {
		return nil, err
	}
	return &identityPasswordChangePersistence{runtime, manager, changer}, nil
}

func (p *identityPasswordChangePersistence) Sessions() *sessions.Manager {
	if p == nil {
		return nil
	}
	return p.manager
}

func (p *identityPasswordChangePersistence) ChangePassword(ctx context.Context, id sessions.ID, oldPassword, newPassword string) (auth.PasswordChangeResult, error) {
	if p == nil || p.runtime == nil || p.manager == nil || p.changer == nil {
		return auth.PasswordChangeResult{}, &Error{Code: CodeInvalidConfig, Field: "password_change", Detail: "password change persistence is uninitialized"}
	}
	if err := p.runtime.validBackendCall(ctx); err != nil {
		return auth.PasswordChangeResult{}, err
	}
	if !id.Valid() {
		return auth.PasswordChangeResult{}, auth.ErrInvalidCredentials
	}
	previous, found, err := p.manager.Peek(ctx, id)
	if err != nil {
		return auth.PasswordChangeResult{}, passwordChangeFailure(err)
	}
	if !found {
		return auth.PasswordChangeResult{}, auth.ErrInvalidCredentials
	}
	change, err := p.changer.Prepare(ctx, previous, oldPassword, newPassword)
	if err != nil {
		if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil || err == auth.ErrInvalidCredentials {
			return auth.PasswordChangeResult{}, err
		}
		return auth.PasswordChangeResult{}, passwordChangeFailure(err)
	}
	store := &passwordChangeSessionStore{durableSessionStore: p.runtime.sessionStore, runtime: p.runtime, changer: p.changer, change: change}
	manager, err := p.manager.WithStore(store)
	if err != nil {
		return auth.PasswordChangeResult{}, err
	}
	// Manager owns entropy and time before the transaction. The scoped store
	// derives payload and credential binding from the authoritative stored row.
	record, err := manager.Rotate(ctx, previous)
	if err != nil {
		if store.rejection != nil {
			return auth.PasswordChangeResult{}, store.rejection
		}
		return auth.PasswordChangeResult{}, passwordChangeFailure(err)
	}
	idValue, _ := record.Value(auth.SessionPrincipalIDKey)
	stamp, _ := record.Value(auth.SessionCredentialStampKey)
	if !store.current.Principal().Authenticated() || store.current.Principal().ID() != idValue || !store.current.MatchesSessionStamp(stamp) {
		return auth.PasswordChangeResult{}, passwordChangeFailure(nil)
	}
	return auth.PasswordChangeResult{Credential: store.current, Record: record}, nil
}

type passwordChangeSessionStore struct {
	*durableSessionStore
	runtime   *Runtime
	changer   *identity.PasswordChanger
	change    identity.PreparedPasswordChange
	current   auth.Credential
	rejection error
}

// A failed password change cannot publish a separate expiry cleanup.
func (store *passwordChangeSessionStore) Delete(context.Context, sessions.ID) error {
	store.rejection = auth.ErrInvalidCredentials
	return auth.ErrInvalidCredentials
}

func (store *passwordChangeSessionStore) Rotate(ctx context.Context, oldID sessions.ID, replacement sessions.Record) (sessions.Record, bool, error) {
	var record sessions.Record
	var credential auth.Credential
	var callbackErr, mutationErr error
	calls := 0
	err := store.runtime.withAtomic(ctx, func(session db.Session) error {
		calls++
		if calls != 1 || isNilInterface(session) {
			callbackErr = passwordChangeFailure(nil)
			return callbackErr
		}
		callbackErr = func() error {
			digest, err := sessionDigest(oldID)
			if err != nil {
				return err
			}
			row, present, err := loadSessionRow(ctx, session, digest)
			if err != nil {
				return err
			}
			if !present {
				return auth.ErrInvalidCredentials
			}
			current, err := decodeSessionPayload(row.payload, oldID, store.limits)
			if err != nil {
				return err
			}
			bound, err := store.change.BindSession(current)
			if err != nil {
				return err
			}
			replacement, err = sessions.RestoreRecord(sessions.RecordSnapshot{
				ID: replacement.ID(), Values: bound.Values(), CreatedAt: current.CreatedAt(),
				AccessedAt: replacement.AccessedAt(), AbsoluteExpiresAt: current.AbsoluteExpiresAt(), IdleExpiresAt: replacement.IdleExpiresAt(),
			}, store.limits)
			if err != nil {
				return err
			}
			gate := &borrowedLoginGate{session: session}
			scoped := *store.durableSessionStore
			scoped.gate = gate
			var rotated bool
			record, rotated, mutationErr = scoped.Rotate(ctx, oldID, replacement)
			if mutationErr != nil {
				return mutationErr
			}
			if !gate.called {
				return passwordChangeFailure(nil)
			}
			if !rotated {
				return auth.ErrInvalidCredentials
			}
			credential, err = store.changer.ApplyIn(ctx, session, store.change)
			if err != nil {
				return err
			}
			keep, err := sessionDigest(record.ID())
			if err != nil {
				return err
			}
			if _, err := store.runtime.revokePrincipalSessions(ctx, session, credential.Principal().ID(), keep); err != nil {
				return err
			}
			return ctx.Err()
		}()
		return callbackErr
	})
	if calls == 1 && err != nil {
		if err == auth.ErrInvalidCredentials && callbackErr == auth.ErrInvalidCredentials {
			store.rejection = auth.ErrInvalidCredentials
			return sessions.Record{}, false, err
		}
		if diagnostics, rejected := validation.Rejected(callbackErr); rejected && err == callbackErr && errors.Unwrap(callbackErr) == nil {
			store.rejection = validation.Reject(diagnostics, nil)
			return sessions.Record{}, false, store.rejection
		}
		if typed, ok := mutationErr.(*sessions.Error); ok && typed != nil && typed.Cause == nil && typed.Code == sessions.CodeEntropy && err == typed && callbackErr == typed {
			return sessions.Record{}, false, typed
		}
	}
	if err != nil || callbackErr != nil || calls != 1 {
		return sessions.Record{}, false, passwordChangeFailure(errors.Join(err, callbackErr))
	}
	store.current = credential
	return record, true, nil
}

func passwordChangeFailure(err error) error {
	if code := operatorOutcomeUnknownCode(err); code != "" {
		return &Error{Code: CodePersistence, Field: "password_change", Detail: "password change outcome is unknown; reconciliation is required", Cause: &query.Error{Category: query.CategoryBackend, Code: code}}
	}
	if safeOperatorErrorIs(err, context.Canceled) {
		return context.Canceled
	}
	if safeOperatorErrorIs(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return &Error{Code: CodePersistence, Field: "password_change", Detail: "password change transaction failed"}
}
