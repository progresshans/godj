package identity

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
)

func (manager *Manager) Permission(ctx context.Context, actor auth.Principal, id int64) (PermissionProfile, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return PermissionProfile{}, err
	}
	if id <= 0 {
		return PermissionProfile{}, managementError(CodeInvalidInput, "permission", nil)
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) (PermissionProfile, error) {
		if err := manager.requireView(ctx, reader, actor.ID(), ViewPermission, ChangePermission); err != nil {
			return PermissionProfile{}, err
		}
		row, err := manager.permissionRow(ctx, reader, id)
		if err != nil {
			return PermissionProfile{}, err
		}
		return permissionProfile(row)
	})
}

// Permissions returns one bounded scalar page in primary-key order. Permission
// assignment details belong to User and Group rather than an unbounded list.
func (manager *Manager) Permissions(ctx context.Context, actor auth.Principal, offset, limit int) (PermissionPage, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return PermissionPage{}, err
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return PermissionPage{}, managementError(CodeInvalidInput, "permission", nil)
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) (PermissionPage, error) {
		if err := manager.requireView(ctx, reader, actor.ID(), ViewPermission, ChangePermission); err != nil {
			return PermissionPage{}, err
		}
		objects := models.PermissionObjects.Using(reader).OrderBy(models.PermissionFields.ID.Asc())
		count, err := objects.Count(ctx)
		if err != nil {
			return PermissionPage{}, err
		}
		page, err := objects.Offset(offset)
		if err != nil {
			return PermissionPage{}, err
		}
		page, err = page.Limit(limit)
		if err != nil {
			return PermissionPage{}, err
		}
		rows, err := page.All(ctx)
		if err != nil {
			return PermissionPage{}, err
		}
		result := PermissionPage{Permissions: make([]PermissionProfile, len(rows)), Total: count}
		for index, row := range rows {
			result.Permissions[index], err = permissionProfile(row)
			if err != nil {
				return PermissionPage{}, err
			}
		}
		return result, nil
	})
}

// CreatePermission requires the current add_permission grant, matching the
// explicitly registered Django ModelAdmin. It performs no password work.
func (manager *Manager) CreatePermission(ctx context.Context, actor auth.Principal, input PermissionCreate) (PermissionProfile, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return PermissionProfile{}, err
	}
	patch, err := input.patch.normalize()
	if err != nil {
		return PermissionProfile{}, err
	}
	code, hasCode := patch.code.Get()
	name, hasName := patch.name.Get()
	if !hasCode {
		return PermissionProfile{}, managementInputError("code", "required")
	}
	if !hasName {
		return PermissionProfile{}, managementInputError("name", "required")
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (PermissionProfile, error) {
		if err := manager.requireActor(ctx, session, actor.ID(), AddPermission); err != nil {
			return PermissionProfile{}, err
		}
		create := models.NewPermissionCreate(code, name)
		violations, err := models.PermissionObjects.ValidateUniqueCreate(ctx, session, create)
		if err != nil {
			return PermissionProfile{}, err
		}
		if !violations.Empty() {
			return PermissionProfile{}, validation.Reject(violations, nil)
		}
		row, err := models.PermissionObjects.Create(ctx, session, create)
		if err != nil {
			return PermissionProfile{}, err
		}
		result, err := permissionProfile(row)
		if err != nil {
			return PermissionProfile{}, err
		}
		if err := manager.auditCatalog(ctx, session, actor.ID(), "permission", row.ID, admin.ActionAdd, []string{"code", "name"}); err != nil {
			return PermissionProfile{}, err
		}
		return result, nil
	})
}

// UpdatePermission preserves assignment keys. A code change is visible to the
// next current credential resolution; it does not change password/session stamps.
func (manager *Manager) UpdatePermission(ctx context.Context, actor auth.Principal, id, revision int64, input PermissionPatch) (PermissionProfile, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return PermissionProfile{}, err
	}
	if err := validCatalogRevision(id, revision, "permission"); err != nil {
		return PermissionProfile{}, err
	}
	input, err := input.normalize()
	if err != nil {
		return PermissionProfile{}, err
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (PermissionProfile, error) {
		if err := manager.requireActor(ctx, session, actor.ID(), ChangePermission); err != nil {
			return PermissionProfile{}, err
		}
		row, err := manager.permissionRow(ctx, session, id)
		if err != nil {
			return PermissionProfile{}, err
		}
		if row.Revision != revision {
			return PermissionProfile{}, managementError(CodeConflict, "permission", nil)
		}
		patch, fields := models.PermissionPatch{}, []string{}
		if code, set := input.code.Get(); set && code != row.Code {
			patch = patch.WithCode(code)
			fields = append(fields, "code")
		}
		if name, set := input.name.Get(); set && name != row.Name {
			patch = patch.WithName(name)
			fields = append(fields, "name")
		}
		if len(fields) == 0 {
			return permissionProfile(row)
		}
		patch = patch.WithRevision(revision + 1)
		violations, err := models.PermissionObjects.ValidateUniqueUpdate(ctx, session, row, patch)
		if err != nil {
			return PermissionProfile{}, err
		}
		if !violations.Empty() {
			return PermissionProfile{}, validation.Reject(violations, nil)
		}
		updated, err := models.PermissionObjects.Patch(ctx, session, row, patch)
		if err != nil {
			return PermissionProfile{}, err
		}
		result, err := permissionProfile(updated)
		if err != nil {
			return PermissionProfile{}, err
		}
		if err := manager.auditCatalog(ctx, session, actor.ID(), "permission", id, admin.ActionChange, fields); err != nil {
			return PermissionProfile{}, err
		}
		return result, nil
	})
}

// DeletePermission uses the host's complete relation policy. Membership and
// incoming host rows plus the audit share one transaction, with no retry.
func (manager *Manager) DeletePermission(ctx context.Context, actor auth.Principal, id, revision int64, policy orm.RelationDeleter[models.Permission]) (CatalogDeletion, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return CatalogDeletion{}, err
	}
	if err := validCatalogRevision(id, revision, "permission"); err != nil {
		return CatalogDeletion{}, err
	}
	return managementRelationWrite(ctx, manager, func(session db.RelationSession) (CatalogDeletion, error) {
		if err := manager.requireActor(ctx, session, actor.ID(), DeletePermission); err != nil {
			return CatalogDeletion{}, err
		}
		row, err := manager.permissionRow(ctx, session, id)
		if err != nil {
			return CatalogDeletion{}, err
		}
		if row.Revision != revision {
			return CatalogDeletion{}, managementError(CodeConflict, "permission", nil)
		}
		if err := manager.advancePermissionOwners(ctx, session, id); err != nil {
			return CatalogDeletion{}, err
		}
		deleted, err := policy.DeleteInSession(ctx, session, row)
		if err != nil {
			return CatalogDeletion{}, err
		}
		if err := manager.auditCatalog(ctx, session, actor.ID(), "permission", id, admin.ActionDelete, nil); err != nil {
			return CatalogDeletion{}, err
		}
		return CatalogDeletion{ID: row.ID, Revision: row.Revision, DeletedRows: deleted}, nil
	})
}

func (manager *Manager) permissionRow(ctx context.Context, reader db.Queryer, id int64) (models.Permission, error) {
	row, found, err := models.PermissionObjects.Using(reader).Filter(models.PermissionFields.ID.Exact(id)).OrderBy(models.PermissionFields.ID.Asc()).First(ctx)
	if err != nil {
		return models.Permission{}, err
	}
	if !found {
		return models.Permission{}, managementError(CodeNotFound, "permission", nil)
	}
	if _, err := permissionProfile(row); err != nil {
		return models.Permission{}, err
	}
	return row, nil
}
