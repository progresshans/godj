package identity

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
)

const (
	ViewUser   auth.Permission = "godj_identity.view_user"
	AddUser    auth.Permission = "godj_identity.add_user"
	ChangeUser auth.Permission = "godj_identity.change_user"
	DeleteUser auth.Permission = "godj_identity.delete_user"
)

// ManagementBackend owns one database coordination domain for user mutations,
// session revocation and audit. The two effects must use the borrowed Session;
// they must neither start a nested transaction nor publish external effects.
type ManagementBackend interface {
	db.SnapshotReader
	db.CoordinatedAtomic
	AppendAudit(context.Context, db.Session, admin.PreparedEvent) error
	RevokePrincipalSessions(context.Context, db.Session, string) (int, error)
}

// Manager operates on current stored authorization under the write fence. It
// does not authenticate a caller: hosts pass an already authenticated actor.
// All cooperating identity writers must participate in the same fence and
// advance the user revision. Direct out-of-band SQL has no such guarantee.
type Manager struct{ state *managerState }

type managerState struct {
	backend     ManagementBackend
	directory   *Directory
	hasher      auth.PasswordHasher
	authorizer  auth.Authorizer
	collections project.Collections
}

func (Manager) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.Manager{redacted}"))
}

func NewManager(backend ManagementBackend, hasher auth.PasswordHasher, authorizer auth.Authorizer) (*Manager, error) {
	if nilIdentityValue(backend) || nilIdentityValue(hasher) || nilIdentityValue(authorizer) {
		return nil, managementError(CodeInvalidConfig, "manager", nil)
	}
	directory, err := NewDirectory(backend)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, "manager", err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, "manager", err)
	}
	return &Manager{state: &managerState{backend: backend, directory: directory, hasher: hasher, authorizer: authorizer, collections: collections}}, nil
}

// SetPassword is an administrative replacement, not self-service password
// change or reset. It requires the actor's CURRENT change_user permission plus
// the configured deny overlay. Password hashing happens at most once, after
// a successful preflight and outside all database scopes. It then rechecks
// authorization, target revision and credential under the coordination fence.
// The password, revision, target's session deletions and semantic audit commit
// together. On any error no Profile is published and work is never retried.
//
// An already admitted request is not retroactively canceled. A login using an
// old credential that races this commit may create an old-stamp session; the
// normal resolver rejects it on its next request. This operation does not
// preserve the caller's session when the caller is also the target.
func (manager *Manager) SetPassword(ctx context.Context, actor auth.Principal, userID, expectedRevision int64, password string) (Profile, error) {
	if ctx == nil || userID <= 0 || expectedRevision <= 0 || expectedRevision == math.MaxInt64 || password == "" {
		return Profile{}, managementError(CodeInvalidInput, "password_change", nil)
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, managementError(CodeInvalidInput, "context", err)
	}
	if manager == nil || manager.state == nil {
		return Profile{}, managementError(CodeInvalidConfig, "manager", nil)
	}
	if !actor.Authenticated() {
		return Profile{}, managementError(CodePermission, "actor", nil)
	}
	state := manager.state
	var before Account
	var callbackErr error
	calls := 0
	err := state.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || nilIdentityValue(reader) {
			callbackErr = managementError(CodePersistence, "snapshot_contract", nil)
			return callbackErr
		}
		_, before, callbackErr = manager.managedUserForChange(ctx, reader, actor.ID(), userID, expectedRevision)
		return callbackErr
	})
	if err = errors.Join(err, callbackErr, ctx.Err()); err != nil {
		return Profile{}, managementWriteFailure(err)
	}
	if calls != 1 {
		return Profile{}, managementError(CodePersistence, "snapshot_contract", nil)
	}
	encoded, err := state.hasher.Hash(ctx, password)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return Profile{}, managementError(CodeInvalidInput, "password", err)
	}
	if err := state.hasher.ValidateEncoded(encoded); err != nil {
		return Profile{}, managementError(CodeInvalidConfig, "password_hasher", err)
	}
	candidate, err := auth.NewCredential(before.Profile().Username, encoded, before.value().credential.Principal())
	if err != nil || candidate.MatchesSessionStamp(before.value().credential.SessionStamp()) {
		return Profile{}, managementError(CodeInvalidConfig, "password_hasher", err)
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, managementError(CodeInvalidInput, "context", err)
	}
	var result Profile
	calls = 0
	callbackErr = nil
	err = state.backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		calls++
		if calls != 1 || nilIdentityValue(session) {
			callbackErr = managementError(CodePersistence, "transaction_contract", nil)
			return callbackErr
		}
		callbackErr = func() error {
			row, current, err := manager.managedUserForChange(ctx, session, actor.ID(), userID, expectedRevision)
			if err != nil {
				return err
			}
			if !current.value().credential.MatchesSessionStamp(before.value().credential.SessionStamp()) {
				return managementError(CodeConflict, "user", nil)
			}
			updated, err := models.UserObjects.Update(ctx, session, row, (models.UserPatch{}).WithEncodedPassword(encoded).WithRevision(expectedRevision+1))
			if err != nil {
				return err
			}
			if _, err := state.backend.RevokePrincipalSessions(ctx, session, row.PrincipalID); err != nil {
				return err
			}
			event, err := admin.PrepareEvent(actor.ID(), "godj_identity.user", userID, admin.ActionChange, []string{"password"}, "")
			if err != nil {
				return err
			}
			if err := state.backend.AppendAudit(ctx, session, event); err != nil {
				return err
			}
			result = profileFromRow(updated)
			return ctx.Err()
		}()
		return callbackErr
	})
	if err = errors.Join(err, callbackErr); err != nil {
		return Profile{}, managementWriteFailure(err)
	}
	if calls != 1 || result.ID == 0 {
		return Profile{}, managementError(CodePersistence, "transaction_contract", nil)
	}
	// The backend confirmed commit. A later cancellation must not turn this
	// into an apparent rollback or invite a password-change retry.
	return result, nil
}

func (manager *Manager) managedUserForChange(ctx context.Context, reader db.Queryer, actorID string, userID, revision int64) (models.User, Account, error) {
	if err := manager.requireActor(ctx, reader, actorID, ChangeUser); err != nil {
		return models.User{}, Account{}, err
	}
	row, found, err := models.UserObjects.Using(reader).Filter(models.UserFields.ID.Exact(userID)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil {
		return models.User{}, Account{}, err
	}
	if !found {
		return models.User{}, Account{}, managementError(CodeNotFound, "user", nil)
	}
	if row.Revision != revision {
		return models.User{}, Account{}, managementError(CodeConflict, "user", nil)
	}
	account, err := manager.state.directory.accountFromRow(ctx, reader, row)
	return row, account, err
}

func (manager *Manager) requireActor(ctx context.Context, reader db.Queryer, actorID string, permission auth.Permission, additional ...auth.Permission) error {
	actor, present, err := models.UserObjects.Using(reader).Filter(models.UserFields.PrincipalID.Exact(actorID)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil {
		return err
	}
	if !present {
		return managementError(CodePermission, "actor", nil)
	}
	account, err := manager.state.directory.accountFromRow(ctx, reader, actor)
	if err != nil {
		return err
	}
	principal := account.value().credential.Principal()
	for _, required := range append([]auth.Permission{permission}, additional...) {
		if !principal.Has(required) {
			return managementError(CodePermission, "actor", nil)
		}
		// Authorizers remain a deny overlay over every current stored grant.
		// They must not recursively acquire this database coordination domain.
		allowed, err := manager.state.authorizer.Allowed(ctx, principal, required)
		if err != nil {
			return managementError(CodePersistence, "actor", err)
		}
		if !allowed {
			return managementError(CodePermission, "actor", nil)
		}
	}
	return nil
}
