package identity

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CheckGroupCreate checks selected group values against current authority and
// storage without writing. Invalid selections do not suppress a valid name's
// uniqueness check. CreateGroup still owns the final authorized transaction.
func (manager *Manager) CheckGroupCreate(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
	return manager.checkGroupForm(ctx, actor, 0, 0, bound)
}

// CheckGroupChange binds the form to the server-observed group and revision.
// It does not reserve the name, retain permission grants or authorize a write.
func (manager *Manager) CheckGroupChange(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) error {
	if err := validCatalogRevision(id, revision, "group"); err != nil {
		return err
	}
	return manager.checkGroupForm(ctx, actor, id, revision, bound)
}

func (manager *Manager) checkGroupForm(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) error {
	if err := manager.validCall(ctx, actor); err != nil {
		return err
	}
	if err := checkCatalogFormCandidate(bound, models.GroupDescriptor{}.Metadata(), id, revision); err != nil {
		return err
	}
	permission := AddGroup
	if id != 0 {
		permission = ChangeGroup
	}
	_, err := managementSnapshot(ctx, manager, func(reader db.Queryer) (struct{}, error) {
		if err := manager.requireActor(ctx, reader, actor.ID(), permission); err != nil {
			return struct{}{}, err
		}
		var current *models.Group
		if id != 0 {
			row, err := manager.groupRow(ctx, reader, id)
			if err != nil {
				return struct{}{}, err
			}
			if row.Revision != revision {
				return struct{}{}, managementError(CodeConflict, "group", nil)
			}
			current = &row
		}
		var related validation.Errors
		if keys, present := bound.Form().Cleaned().Integers("permissions"); present {
			keys, err := normalizeIdentityKeys(keys, auth.MaximumPermissions, "permissions")
			if err != nil {
				// This is a pure input check, before any relation I/O.
				var rejected bool
				related, rejected = validation.Rejected(err)
				if !rejected {
					return struct{}{}, err
				}
			} else {
				related, err = manager.groupPermissionViolations(ctx, reader, keys)
				if err != nil {
					return struct{}{}, managementError(CodePersistence, "permissions", err)
				}
			}
		}
		checked, err := bound.WithErrors(related)
		if err != nil {
			return struct{}{}, err
		}
		failures, err := checkCatalogFormUnique(ctx, reader, models.GroupObjects, current, checked)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, validation.Reject(validation.Join(related, failures), nil)
	})
	return err
}

// CheckPermissionCreate performs the permission form's advisory DB checks in
// one current authorized read snapshot. CreatePermission rechecks its write.
func (manager *Manager) CheckPermissionCreate(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
	return manager.checkPermissionForm(ctx, actor, 0, 0, bound)
}

// CheckPermissionChange requires the current permission row and revision even
// when another input field has failed. It never changes grants or sessions.
func (manager *Manager) CheckPermissionChange(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) error {
	if err := validCatalogRevision(id, revision, "permission"); err != nil {
		return err
	}
	return manager.checkPermissionForm(ctx, actor, id, revision, bound)
}

func (manager *Manager) checkPermissionForm(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) error {
	if err := manager.validCall(ctx, actor); err != nil {
		return err
	}
	if err := checkCatalogFormCandidate(bound, models.PermissionDescriptor{}.Metadata(), id, revision); err != nil {
		return err
	}
	permission := AddPermission
	if id != 0 {
		permission = ChangePermission
	}
	_, err := managementSnapshot(ctx, manager, func(reader db.Queryer) (struct{}, error) {
		if err := manager.requireActor(ctx, reader, actor.ID(), permission); err != nil {
			return struct{}{}, err
		}
		var current *models.Permission
		if id != 0 {
			row, err := manager.permissionRow(ctx, reader, id)
			if err != nil {
				return struct{}{}, err
			}
			if row.Revision != revision {
				return struct{}{}, managementError(CodeConflict, "permission", nil)
			}
			current = &row
		}
		failures, err := checkCatalogFormUnique(ctx, reader, models.PermissionObjects, current, bound)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, validation.Reject(failures, nil)
	})
	return err
}

func checkCatalogFormCandidate(bound formmodel.BoundForm, model ir.Model, id, revision int64) error {
	if !bound.Form().Bound() || !reflect.DeepEqual(bound.Model(), model) {
		return managementError(CodeInvalidInput, model.Name+"_form", nil)
	}
	key, present := bound.Candidate().Get("id")
	version, versionPresent := bound.Candidate().Integer("revision")
	valid := present && versionPresent
	if id == 0 {
		valid = valid && key.IsNull() && version == 1
	} else {
		value, integer := key.AsInteger()
		valid = valid && integer && value == id && version == revision
	}
	if !valid {
		return managementError(CodeInvalidInput, model.Name+"_form", nil)
	}
	return nil
}

func checkCatalogFormUnique[M any](ctx context.Context, reader db.Queryer, manager orm.Manager[M], current *M, bound formmodel.BoundForm) (validation.Errors, error) {
	return bound.CheckDatabase(ctx, formmodel.DatabaseChecks{
		UniqueFields: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
			return manager.ValidateUniqueFields(ctx, reader, values, current)
		},
		Constraints: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
			return manager.ValidateUniqueConstraints(ctx, reader, values, current)
		},
	})
}
