package model

import (
	"context"
	"crypto/hmac"
	"errors"
	"math"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

// StartPasswordReset exchanges recipient-only material for a server-side
// proof. An active prior session rotates with its CURRENT values and lifetime;
// an absent/expired prior session creates an anonymous proof. Final refusal
// never falls back to creating a session or publishes independent cleanup.
func (owner *PasswordResetter[M]) StartPasswordReset(ctx context.Context, previousID sessions.ID, principalID, encoded string) (sessions.Record, error) {
	_, token, err := owner.preflight(ctx, principalID, encoded, nil)
	if err != nil {
		return sessions.Record{}, err
	}
	var previous sessions.Record
	var present bool
	if previousID.Valid() {
		previous, present, err = owner.state.manager.Peek(ctx, previousID)
		if err != nil {
			return sessions.Record{}, accountPasswordFailure(err)
		}
	}
	store := &passwordResetSessionStore[M]{SessionStore: owner.state.store, owner: owner, principalID: principalID, encodedToken: encoded, token: token}
	defer store.clear()
	manager, err := owner.state.manager.WithStore(store)
	if err != nil {
		return sessions.Record{}, accountPasswordFailure(err)
	}
	var record sessions.Record
	if present {
		record, err = manager.Rotate(ctx, previous)
	} else {
		record, err = manager.Create(ctx, map[string]string{auth.SessionResetPrincipalIDKey: principalID, auth.SessionResetTokenKey: encoded})
	}
	if err != nil {
		return sessions.Record{}, store.failure(err)
	}
	return record, nil
}

func passwordResetProofToken(record sessions.Record, principalID string) (string, error) {
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil || !record.ID().Valid() {
		return "", auth.ErrInvalidResetProof
	}
	target, found := record.Value(auth.SessionResetPrincipalIDKey)
	encoded, stored := record.Value(auth.SessionResetTokenKey)
	if !found || !stored || target != principalID {
		return "", auth.ErrInvalidResetProof
	}
	if _, valid := auth.ParsePasswordResetToken(encoded); !valid {
		return "", auth.ErrInvalidResetProof
	}
	return encoded, nil
}

func (owner *PasswordResetter[M]) proof(ctx context.Context, id sessions.ID, principalID string) (sessions.Record, string, error) {
	if err := owner.valid(ctx); err != nil {
		return sessions.Record{}, "", err
	}
	if !id.Valid() {
		return sessions.Record{}, "", auth.ErrInvalidResetProof
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return sessions.Record{}, "", auth.ErrInvalidResetProof
	}
	record, present, err := owner.state.manager.Peek(ctx, id)
	if err != nil {
		return sessions.Record{}, "", accountPasswordFailure(err)
	}
	if !present {
		return sessions.Record{}, "", auth.ErrInvalidResetProof
	}
	token, err := passwordResetProofToken(record, principalID)
	return record, token, err
}

// CheckPasswordReset has no hash, touch or expiry-cleanup effects. A nil
// password checks only the current proof; it grants no reusable write access.
func (owner *PasswordResetter[M]) CheckPasswordReset(ctx context.Context, id sessions.ID, principalID string, password *string) error {
	_, encoded, err := owner.proof(ctx, id, principalID)
	if err != nil {
		return err
	}
	_, _, err = owner.preflight(ctx, principalID, encoded, password)
	return err
}

type passwordResetPreparation[M any] struct {
	before       passwordResetAccount[M]
	after        auth.Credential
	raw, encoded string
}

// ResetPassword hashes once outside every DB scope. The final transaction
// reobserves the current proof, authentication binding, target, token and typed
// policy, then couples Password/current Revision, target revocation, proof
// cleanup and self-authored audit. Unknown outcomes never retry or publish.
func (owner *PasswordResetter[M]) ResetPassword(ctx context.Context, id sessions.ID, principalID, password string) (auth.PasswordResetResult, error) {
	var zero auth.PasswordResetResult
	previous, encodedToken, err := owner.proof(ctx, id, principalID)
	if err != nil {
		return zero, err
	}
	before, token, err := owner.preflight(ctx, principalID, encodedToken, &password)
	if err != nil {
		return zero, err
	}
	state := owner.state
	encoded, err := encodeUsablePassword(ctx, state.hasher, password)
	if err != nil {
		return zero, err
	}
	binding := state.store.state.credentials.state.binding
	candidate, err := binding.state.objects.ApplyValues(before.row, map[string]query.Value{binding.state.role.Password: query.String(encoded)})
	if err != nil {
		return zero, accountPasswordFailure(err)
	}
	after, err := binding.prepareCredentialForDomain(before.domain, candidate, before.credential.Principal().Permissions())
	if err != nil || !after.HasUsablePassword() || after.MatchesSessionStamp(before.credential.SessionStamp()) {
		return zero, managementError(CodeInvalidConfig, err)
	}
	prepared := passwordResetPreparation[M]{before, after, password, encoded}
	store := &passwordResetSessionStore[M]{SessionStore: state.store, owner: owner, principalID: principalID, encodedToken: encodedToken, token: token, previous: previous, prepared: &prepared}
	defer func() { store.clear(); prepared = passwordResetPreparation[M]{} }()
	if currentID, _ := previous.Value(auth.SessionPrincipalIDKey); currentID == principalID {
		if _, err := store.write(ctx, id, sessions.Record{}, true); err != nil {
			return zero, store.failure(err)
		}
		return auth.PasswordResetResult{ClearSession: true}, nil
	}
	manager, err := state.manager.WithStore(store)
	if err != nil {
		return zero, accountPasswordFailure(err)
	}
	record, err := manager.Rotate(ctx, previous)
	if err != nil {
		return zero, store.failure(err)
	}
	return auth.PasswordResetResult{Record: record}, nil
}

// One interceptor is private to one call, including its bounded confirmed-ID
// collision retries. It never escapes as a reusable prepared password/proof.
type passwordResetSessionStore[M any] struct {
	*SessionStore[M]
	owner                     *PasswordResetter[M]
	principalID, encodedToken string
	token                     auth.PasswordResetSignature
	previous                  sessions.Record
	prepared                  *passwordResetPreparation[M]
	rejection                 error
}

func (store *passwordResetSessionStore[M]) clear() {
	store.prepared = nil
	store.previous = sessions.Record{}
	store.token = auth.PasswordResetSignature{}
	store.encodedToken = ""
}
func (store *passwordResetSessionStore[M]) failure(err error) error {
	if store.rejection != nil {
		return store.rejection
	}
	return accountPasswordFailure(err)
}
func (store *passwordResetSessionStore[M]) Delete(context.Context, sessions.ID) error {
	store.rejection = auth.ErrInvalidResetProof
	return store.rejection
}
func (store *passwordResetSessionStore[M]) Create(ctx context.Context, candidate sessions.Record) (bool, error) {
	result, err := store.write(ctx, sessions.ID{}, candidate, false)
	return result.present, err
}
func (store *passwordResetSessionStore[M]) Rotate(ctx context.Context, oldID sessions.ID, candidate sessions.Record) (sessions.Record, bool, error) {
	result, err := store.write(ctx, oldID, candidate, false)
	return result.record, result.present, err
}

func (store *passwordResetSessionStore[M]) write(ctx context.Context, oldID sessions.ID, candidate sessions.Record, clear bool) (accountSessionRecord, error) {
	result, err := accountSessionWrite(ctx, store.SessionStore, func(work context.Context, scope db.Session, scoped *sessiondb.Store) (accountSessionRecord, error) {
		var zero accountSessionRecord
		state := store.owner.state
		var current sessions.Record
		if oldID.Valid() {
			manager, err := state.manager.WithStore(scoped)
			if err != nil {
				return zero, err
			}
			var present bool
			current, present, err = manager.Peek(work, oldID)
			if err != nil {
				return zero, err
			}
			if !present {
				return zero, auth.ErrInvalidResetProof
			}
			if store.prepared != nil {
				encoded, err := passwordResetProofToken(current, store.principalID)
				if err != nil || !hmac.Equal([]byte(encoded), []byte(store.encodedToken)) {
					return zero, auth.ErrInvalidResetProof
				}
				for _, key := range []string{auth.SessionPrincipalIDKey, auth.SessionCredentialStampKey} {
					before, had := store.previous.Value(key)
					now, has := current.Value(key)
					if had != has || !hmac.Equal([]byte(before), []byte(now)) {
						return zero, auth.ErrInvalidResetProof
					}
				}
			}
		} else if store.prepared != nil || clear {
			return zero, auth.ErrInvalidResetProof
		}
		account, err := state.accountIn(work, scope, store.principalID)
		if err != nil {
			return zero, err
		}
		now, err := state.instant()
		if err != nil {
			return zero, err
		}
		if !account.present || !state.config.Keys.Accepts(account.observation, store.token, now.Unix(), state.config.Timeout) {
			return zero, auth.ErrInvalidResetProof
		}
		if store.prepared != nil {
			if account.id != store.prepared.before.id {
				return zero, auth.ErrInvalidResetProof
			}
			if err := state.validatePassword(work, store.prepared.raw, account.row); err != nil {
				return zero, err
			}
		}
		if oldID.Valid() {
			// Recheck after the current policy, using the session manager's
			// own serialized clock, not the separately configured token clock.
			active, err := state.manager.CheckActiveRecord(current)
			if err != nil {
				return zero, err
			}
			if !active {
				return zero, auth.ErrInvalidResetProof
			}
		}
		if clear {
			currentID, _ := current.Value(auth.SessionPrincipalIDKey)
			if store.prepared == nil || currentID != store.principalID {
				return zero, auth.ErrInvalidResetProof
			}
			if err := store.applyIn(work, scope, scoped, account, now); err != nil {
				return zero, err
			}
			return accountSessionRecord{present: true}, nil
		}
		if oldID.Valid() {
			values := current
			if store.prepared != nil {
				values = values.WithoutValue(auth.SessionResetPrincipalIDKey).WithoutValue(auth.SessionResetTokenKey)
			} else {
				values, err = values.WithValue(auth.SessionResetPrincipalIDKey, store.principalID)
				if err != nil {
					return zero, err
				}
				values, err = values.WithValue(auth.SessionResetTokenKey, store.encodedToken)
				if err != nil {
					return zero, err
				}
			}
			accessed, idle := candidate.AccessedAt(), candidate.IdleExpiresAt()
			if accessed.Before(current.AccessedAt()) {
				accessed = current.AccessedAt()
			}
			if idle.Before(current.IdleExpiresAt()) {
				idle = current.IdleExpiresAt()
			}
			if idle.After(current.AbsoluteExpiresAt()) {
				idle = current.AbsoluteExpiresAt()
			}
			candidate, err = sessions.RestoreRecord(sessions.RecordSnapshot{ID: candidate.ID(), Values: values.Values(), CreatedAt: current.CreatedAt(), AccessedAt: accessed, AbsoluteExpiresAt: current.AbsoluteExpiresAt(), IdleExpiresAt: idle}, state.store.state.limits)
			if err != nil {
				return zero, err
			}
		}
		active, err := state.manager.CheckActiveRecord(candidate)
		if err != nil {
			return zero, err
		}
		if !active {
			return zero, auth.ErrInvalidResetProof
		}
		if !oldID.Valid() {
			created, err := scoped.Create(work, candidate)
			if err != nil {
				return zero, err
			}
			return accountSessionRecord{candidate, created}, nil
		}
		published, rotated, err := scoped.Rotate(work, oldID, candidate)
		if err != nil {
			return zero, err
		}
		if !rotated {
			return zero, auth.ErrInvalidResetProof
		}
		if store.prepared != nil {
			if err := store.applyIn(work, scope, scoped, account, now); err != nil {
				return zero, err
			}
		}
		return accountSessionRecord{published, true}, nil
	})
	if err != nil {
		if err == auth.ErrInvalidResetProof {
			store.rejection = err
		} else if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
			store.rejection = err
		}
		// Only a direct, cause-free session collision after confirmed rollback
		// reaches Manager's existing bounded ID retry. No password work repeats.
	}
	return result, err
}

func (store *passwordResetSessionStore[M]) applyIn(ctx context.Context, scope db.Session, scoped *sessiondb.Store, current passwordResetAccount[M], now time.Time) error {
	binding := store.state.credentials.state.binding
	objects, role := binding.state.objects, binding.state.role
	values, err := objects.ModelValues(current.row)
	if err != nil {
		return err
	}
	revision, valid := values[role.Revision].Integer()
	if !valid || revision < 1 || revision == math.MaxInt64 {
		return invalidAccountCredential()
	}
	updated, err := objects.ApplyValues(current.row, map[string]query.Value{role.Password: query.String(store.prepared.encoded), role.Revision: query.Integer(revision + 1)})
	if err != nil {
		return err
	}
	credential, err := binding.prepareCredentialForDomain(current.domain, updated, current.credential.Principal().Permissions())
	if err != nil || !credential.HasUsablePassword() || !credential.MatchesSessionStamp(store.prepared.after.SessionStamp()) {
		return invalidAccountCredential()
	}
	if err := objects.Save(ctx, scope, &updated, orm.UpdateFieldNames[M](role.Password, role.Revision)); err != nil {
		return err
	}
	if _, err := scoped.DeleteMatching(ctx, auth.SessionPrincipalIDKey, store.principalID, sessions.ID{}); err != nil {
		return err
	}
	event, err := accountaudit.Prepare(credential.Principal(), role.Model.AppLabel+"."+role.Model.ModelName, current.id, admin.ActionChange, []string{role.Password}, now)
	if err != nil {
		return err
	}
	if _, err := accountaudit.AppendIn(ctx, scope, event); err != nil {
		return err
	}
	return validateAccountSessionScope(ctx, scope)
}
