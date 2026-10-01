package identity

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

// PreparedUserCreation owns one immutable credential candidate, including its
// private password material. Drop it when the operation ends. Preparation
// writes nothing and hashes at most once; only CommitUserCreation can publish
// the user. This is not a saved model or reusable authorization snapshot.
type PreparedUserCreation struct{ state *preparedUserCreation }

type preparedUserCreation struct {
	owner               *Manager
	actorID             string
	row                 models.User
	groups, permissions []int64
	caseInsensitive     bool
	password            passwordInput
	encoded             string
	attempted           atomic.Bool
}

func (PreparedUserCreation) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PreparedUserCreation{redacted}"))
}
func (PreparedUserCreation) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.PreparedUserCreation{redacted}"`), nil
}

// PrepareUserCreation normalizes and checks the input under current authority,
// ends the read scope, then hashes the password once. The candidate is bound to
// this manager and the authenticated actor's stable ID. Call CommitUserCreation
// to save; abandoning the candidate has no database effect.
func (manager *Manager) PrepareUserCreation(ctx context.Context, actor auth.Principal, input UserCreate, password string) (PreparedUserCreation, error) {
	return manager.prepareUserCreation(ctx, actor, input, passwordInput{raw: password})
}

// PrepareUserCreationWithUnusablePassword follows the same read/authority
// contract without invoking password policy or hashing.
func (manager *Manager) PrepareUserCreationWithUnusablePassword(ctx context.Context, actor auth.Principal, input UserCreate) (PreparedUserCreation, error) {
	return manager.prepareUserCreation(ctx, actor, input, passwordInput{unusable: true})
}

func (manager *Manager) prepareUserCreation(ctx context.Context, actor auth.Principal, input UserCreate, password passwordInput) (PreparedUserCreation, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return PreparedUserCreation{}, err
	}
	if !password.unusable && password.raw == "" {
		return PreparedUserCreation{}, managementInputError("password", "required")
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: input.principalID}); err != nil {
		return PreparedUserCreation{}, managementInputError("principal_id", "invalid")
	}
	patch, err := input.patch.normalize()
	if err != nil {
		return PreparedUserCreation{}, err
	}
	username, set := patch.username.Get()
	if !set {
		return PreparedUserCreation{}, managementInputError("username", "required")
	}
	row, _, _ := patch.apply(models.User{PrincipalID: input.principalID, DateJoined: time.Now().UTC().Truncate(time.Microsecond), Revision: 1})
	row.Username = username
	groups, _ := patch.groups.Get()
	permissions, _ := patch.permissions.Get()
	candidate := &preparedUserCreation{owner: manager, actorID: actor.ID(), row: row, groups: groups, permissions: permissions, caseInsensitive: input.caseInsensitiveUsernameCheck, password: password}
	if _, err := managementSnapshot(ctx, manager, func(reader db.Queryer) (struct{}, error) {
		return struct{}{}, manager.preflightUserCreation(ctx, actor, reader, candidate, "identity-creation-preflight")
	}); err != nil {
		return PreparedUserCreation{}, err
	}
	encoded, err := password.encode(ctx, manager.state.hasher)
	if err != nil {
		return PreparedUserCreation{}, err
	}
	principal, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser})
	if credential, err := auth.NewCredential(row.Username, encoded, principal); err != nil || credential.HasUsablePassword() == password.unusable {
		return PreparedUserCreation{}, managementError(CodeInvalidConfig, "password_hasher", err)
	}
	if err := ctx.Err(); err != nil {
		return PreparedUserCreation{}, err
	}
	candidate.encoded = encoded
	return PreparedUserCreation{state: candidate}, nil
}

func (candidate *preparedUserCreation) create(encoded string) models.UserCreate {
	row := candidate.row
	return models.NewUserCreate(row.PrincipalID, row.Username, encoded, row.DateJoined).
		WithFirstName(row.FirstName).WithLastName(row.LastName).WithEmail(row.Email).
		WithActive(row.Active).WithStaff(row.Staff).WithSuperuser(row.Superuser)
}

func (manager *Manager) preflightUserCreation(ctx context.Context, actor auth.Principal, reader db.Queryer, candidate *preparedUserCreation, encoded string) error {
	if err := manager.requireActor(ctx, reader, actor.ID(), AddUser, ChangeUser); err != nil {
		return err
	}
	if err := manager.validateUserKeys(ctx, reader, candidate.groups, candidate.permissions); err != nil {
		return err
	}
	if err := manager.validateEffectiveGrants(ctx, reader, candidate.groups, candidate.permissions); err != nil {
		return err
	}
	failures, err := models.UserObjects.ValidateUniqueCreate(ctx, reader, candidate.create(encoded))
	if err != nil {
		return err
	}
	if !failures.Empty() {
		return validation.Reject(failures, nil)
	}
	if candidate.caseInsensitive {
		exists, err := models.UserObjects.Using(reader).Filter(models.UserFields.Username.IExact(candidate.row.Username)).Exists(ctx)
		if err != nil {
			return err
		}
		if exists {
			return managementInputError("username", "unique")
		}
	}
	return candidate.password.validate(ctx, manager, profileFromRow(candidate.row))
}

// CommitUserCreation rechecks current authority, constraints, relation choices
// and password policy under the native write fence. User, memberships and audit
// commit together. It performs no hash work and never retries an uncertain
// outcome. Copies share one commit attempt, including failed/uncertain writes;
// a fresh preparation is required afterwards. Another manager or actor cannot
// adopt or consume this candidate. A canceled call that never enters the write
// attempt does not consume it.
func (manager *Manager) CommitUserCreation(ctx context.Context, actor auth.Principal, prepared PreparedUserCreation) (UserDetails, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return UserDetails{}, err
	}
	candidate := prepared.state
	if candidate == nil || candidate.owner != manager {
		return UserDetails{}, managementError(CodeInvalidInput, "user_creation", nil)
	}
	if candidate.actorID != actor.ID() {
		return UserDetails{}, managementError(CodePermission, "actor", nil)
	}
	if !candidate.attempted.CompareAndSwap(false, true) {
		return UserDetails{}, managementError(CodeConflict, "user_creation", nil)
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (UserDetails, error) {
		if err := manager.preflightUserCreation(ctx, actor, session, candidate, candidate.encoded); err != nil {
			return UserDetails{}, err
		}
		created, err := models.UserObjects.Create(ctx, session, candidate.create(candidate.encoded))
		if err != nil {
			return UserDetails{}, err
		}
		if err := manager.setUserKeys(ctx, session, created, candidate.groups, candidate.permissions, true, true); err != nil {
			return UserDetails{}, err
		}
		result, err := manager.userDetails(ctx, session, created.ID)
		if err != nil {
			return UserDetails{}, err
		}
		if err := manager.auditUser(ctx, session, actor.ID(), created.ID, admin.ActionAdd, []string{"username", "first_name", "last_name", "email", "active", "staff", "superuser", "groups", "permissions", "password"}); err != nil {
			return UserDetails{}, err
		}
		return result, nil
	})
}
