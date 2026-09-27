package systemstate

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

// PasswordResetPersistence owns one resetter and its exact durable session
// domain. Copies of this pointer share immutable configuration, not per-request
// state. Use Resetter to bind the mail requester to the same token/policy owner.
type PasswordResetPersistence struct {
	runtime  *Runtime
	manager  *sessions.Manager
	resetter *identity.PasswordResetter
}

var _ auth.PasswordResetPersistence = (*PasswordResetPersistence)(nil)

func (*PasswordResetPersistence) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.PasswordResetPersistence{redacted}"))
}

// PasswordResetPersistence performs no I/O and requires this identity runtime's
// exact session store. Legacy operator authentication cannot provide reset.
func (runtime *Runtime) PasswordResetPersistence(manager *sessions.Manager, config identity.PasswordResetConfig) (*PasswordResetPersistence, error) {
	if runtime == nil || runtime.passwordHasher == nil || runtime.sessionStore == nil || manager == nil || manager.Store() != runtime.sessionStore {
		return nil, &Error{Code: CodeInvalidConfig, Field: "password_reset", Detail: "password reset requires this identity runtime's session manager"}
	}
	resetter, err := identity.NewPasswordResetter(runtime, runtime.passwordHasher, config)
	if err != nil {
		return nil, err
	}
	return &PasswordResetPersistence{runtime, manager, resetter}, nil
}

func (p *PasswordResetPersistence) Sessions() *sessions.Manager {
	if p == nil {
		return nil
	}
	return p.manager
}

func (p *PasswordResetPersistence) Resetter() *identity.PasswordResetter {
	if p == nil {
		return nil
	}
	return p.resetter
}

func (p *PasswordResetPersistence) validCall(ctx context.Context) error {
	if p == nil || p.runtime == nil || p.manager == nil || p.resetter == nil {
		return &Error{Code: CodeInvalidConfig, Field: "password_reset", Detail: "password reset persistence is uninitialized"}
	}
	return p.runtime.validBackendCall(ctx)
}

func (p *PasswordResetPersistence) StartPasswordReset(ctx context.Context, previousID sessions.ID, principalID, token string) (sessions.Record, error) {
	if err := p.validCall(ctx); err != nil {
		return sessions.Record{}, err
	}
	if err := p.resetter.CheckPassword(ctx, principalID, token, nil); err != nil {
		return sessions.Record{}, resetAdmissionFailure(err)
	}
	var previous sessions.Record
	var found bool
	var err error
	if previousID.Valid() {
		previous, found, err = p.manager.Peek(ctx, previousID)
		if err != nil {
			return sessions.Record{}, passwordResetFailure(err)
		}
	}
	store := &resetSessionStore{durableSessionStore: p.runtime.sessionStore, owner: p, principalID: principalID, token: token}
	manager, err := p.manager.WithStore(store)
	if err != nil {
		return sessions.Record{}, err
	}
	var record sessions.Record
	if found {
		// The scoped store adds proof values to the CURRENT stored payload.
		record, err = manager.Rotate(ctx, previous)
	} else {
		record, err = manager.Create(ctx, map[string]string{auth.SessionResetPrincipalIDKey: principalID, auth.SessionResetTokenKey: token})
	}
	if err != nil {
		if store.rejection != nil {
			return sessions.Record{}, store.rejection
		}
		return sessions.Record{}, passwordResetFailure(err)
	}
	return record, nil
}

func resetToken(record sessions.Record, principalID string) (string, error) {
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil || !record.ID().Valid() {
		return "", auth.ErrInvalidResetProof
	}
	target, found := record.Value(auth.SessionResetPrincipalIDKey)
	token, stored := record.Value(auth.SessionResetTokenKey)
	if !found || !stored || target != principalID || token == "" {
		return "", auth.ErrInvalidResetProof
	}
	return token, nil
}

func (p *PasswordResetPersistence) proof(ctx context.Context, id sessions.ID, principalID string) (sessions.Record, string, error) {
	if !id.Valid() {
		return sessions.Record{}, "", auth.ErrInvalidResetProof
	}
	previous, found, err := p.manager.Peek(ctx, id)
	if err != nil {
		return sessions.Record{}, "", passwordResetFailure(err)
	}
	if !found {
		return sessions.Record{}, "", auth.ErrInvalidResetProof
	}
	token, err := resetToken(previous, principalID)
	return previous, token, err
}

func (p *PasswordResetPersistence) CheckPasswordReset(ctx context.Context, id sessions.ID, principalID string, password *string) error {
	if err := p.validCall(ctx); err != nil {
		return err
	}
	_, token, err := p.proof(ctx, id, principalID)
	if err != nil {
		return err
	}
	return resetAdmissionFailure(p.resetter.CheckPassword(ctx, principalID, token, password))
}

func (p *PasswordResetPersistence) ResetPassword(ctx context.Context, id sessions.ID, principalID, password string) (auth.PasswordResetResult, error) {
	if err := p.validCall(ctx); err != nil {
		return auth.PasswordResetResult{}, err
	}
	previous, token, err := p.proof(ctx, id, principalID)
	if err != nil {
		return auth.PasswordResetResult{}, err
	}
	prepared, err := p.resetter.Prepare(ctx, principalID, token, password)
	if err != nil {
		return auth.PasswordResetResult{}, resetAdmissionFailure(err)
	}
	store := &resetSessionStore{durableSessionStore: p.runtime.sessionStore, owner: p, principalID: principalID,
		token: token, finishing: true, previous: previous, prepared: prepared}
	if currentID, _ := previous.Value(auth.SessionPrincipalIDKey); currentID == principalID {
		// ApplyIn revokes this authenticated target session too. There must be
		// no replacement cookie, fresh authentication or preserved target data.
		if _, _, err := store.write(ctx, id, sessions.Record{}, true); err != nil {
			if store.rejection != nil {
				return auth.PasswordResetResult{}, store.rejection
			}
			return auth.PasswordResetResult{}, passwordResetFailure(err)
		}
		return auth.PasswordResetResult{ClearSession: true}, nil
	}
	manager, err := p.manager.WithStore(store)
	if err != nil {
		return auth.PasswordResetResult{}, err
	}
	record, err := manager.Rotate(ctx, previous)
	if err != nil {
		if store.rejection != nil {
			return auth.PasswordResetResult{}, store.rejection
		}
		return auth.PasswordResetResult{}, passwordResetFailure(err)
	}
	return auth.PasswordResetResult{Record: record}, nil
}

// Peek's clock is sampled INSIDE the final fence. An earlier entropy/time
// proposal must not admit a session that expired while waiting for the gate.
func (p *PasswordResetPersistence) peekIn(ctx context.Context, session db.Session, id sessions.ID) (sessions.Record, bool, error) {
	scoped := *p.runtime.sessionStore
	scoped.gate = &borrowedLoginGate{session: session}
	manager, err := p.manager.WithStore(&scoped)
	if err != nil {
		return sessions.Record{}, false, err
	}
	return manager.Peek(ctx, id)
}

type resetSessionStore struct {
	*durableSessionStore
	owner       *PasswordResetPersistence
	principalID string
	token       string
	finishing   bool
	previous    sessions.Record
	prepared    identity.PreparedPasswordReset
	rejection   error
}

func (store *resetSessionStore) Delete(context.Context, sessions.ID) error {
	// A failed operation must not independently publish expiry cleanup.
	store.rejection = auth.ErrInvalidResetProof
	return auth.ErrInvalidResetProof
}

func (store *resetSessionStore) Create(ctx context.Context, record sessions.Record) (bool, error) {
	_, created, err := store.write(ctx, sessions.ID{}, record, false)
	return created, err
}

func (store *resetSessionStore) Rotate(ctx context.Context, id sessions.ID, record sessions.Record) (sessions.Record, bool, error) {
	return store.write(ctx, id, record, false)
}

func (store *resetSessionStore) write(ctx context.Context, oldID sessions.ID, replacement sessions.Record, clear bool) (sessions.Record, bool, error) {
	var record sessions.Record
	var callbackErr, mutationErr error
	calls, completed := 0, false
	changed := false
	err := store.owner.runtime.withAtomic(ctx, func(session db.Session) error {
		calls++
		if calls != 1 || isNilInterface(session) {
			callbackErr = passwordResetFailure(nil)
			return callbackErr
		}
		callbackErr = func() error {
			if oldID.Valid() {
				current, found, err := store.owner.peekIn(ctx, session, oldID)
				if err != nil {
					return err
				}
				if !found {
					return auth.ErrInvalidResetProof
				}
				if store.finishing {
					token, err := resetToken(current, store.principalID)
					if err != nil || !hmac.Equal([]byte(token), []byte(store.token)) {
						return auth.ErrInvalidResetProof
					}
					for _, key := range []string{auth.SessionPrincipalIDKey, auth.SessionCredentialStampKey} {
						before, had := store.previous.Value(key)
						now, has := current.Value(key)
						if before != now || had != has {
							return auth.ErrInvalidResetProof
						}
					}
					if clear {
						if err := store.owner.resetter.ApplyIn(ctx, session, store.prepared); err != nil {
							return err
						}
						changed, completed = true, true
						return ctx.Err()
					}
					current = current.WithoutValue(auth.SessionResetPrincipalIDKey).WithoutValue(auth.SessionResetTokenKey)
				} else {
					current, err = current.WithValue(auth.SessionResetPrincipalIDKey, store.principalID)
					if err != nil {
						return err
					}
					current, err = current.WithValue(auth.SessionResetTokenKey, store.token)
					if err != nil {
						return err
					}
				}
				replacement, err = sessions.RestoreRecord(sessions.RecordSnapshot{
					ID: replacement.ID(), Values: current.Values(), CreatedAt: current.CreatedAt(), AccessedAt: replacement.AccessedAt(),
					AbsoluteExpiresAt: current.AbsoluteExpiresAt(), IdleExpiresAt: replacement.IdleExpiresAt(),
				}, store.limits)
				if err != nil {
					return err
				}
			}
			if err := store.owner.manager.CheckRecord(replacement); err != nil {
				return err
			}
			if !store.finishing {
				if err := store.owner.resetter.CheckTokenIn(ctx, session, store.principalID, store.token); err != nil {
					return err
				}
			}
			gate := &borrowedLoginGate{session: session}
			scoped := *store.durableSessionStore
			scoped.gate = gate
			if oldID.Valid() {
				record, changed, mutationErr = scoped.Rotate(ctx, oldID, replacement)
				if mutationErr == nil && !changed {
					return auth.ErrInvalidResetProof
				}
			} else {
				changed, mutationErr = scoped.Create(ctx, replacement)
				record = replacement
			}
			if mutationErr != nil {
				return mutationErr
			}
			if !gate.called {
				return passwordResetFailure(nil)
			}
			if store.finishing {
				if err := store.owner.resetter.ApplyIn(ctx, session, store.prepared); err != nil {
					return err
				}
			}
			completed = true
			return ctx.Err()
		}()
		return callbackErr
	})
	if calls == 1 && err != nil {
		if (callbackErr == identity.ErrInvalidResetToken || callbackErr == auth.ErrInvalidResetProof) && err == callbackErr {
			store.rejection = auth.ErrInvalidResetProof
			return sessions.Record{}, false, store.rejection
		}
		if diagnostics, rejected := validation.Rejected(callbackErr); rejected && errors.Unwrap(callbackErr) == nil && err == callbackErr {
			store.rejection = validation.Reject(diagnostics, nil)
			return sessions.Record{}, false, store.rejection
		}
		if typed, ok := mutationErr.(*sessions.Error); ok && typed != nil && typed.Cause == nil &&
			err == typed && callbackErr == typed && (typed.Code == sessions.CodeEntropy || typed.Code == sessions.CodeStoreFull) {
			return sessions.Record{}, false, typed
		}
	}
	if err != nil || callbackErr != nil || calls != 1 || !completed {
		return sessions.Record{}, false, passwordResetFailure(errors.Join(err, callbackErr))
	}
	return record, changed, nil
}

func resetAdmissionFailure(err error) error {
	if err == nil {
		return nil
	}
	if err == identity.ErrInvalidResetToken || err == auth.ErrInvalidResetProof {
		return auth.ErrInvalidResetProof
	}
	if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
		return err
	}
	return passwordResetFailure(err)
}

func passwordResetFailure(err error) error {
	if code := operatorOutcomeUnknownCode(err); code != "" {
		return &Error{Code: CodePersistence, Field: "password_reset", Detail: "password reset outcome is unknown; reconciliation is required", Cause: &query.Error{Category: query.CategoryBackend, Code: code}}
	}
	if safeOperatorErrorIs(err, context.Canceled) {
		return context.Canceled
	}
	if safeOperatorErrorIs(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return &Error{Code: CodePersistence, Field: "password_reset", Detail: "password reset persistence failed"}
}
