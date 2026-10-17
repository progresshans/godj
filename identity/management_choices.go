package identity

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
)

// MaximumManagementChoices bounds one complete administrative selection
// snapshot. An oversized catalog is an explicit error, never a truncated list
// that could silently drop a stored selection when the form is saved.
const MaximumManagementChoices = 4096

type ManagementChoice struct {
	ID    int64
	Label string
}

// UserGroupChoices and UserPermissionChoices authorize assigning relations to
// a user. They deliberately require change_user, not target-model view grants.
// Membership and current authority are checked again by UpdateUser's fence.
func (manager *Manager) UserGroupChoices(ctx context.Context, actor auth.Principal) ([]ManagementChoice, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return nil, err
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) ([]ManagementChoice, error) {
		if err := manager.requireActor(ctx, reader, actor.ID(), ChangeUser); err != nil {
			return nil, err
		}
		objects, err := models.GroupObjects.Using(reader).OrderBy(models.GroupFields.ID.Asc()).Limit(MaximumManagementChoices + 1)
		if err != nil {
			return nil, err
		}
		rows, err := objects.All(ctx)
		if err != nil {
			return nil, managementError(CodePersistence, "group", err)
		}
		if len(rows) > MaximumManagementChoices {
			return nil, managementError(CodePersistence, "group", nil)
		}
		result := make([]ManagementChoice, len(rows))
		for i, row := range rows {
			profile, err := groupProfile(row)
			if err != nil {
				return nil, err
			}
			result[i] = ManagementChoice{ID: profile.ID, Label: profile.Name}
		}
		return result, nil
	})
}

func (manager *Manager) UserPermissionChoices(ctx context.Context, actor auth.Principal) ([]ManagementChoice, error) {
	return manager.permissionChoices(ctx, actor, ChangeUser)
}

// GroupPermissionChoices admits the requested form action. Add-only actors
// can assign permissions during group creation; change-only actors can edit.
func (manager *Manager) GroupPermissionChoices(ctx context.Context, actor auth.Principal, action admin.Action) ([]ManagementChoice, error) {
	permission := AddGroup
	switch action {
	case admin.ActionAdd:
	case admin.ActionChange:
		permission = ChangeGroup
	default:
		return nil, managementError(CodeInvalidInput, "group", nil)
	}
	return manager.permissionChoices(ctx, actor, permission)
}

func (manager *Manager) permissionChoices(ctx context.Context, actor auth.Principal, permission auth.Permission) ([]ManagementChoice, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return nil, err
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) ([]ManagementChoice, error) {
		if err := manager.requireActor(ctx, reader, actor.ID(), permission); err != nil {
			return nil, err
		}
		objects, err := models.PermissionObjects.Using(reader).OrderBy(models.PermissionFields.ID.Asc()).Limit(MaximumManagementChoices + 1)
		if err != nil {
			return nil, err
		}
		rows, err := objects.All(ctx)
		if err != nil {
			return nil, managementError(CodePersistence, "permission", err)
		}
		if len(rows) > MaximumManagementChoices {
			return nil, managementError(CodePersistence, "permission", nil)
		}
		result := make([]ManagementChoice, len(rows))
		for i, row := range rows {
			profile, err := permissionProfile(row)
			if err != nil {
				return nil, err
			}
			result[i] = ManagementChoice{ID: profile.ID, Label: profile.Code + " — " + profile.Name}
		}
		return result, nil
	})
}
