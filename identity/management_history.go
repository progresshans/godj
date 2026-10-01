package identity

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
)

// ManagementHistoryReader reads through the manager-owned snapshot. It must
// not acquire another transaction, replace the reader, or cache observations.
// It is an optional backend capability used by administrative history.
type ManagementHistoryReader interface {
	AuditHistoryInSnapshot(context.Context, db.Queryer, string, int64, int) ([]admin.AuditEntry, error)
}

func (manager *Manager) UserHistory(ctx context.Context, actor auth.Principal, id int64, limit int) ([]admin.AuditEntry, error) {
	return manager.history(ctx, actor, "user", id, limit, ViewUser, ChangeUser, func(reader db.Queryer) (bool, error) {
		return models.UserObjects.Using(reader).Filter(models.UserFields.ID.Exact(id)).Exists(ctx)
	})
}

func (manager *Manager) GroupHistory(ctx context.Context, actor auth.Principal, id int64, limit int) ([]admin.AuditEntry, error) {
	return manager.history(ctx, actor, "group", id, limit, ViewGroup, ChangeGroup, func(reader db.Queryer) (bool, error) {
		return models.GroupObjects.Using(reader).Filter(models.GroupFields.ID.Exact(id)).Exists(ctx)
	})
}

func (manager *Manager) PermissionHistory(ctx context.Context, actor auth.Principal, id int64, limit int) ([]admin.AuditEntry, error) {
	return manager.history(ctx, actor, "permission", id, limit, ViewPermission, ChangePermission, func(reader db.Queryer) (bool, error) {
		return models.PermissionObjects.Using(reader).Filter(models.PermissionFields.ID.Exact(id)).Exists(ctx)
	})
}

func (manager *Manager) history(ctx context.Context, actor auth.Principal, model string, id int64, limit int, view, change auth.Permission, exists func(db.Queryer) (bool, error)) ([]admin.AuditEntry, error) {
	if err := manager.validCall(ctx, actor); err != nil {
		return nil, err
	}
	if id <= 0 || limit < 1 || limit > admin.MaximumHistoryEntries {
		return nil, managementError(CodeInvalidInput, model, nil)
	}
	history, ok := manager.state.backend.(ManagementHistoryReader)
	if !ok || nilIdentityValue(history) {
		return nil, managementError(CodeInvalidConfig, "manager", nil)
	}
	return managementSnapshot(ctx, manager, func(reader db.Queryer) ([]admin.AuditEntry, error) {
		if err := manager.requireView(ctx, reader, actor.ID(), view, change); err != nil {
			return nil, err
		}
		found, err := exists(reader)
		if err != nil {
			return nil, managementError(CodePersistence, model, err)
		}
		if !found {
			return nil, managementError(CodeNotFound, model, nil)
		}
		entries, err := history.AuditHistoryInSnapshot(ctx, reader, "godj_identity."+model, id, limit)
		if err != nil {
			return nil, managementError(CodePersistence, model, err)
		}
		if len(entries) > limit {
			return nil, managementError(CodePersistence, model, nil)
		}
		result := make([]admin.AuditEntry, len(entries))
		var previous uint64
		for i, entry := range entries {
			if entry.Model != "godj_identity."+model || entry.ObjectID != id || entry.Sequence <= previous {
				return nil, managementError(CodePersistence, model, nil)
			}
			prepared, err := admin.PrepareEvent(entry.ActorID, entry.Model, entry.ObjectID, entry.Action, entry.ChangedFields, entry.DisplayLabel)
			if err != nil {
				return nil, managementError(CodePersistence, model, err)
			}
			result[i] = entry
			result[i].ChangedFields = prepared.ChangedFields()
			previous = entry.Sequence
		}
		return result, nil
	})
}
