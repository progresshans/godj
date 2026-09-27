package identity

import (
	"context"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
)

// UserDetails is a detached, explicitly authorized management projection.
// Permissions are direct grants; inherited permissions belong to Account.
type UserDetails struct {
	Profile
	GroupIDs      []int64 `json:"groups"`
	PermissionIDs []int64 `json:"permissions"`
}

type UserPage struct {
	Users []Profile `json:"users"`
	Total int64     `json:"total"`
}

// UserDeletion reports only the completed operation. Delete-only authority
// does not implicitly grant access to the target's full profile.
type UserDeletion struct {
	ID          int64 `json:"id"`
	Revision    int64 `json:"revision"`
	DeletedRows int64 `json:"deleted_rows"`
}

// User and Users require the current view_user permission or change_user, as
// does Django's administrative view admission. Each read owns one snapshot.
func (manager *Manager) User(ctx context.Context, actor auth.Principal, id int64) (UserDetails, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return UserDetails{}, err
	}
	if id <= 0 {
		return UserDetails{}, managementError(CodeInvalidInput, "user", nil)
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) (UserDetails, error) {
		if err := manager.requireView(ctx, reader, actor.ID(), ViewUser, ChangeUser); err != nil {
			return UserDetails{}, err
		}
		return manager.userDetails(ctx, reader, id)
	})
}

// Users returns a bounded scalar page in primary-key order. Per-user relation
// details are read with User; listing never performs one query per result.
func (manager *Manager) Users(ctx context.Context, actor auth.Principal, offset, limit int) (UserPage, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return UserPage{}, err
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return UserPage{}, managementError(CodeInvalidInput, "user", nil)
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) (UserPage, error) {
		if err := manager.requireView(ctx, reader, actor.ID(), ViewUser, ChangeUser); err != nil {
			return UserPage{}, err
		}
		objects := models.UserObjects.Using(reader).OrderBy(models.UserFields.ID.Asc())
		count, err := objects.Count(ctx)
		if err != nil {
			return UserPage{}, err
		}
		page, err := objects.Offset(offset)
		if err != nil {
			return UserPage{}, err
		}
		page, err = page.Limit(limit)
		if err != nil {
			return UserPage{}, err
		}
		rows, err := page.All(ctx)
		if err != nil {
			return UserPage{}, err
		}
		result := UserPage{Users: make([]Profile, len(rows)), Total: count}
		for index, row := range rows {
			if err := validateManagedUserRow(row); err != nil {
				return UserPage{}, err
			}
			result.Users[index] = profileFromRow(row)
		}
		return result, nil
	})
}

// CreateUser creates a new credential, profile and requested memberships in one
// coordinated transaction. It never replaces an existing identity or username.
// The host supplies a new opaque principal ID; no browser-provided identity is
// implicitly trusted. Hash work occurs once outside both database scopes.
func (manager *Manager) CreateUser(ctx context.Context, actor auth.Principal, input UserCreate, password string) (UserDetails, error) {
	return manager.createUser(ctx, actor, input, passwordInput{raw: password})
}

// CreateUserWithUnusablePassword creates an account that cannot use password
// authentication. Authorization, profile and relation validation, and atomic
// audit are identical to CreateUser; no raw password policy or hash is run.
func (manager *Manager) CreateUserWithUnusablePassword(ctx context.Context, actor auth.Principal, input UserCreate) (UserDetails, error) {
	return manager.createUser(ctx, actor, input, passwordInput{unusable: true})
}

func (manager *Manager) createUser(ctx context.Context, actor auth.Principal, input UserCreate, password passwordInput) (UserDetails, error) {
	prepared, err := manager.prepareUserCreation(ctx, actor, input, password)
	if err != nil {
		return UserDetails{}, err
	}
	return manager.CommitUserCreation(ctx, actor, prepared)
}

// UpdateUser preserves the credential and principal ID. Effective permissions
// are read on each request; role/grant changes do not rehash or rotate a stamp.
// Deactivation deletes the target's currently stored sessions in this commit.
func (manager *Manager) UpdateUser(ctx context.Context, actor auth.Principal, id, revision int64, input UserPatch) (UserDetails, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return UserDetails{}, err
	}
	if err := validUserRevision(id, revision); err != nil {
		return UserDetails{}, err
	}
	input, err := input.normalize()
	if err != nil {
		return UserDetails{}, err
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (UserDetails, error) {
		row, account, err := manager.managedUserForChange(ctx, session, actor.ID(), id, revision)
		if err != nil {
			return UserDetails{}, err
		}
		before, err := manager.userDetailsFromAccount(ctx, session, account)
		if err != nil {
			return UserDetails{}, err
		}
		desired, patch, changed := input.apply(row)
		groups, changeGroups := input.groups.Get()
		if !changeGroups {
			groups = before.GroupIDs
		}
		permissions, changePermissions := input.permissions.Get()
		if !changePermissions {
			permissions = before.PermissionIDs
		}
		if err := manager.validateUserKeys(ctx, session, groups, permissions); err != nil {
			return UserDetails{}, err
		}
		if err := manager.validateEffectiveGrants(ctx, session, groups, permissions); err != nil {
			return UserDetails{}, err
		}
		changeGroups = !slices.Equal(groups, before.GroupIDs)
		changePermissions = !slices.Equal(permissions, before.PermissionIDs)
		if changeGroups {
			changed = append(changed, "groups")
		}
		if changePermissions {
			changed = append(changed, "permissions")
		}
		if len(changed) == 0 {
			return before, nil
		}
		patch = patch.WithRevision(revision + 1)
		violations, err := models.UserObjects.ValidateUniqueUpdate(ctx, session, row, patch)
		if err != nil {
			return UserDetails{}, err
		}
		if !violations.Empty() {
			return UserDetails{}, validation.Reject(violations, nil)
		}
		updated, err := models.UserObjects.Update(ctx, session, row, patch)
		if err != nil {
			return UserDetails{}, err
		}
		if err := manager.setUserKeys(ctx, session, updated, groups, permissions, changeGroups, changePermissions); err != nil {
			return UserDetails{}, err
		}
		if row.Active && !desired.Active {
			if _, err := manager.state.backend.RevokePrincipalSessions(ctx, session, row.PrincipalID); err != nil {
				return UserDetails{}, err
			}
		}
		result, err := manager.userDetails(ctx, session, id)
		if err != nil {
			return UserDetails{}, err
		}
		if err := manager.auditUser(ctx, session, actor.ID(), id, admin.ActionChange, changed); err != nil {
			return UserDetails{}, err
		}
		return result, nil
	})
}

// DeleteUser requires the HOST's generated deleter, including every incoming
// relation outside the identity app. PROTECT/CASCADE/SET_NULL, session deletion
// and audit share one outer transaction. No identity-only fallback is chosen.
func (manager *Manager) DeleteUser(ctx context.Context, actor auth.Principal, id, revision int64, policy orm.RelationDeleter[models.User]) (UserDeletion, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return UserDeletion{}, err
	}
	if err := validUserRevision(id, revision); err != nil {
		return UserDeletion{}, err
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (UserDeletion, error) {
		if err := manager.requireActor(ctx, session, actor.ID(), DeleteUser); err != nil {
			return UserDeletion{}, err
		}
		row, present, err := models.UserObjects.Using(session).Filter(models.UserFields.ID.Exact(id)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
		if err != nil {
			return UserDeletion{}, err
		}
		if !present {
			return UserDeletion{}, managementError(CodeNotFound, "user", nil)
		}
		if row.Revision != revision {
			return UserDeletion{}, managementError(CodeConflict, "user", nil)
		}
		if err := validateManagedUserRow(row); err != nil {
			return UserDeletion{}, err
		}
		deleted, err := policy.DeleteInSession(ctx, session, row)
		if err != nil {
			return UserDeletion{}, err
		}
		if _, err := manager.state.backend.RevokePrincipalSessions(ctx, session, row.PrincipalID); err != nil {
			return UserDeletion{}, err
		}
		if err := manager.auditUser(ctx, session, actor.ID(), id, admin.ActionDelete, nil); err != nil {
			return UserDeletion{}, err
		}
		return UserDeletion{ID: row.ID, Revision: row.Revision, DeletedRows: deleted}, nil
	})
}

func validateManagedUserRow(row models.User) error {
	if err := validateUserProfileText(row); err != nil {
		return managementError(CodePersistence, "user", err)
	}
	if row.ID <= 0 || row.Revision <= 0 {
		return managementError(CodePersistence, "user", nil)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser})
	if err != nil {
		return err
	}
	_, err = auth.NewCredential(row.Username, row.EncodedPassword, principal)
	return err
}

func (manager *Manager) userDetails(ctx context.Context, reader db.Queryer, id int64) (UserDetails, error) {
	row, found, err := models.UserObjects.Using(reader).Filter(models.UserFields.ID.Exact(id)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil {
		return UserDetails{}, err
	}
	if !found {
		return UserDetails{}, managementError(CodeNotFound, "user", nil)
	}
	account, err := manager.state.directory.accountFromRow(ctx, reader, row)
	if err != nil {
		return UserDetails{}, err
	}
	return manager.userDetailsFromAccount(ctx, reader, account)
}

func (manager *Manager) userDetailsFromAccount(ctx context.Context, reader db.Queryer, account Account) (UserDetails, error) {
	keys, err := manager.userKeys(ctx, reader, account.Profile().ID)
	if err != nil {
		return UserDetails{}, err
	}
	return UserDetails{Profile: account.Profile(), GroupIDs: keys.groups, PermissionIDs: keys.permissions}, nil
}

type userRelationKeys struct{ groups, permissions []int64 }

func (manager *Manager) userKeys(ctx context.Context, reader db.Queryer, id int64) (userRelationKeys, error) {
	relations := manager.state.directory.state.relations
	groupsQuery, err := models.GroupObjects.Using(reader).Filter(relations.IdentityGroup.Users.ID.Exact(id)).OrderBy(models.GroupFields.ID.Asc()).Limit(MaximumUserGroups + 1)
	if err != nil {
		return userRelationKeys{}, err
	}
	groups, err := groupsQuery.All(ctx)
	if err != nil {
		return userRelationKeys{}, err
	}
	if len(groups) > MaximumUserGroups {
		return userRelationKeys{}, managementError(CodePersistence, "user", nil)
	}
	permissionQuery, err := models.PermissionObjects.Using(reader).Filter(relations.IdentityPermission.Users.ID.Exact(id)).OrderBy(models.PermissionFields.ID.Asc()).Limit(auth.MaximumPermissions + 1)
	if err != nil {
		return userRelationKeys{}, err
	}
	permissions, err := permissionQuery.All(ctx)
	if err != nil {
		return userRelationKeys{}, err
	}
	if len(permissions) > auth.MaximumPermissions {
		return userRelationKeys{}, managementError(CodePersistence, "user", nil)
	}
	result := userRelationKeys{groups: make([]int64, len(groups)), permissions: make([]int64, len(permissions))}
	for index, group := range groups {
		if group.Revision <= 0 {
			return userRelationKeys{}, managementError(CodePersistence, "user", nil)
		}
		result.groups[index] = group.ID
	}
	for index, permission := range permissions {
		if permission.Revision <= 0 {
			return userRelationKeys{}, managementError(CodePersistence, "user", nil)
		}
		result.permissions[index] = permission.ID
	}
	return result, nil
}

func (manager *Manager) validateUserKeys(ctx context.Context, reader db.Queryer, groups, permissions []int64) error {
	groupRows, err := models.GroupObjects.Using(reader).Filter(models.GroupFields.ID.In(groups...)).OrderBy(models.GroupFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	if len(groupRows) != len(groups) {
		return managementInputError("groups", "invalid_choice")
	}
	for index, row := range groupRows {
		if row.ID != groups[index] || row.Revision <= 0 {
			return managementInputError("groups", "invalid_choice")
		}
	}
	permissionRows, err := models.PermissionObjects.Using(reader).Filter(models.PermissionFields.ID.In(permissions...)).OrderBy(models.PermissionFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	if len(permissionRows) != len(permissions) {
		return managementInputError("permissions", "invalid_choice")
	}
	for index, row := range permissionRows {
		if row.ID != permissions[index] {
			return managementInputError("permissions", "invalid_choice")
		}
	}
	return nil
}

func (manager *Manager) validateEffectiveGrants(ctx context.Context, reader db.Queryer, groups, permissions []int64) error {
	relations := manager.state.directory.state.relations
	objects, err := models.PermissionObjects.Using(reader).Filter(orm.Or(models.PermissionFields.ID.In(permissions...), relations.IdentityPermission.Groups.ID.In(groups...))).Distinct().OrderBy(models.PermissionFields.ID.Asc()).Limit(auth.MaximumPermissions + 1)
	if err != nil {
		return err
	}
	rows, err := objects.All(ctx)
	if err != nil {
		return err
	}
	if len(rows) > auth.MaximumPermissions {
		return managementInputError("permissions", "max_items")
	}
	for _, row := range rows {
		if row.Revision <= 0 {
			return managementError(CodePersistence, "user", nil)
		}
		if _, err := auth.NewPermission(row.Code); err != nil {
			return managementError(CodePersistence, "user", err)
		}
	}
	return nil
}

func (manager *Manager) setUserKeys(ctx context.Context, session db.RelationSession, user models.User, groups, permissions []int64, changeGroups, changePermissions bool) error {
	if changeGroups {
		collection, err := manager.state.collections.IdentityUserGroups.InSession(session, user)
		if err != nil {
			return err
		}
		if err := collection.SetKeys(ctx, groups); err != nil {
			return err
		}
	}
	if changePermissions {
		collection, err := manager.state.collections.IdentityUserPermissions.InSession(session, user)
		if err != nil {
			return err
		}
		if err := collection.SetKeys(ctx, permissions); err != nil {
			return err
		}
	}
	return nil
}

func (manager *Manager) auditUser(ctx context.Context, session db.Session, actorID string, id int64, action admin.Action, fields []string) error {
	event, err := admin.PrepareEvent(actorID, "godj_identity.user", id, action, fields, "")
	if err != nil {
		return err
	}
	return manager.state.backend.AppendAudit(ctx, session, event)
}
