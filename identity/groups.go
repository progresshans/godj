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

func (manager *Manager) Group(ctx context.Context, actor auth.Principal, id int64) (GroupDetails, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return GroupDetails{}, err
	}
	if id <= 0 {
		return GroupDetails{}, managementError(CodeInvalidInput, "group", nil)
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) (GroupDetails, error) {
		if err := manager.requireView(ctx, reader, actor.ID(), ViewGroup, ChangeGroup); err != nil {
			return GroupDetails{}, err
		}
		return manager.groupDetails(ctx, reader, id)
	})
}

// Groups returns a bounded scalar page without fetching each group's members
// or permissions. Group owns the separately authorized selection detail.
func (manager *Manager) Groups(ctx context.Context, actor auth.Principal, offset, limit int) (GroupPage, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return GroupPage{}, err
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return GroupPage{}, managementError(CodeInvalidInput, "group", nil)
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) (GroupPage, error) {
		if err := manager.requireView(ctx, reader, actor.ID(), ViewGroup, ChangeGroup); err != nil {
			return GroupPage{}, err
		}
		objects := models.GroupObjects.Using(reader).OrderBy(models.GroupFields.ID.Asc())
		count, err := objects.Count(ctx)
		if err != nil {
			return GroupPage{}, err
		}
		page, err := objects.Offset(offset)
		if err != nil {
			return GroupPage{}, err
		}
		page, err = page.Limit(limit)
		if err != nil {
			return GroupPage{}, err
		}
		rows, err := page.All(ctx)
		if err != nil {
			return GroupPage{}, err
		}
		result := GroupPage{Groups: make([]GroupProfile, len(rows)), Total: count}
		for index, row := range rows {
			result.Groups[index], err = groupProfile(row)
			if err != nil {
				return GroupPage{}, err
			}
		}
		return result, nil
	})
}

func (manager *Manager) CreateGroup(ctx context.Context, actor auth.Principal, input GroupCreate) (GroupDetails, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return GroupDetails{}, err
	}
	patch, err := input.patch.normalize()
	if err != nil {
		return GroupDetails{}, err
	}
	name, set := patch.name.Get()
	if !set {
		return GroupDetails{}, managementInputError("name", "required")
	}
	permissions, _ := patch.permissions.Get()
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (GroupDetails, error) {
		if err := manager.requireActor(ctx, session, actor.ID(), AddGroup); err != nil {
			return GroupDetails{}, err
		}
		if err := manager.validateGroupPermissions(ctx, session, permissions); err != nil {
			return GroupDetails{}, err
		}
		create := models.NewGroupCreate(name)
		violations, err := models.GroupObjects.ValidateUniqueCreate(ctx, session, create)
		if err != nil {
			return GroupDetails{}, err
		}
		if !violations.Empty() {
			return GroupDetails{}, validation.Reject(violations, nil)
		}
		row, err := models.GroupObjects.Create(ctx, session, create)
		if err != nil {
			return GroupDetails{}, err
		}
		if err := manager.setGroupPermissions(ctx, session, row, permissions); err != nil {
			return GroupDetails{}, err
		}
		result, err := manager.groupDetails(ctx, session, row.ID)
		if err != nil {
			return GroupDetails{}, err
		}
		if err := manager.auditCatalog(ctx, session, actor.ID(), "group", row.ID, admin.ActionAdd, []string{"name", "permissions"}); err != nil {
			return GroupDetails{}, err
		}
		return result, nil
	})
}

// UpdateGroup changes name and the complete permission selection atomically.
// Every affected user's proposed effective union must remain representable.
// User/password revisions and session stamps are not rewritten by grant changes.
func (manager *Manager) UpdateGroup(ctx context.Context, actor auth.Principal, id, revision int64, input GroupPatch) (GroupDetails, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return GroupDetails{}, err
	}
	if err := validCatalogRevision(id, revision, "group"); err != nil {
		return GroupDetails{}, err
	}
	input, err := input.normalize()
	if err != nil {
		return GroupDetails{}, err
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (GroupDetails, error) {
		if err := manager.requireActor(ctx, session, actor.ID(), ChangeGroup); err != nil {
			return GroupDetails{}, err
		}
		row, err := manager.groupRow(ctx, session, id)
		if err != nil {
			return GroupDetails{}, err
		}
		if row.Revision != revision {
			return GroupDetails{}, managementError(CodeConflict, "group", nil)
		}
		before, err := manager.groupDetailsFromRow(ctx, session, row)
		if err != nil {
			return GroupDetails{}, err
		}
		patch, fields := models.GroupPatch{}, []string{}
		if name, set := input.name.Get(); set && name != row.Name {
			patch = patch.WithName(name)
			fields = append(fields, "name")
		}
		permissions, changePermissions := input.permissions.Get()
		changePermissions = changePermissions && !slices.Equal(permissions, before.PermissionIDs)
		if changePermissions {
			if err := manager.validateGroupPermissions(ctx, session, permissions); err != nil {
				return GroupDetails{}, err
			}
			if err := manager.validateGroupMemberGrants(ctx, session, id, permissions); err != nil {
				return GroupDetails{}, err
			}
			fields = append(fields, "permissions")
		}
		if len(fields) == 0 {
			return before, nil
		}
		patch = patch.WithRevision(revision + 1)
		violations, err := models.GroupObjects.ValidateUniqueUpdate(ctx, session, row, patch)
		if err != nil {
			return GroupDetails{}, err
		}
		if !violations.Empty() {
			return GroupDetails{}, validation.Reject(violations, nil)
		}
		updated, err := models.GroupObjects.Patch(ctx, session, row, patch)
		if err != nil {
			return GroupDetails{}, err
		}
		if changePermissions {
			if err := manager.setGroupPermissions(ctx, session, updated, permissions); err != nil {
				return GroupDetails{}, err
			}
		}
		result, err := manager.groupDetailsFromRow(ctx, session, updated)
		if err != nil {
			return GroupDetails{}, err
		}
		if err := manager.auditCatalog(ctx, session, actor.ID(), "group", id, admin.ActionChange, fields); err != nil {
			return GroupDetails{}, err
		}
		return result, nil
	})
}

func (manager *Manager) DeleteGroup(ctx context.Context, actor auth.Principal, id, revision int64, policy orm.RelationDeleter[models.Group]) (CatalogDeletion, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return CatalogDeletion{}, err
	}
	if err := validCatalogRevision(id, revision, "group"); err != nil {
		return CatalogDeletion{}, err
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (CatalogDeletion, error) {
		if err := manager.requireActor(ctx, session, actor.ID(), DeleteGroup); err != nil {
			return CatalogDeletion{}, err
		}
		row, err := manager.groupRow(ctx, session, id)
		if err != nil {
			return CatalogDeletion{}, err
		}
		if row.Revision != revision {
			return CatalogDeletion{}, managementError(CodeConflict, "group", nil)
		}
		if err := manager.advanceUserOwners(ctx, session, manager.state.directory.state.relations.IdentityUser.Groups.ID.Exact(id)); err != nil {
			return CatalogDeletion{}, err
		}
		deleted, err := policy.DeleteInSession(ctx, session, row)
		if err != nil {
			return CatalogDeletion{}, err
		}
		if err := manager.auditCatalog(ctx, session, actor.ID(), "group", id, admin.ActionDelete, nil); err != nil {
			return CatalogDeletion{}, err
		}
		return CatalogDeletion{ID: row.ID, Revision: row.Revision, DeletedRows: deleted}, nil
	})
}

func (manager *Manager) groupRow(ctx context.Context, reader db.Queryer, id int64) (models.Group, error) {
	row, found, err := models.GroupObjects.Using(reader).Filter(models.GroupFields.ID.Exact(id)).OrderBy(models.GroupFields.ID.Asc()).First(ctx)
	if err != nil {
		return models.Group{}, err
	}
	if !found {
		return models.Group{}, managementError(CodeNotFound, "group", nil)
	}
	if _, err := groupProfile(row); err != nil {
		return models.Group{}, err
	}
	return row, nil
}

func (manager *Manager) groupDetails(ctx context.Context, reader db.Queryer, id int64) (GroupDetails, error) {
	row, err := manager.groupRow(ctx, reader, id)
	if err != nil {
		return GroupDetails{}, err
	}
	return manager.groupDetailsFromRow(ctx, reader, row)
}

func (manager *Manager) groupDetailsFromRow(ctx context.Context, reader db.Queryer, row models.Group) (GroupDetails, error) {
	profile, err := groupProfile(row)
	if err != nil {
		return GroupDetails{}, err
	}
	relations := manager.state.directory.state.relations
	objects, err := models.PermissionObjects.Using(reader).Filter(relations.IdentityPermission.Groups.ID.Exact(row.ID)).OrderBy(models.PermissionFields.ID.Asc()).Limit(auth.MaximumPermissions + 1)
	if err != nil {
		return GroupDetails{}, err
	}
	permissions, err := objects.All(ctx)
	if err != nil {
		return GroupDetails{}, err
	}
	if len(permissions) > auth.MaximumPermissions {
		return GroupDetails{}, managementError(CodePersistence, "group", nil)
	}
	result := GroupDetails{GroupProfile: profile, PermissionIDs: make([]int64, len(permissions))}
	for index, permission := range permissions {
		if _, err := permissionProfile(permission); err != nil {
			return GroupDetails{}, err
		}
		result.PermissionIDs[index] = permission.ID
	}
	return result, nil
}

func (manager *Manager) validateGroupPermissions(ctx context.Context, reader db.Queryer, permissions []int64) error {
	failures, err := manager.groupPermissionViolations(ctx, reader, permissions)
	if err != nil {
		return err
	}
	return validation.Reject(failures, nil)
}

func (manager *Manager) groupPermissionViolations(ctx context.Context, reader db.Queryer, permissions []int64) (validation.Errors, error) {
	rows, err := models.PermissionObjects.Using(reader).Filter(models.PermissionFields.ID.In(permissions...)).OrderBy(models.PermissionFields.ID.Asc()).All(ctx)
	if err != nil {
		return validation.Errors{}, err
	}
	if len(rows) != len(permissions) {
		return validation.NewErrors(validation.New("permissions", "invalid_choice")), nil
	}
	for index, row := range rows {
		if row.ID != permissions[index] {
			return validation.NewErrors(validation.New("permissions", "invalid_choice")), nil
		}
		if _, err := permissionProfile(row); err != nil {
			return validation.Errors{}, err
		}
	}
	return validation.Errors{}, nil
}

func (manager *Manager) setGroupPermissions(ctx context.Context, session db.RelationSession, row models.Group, permissions []int64) error {
	collection, err := manager.state.collections.IdentityGroupPermissions.InSession(session, row)
	if err != nil {
		return err
	}
	return collection.SetKeys(ctx, permissions)
}

func (manager *Manager) validateGroupMemberGrants(ctx context.Context, reader db.Queryer, id int64, permissions []int64) error {
	relations := manager.state.directory.state.relations
	objects := models.UserObjects.Using(reader).Filter(relations.IdentityUser.Groups.ID.Exact(id))
	return walkCatalogUsers(ctx, objects, func(user models.User) error {
		keys, err := manager.userKeys(ctx, reader, user.ID)
		if err != nil {
			return err
		}
		at, present := slices.BinarySearch(keys.groups, id)
		if !present {
			return managementError(CodePersistence, "group", nil)
		}
		others := append(keys.groups[:at:at], keys.groups[at+1:]...)
		proposed := append(keys.permissions, permissions...)
		return manager.validateEffectiveGrants(ctx, reader, others, proposed)
	})
}
