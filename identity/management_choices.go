package identity

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

// UserGroupChoiceSource declares bounded group selection for editing a user.
// Assignment deliberately requires current change_user, not view_group. Every
// Search/Resolve rereads that authority in the same borrowed read snapshot as
// the target query; the final UpdateUser fence still owns write authority.
func (manager *Manager) UserGroupChoiceSource() (admin.RelatedChoices, error) {
	return managementChoiceSource(manager, "user", "groups", ChangeUser, models.GroupObjects, formmodel.ModelChoiceConfig[models.Group]{
		Label: func(row models.Group) (string, error) {
			profile, err := groupProfile(row)
			return profile.Name, err
		},
		Search: func(term string) ([]orm.Predicate[models.Group], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Group]{models.GroupFields.Name.IContains(term)}, nil
		},
		Order: []orm.Ordering[models.Group]{models.GroupFields.ID.Asc()},
	})
}

func (manager *Manager) UserPermissionChoiceSource() (admin.RelatedChoices, error) {
	return manager.permissionChoiceSource("user", ChangeUser)
}

// GroupPermissionChoiceSource keeps creation and editing admission distinct:
// add-only actors can assign permissions when creating a group.
func (manager *Manager) GroupPermissionChoiceSource(action admin.Action) (admin.RelatedChoices, error) {
	permission := AddGroup
	switch action {
	case admin.ActionAdd:
	case admin.ActionChange:
		permission = ChangeGroup
	default:
		return nil, managementError(CodeInvalidInput, "group", nil)
	}
	return manager.permissionChoiceSource("group", permission)
}

func (manager *Manager) permissionChoiceSource(source string, permission auth.Permission) (admin.RelatedChoices, error) {
	return managementChoiceSource(manager, source, "permissions", permission, models.PermissionObjects, formmodel.ModelChoiceConfig[models.Permission]{
		Label: func(row models.Permission) (string, error) {
			profile, err := permissionProfile(row)
			if err != nil {
				return "", err
			}
			return profile.Code + " — " + profile.Name, nil
		},
		Search: func(term string) ([]orm.Predicate[models.Permission], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Permission]{orm.Or(models.PermissionFields.Code.IContains(term), models.PermissionFields.Name.IContains(term))}, nil
		},
		Order: []orm.Ordering[models.Permission]{models.PermissionFields.ID.Asc()},
	})
}

func managementChoiceSource[M any](manager *Manager, source, field string, permission auth.Permission, target orm.Manager[M], config formmodel.ModelChoiceConfig[M]) (admin.RelatedChoices, error) {
	if manager == nil || manager.state == nil {
		return nil, managementError(CodeInvalidConfig, "manager", nil)
	}
	binding, err := project.Bind()
	if err != nil {
		return nil, err
	}
	choices, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "godj_identity", ModelName: source}, field, target, config)
	if err != nil {
		return nil, err
	}
	return admin.NewRelatedChoices(admin.RelatedChoiceConfig[M]{
		Choices: choices, Permission: permission,
		Scope: func(ctx context.Context, actor auth.Principal, _ admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[M]) error) error {
			if err := manager.validCall(ctx, actor); err != nil {
				return choiceAdmissionError(err)
			}
			_, err := managementSnapshot(ctx, manager, func(reader db.Queryer) (struct{}, error) {
				if err := manager.requireActor(ctx, reader, actor.ID(), permission); err != nil {
					return struct{}{}, err
				}
				return struct{}{}, visit(reader, nil)
			})
			return choiceAdmissionError(err)
		},
	})
}

// Choice sources are Admin adapters. Preserve execution/cleanup/cancellation
// errors while presenting only the manager's direct permission denial as 403.
func choiceAdmissionError(err error) error {
	if failure, ok := err.(*Error); ok && failure != nil && failure.Code == CodePermission {
		return admin.NewOperationError(admin.OperationDenied, err)
	}
	return err
}
