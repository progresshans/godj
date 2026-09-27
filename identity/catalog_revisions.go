package identity

import (
	"context"
	"math"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
)

const catalogOwnerBatchSize = 256

// walkCatalogUsers consumes each rowset before the visitor performs nested
// reads/writes. The first page includes invalid nonpositive keys so corruption
// is rejected instead of silently excluding an affected account.
func walkCatalogUsers(ctx context.Context, objects orm.QuerySet[models.User], visit func(models.User) error) error {
	var lastID int64
	started := false
	for {
		page := objects.OrderBy(models.UserFields.ID.Asc())
		if started {
			page = page.Filter(models.UserFields.ID.GreaterThan(lastID))
		}
		bounded, err := page.Limit(catalogOwnerBatchSize)
		if err != nil {
			return err
		}
		users, err := bounded.All(ctx)
		if err != nil {
			return err
		}
		for _, user := range users {
			if started && user.ID <= lastID {
				return managementError(CodePersistence, "user", nil)
			}
			if err := validateManagedUserRow(user); err != nil {
				return err
			}
			if err := visit(user); err != nil {
				return err
			}
			lastID, started = user.ID, true
		}
		if len(users) < catalogOwnerBatchSize {
			return nil
		}
	}
}

func (manager *Manager) advanceUserOwners(ctx context.Context, session db.RelationSession, predicate orm.Predicate[models.User]) error {
	return walkCatalogUsers(ctx, models.UserObjects.Using(session).Filter(predicate), func(user models.User) error {
		if user.Revision == math.MaxInt64 {
			return managementError(CodePersistence, "user", nil)
		}
		_, err := models.UserObjects.Update(ctx, session, user, models.UserPatch{}.WithRevision(user.Revision+1))
		return err
	})
}

// Cascading removal changes the owner's direct collection representation.
// Advance that resource revision before deletion under the same write fence.
// Password/session stamps and indirectly inherited User grants have separate
// ownership; a changed Group permission list does not rewrite its members.
func (manager *Manager) advancePermissionOwners(ctx context.Context, session db.RelationSession, id int64) error {
	relations := manager.state.directory.state.relations
	if err := manager.advanceUserOwners(ctx, session, relations.IdentityUser.Permissions.ID.Exact(id)); err != nil {
		return err
	}
	objects := models.GroupObjects.Using(session).Filter(relations.IdentityGroup.Permissions.ID.Exact(id)).OrderBy(models.GroupFields.ID.Asc())
	var lastID int64
	started := false
	for {
		page := objects
		if started {
			page = page.Filter(models.GroupFields.ID.GreaterThan(lastID))
		}
		bounded, err := page.Limit(catalogOwnerBatchSize)
		if err != nil {
			return err
		}
		groups, err := bounded.All(ctx)
		if err != nil {
			return err
		}
		for _, group := range groups {
			if _, err := groupProfile(group); err != nil {
				return err
			}
			if group.Revision == math.MaxInt64 || started && group.ID <= lastID {
				return managementError(CodePersistence, "group", nil)
			}
			if _, err := models.GroupObjects.Update(ctx, session, group, models.GroupPatch{}.WithRevision(group.Revision+1)); err != nil {
				return err
			}
			lastID, started = group.ID, true
		}
		if len(groups) < catalogOwnerBatchSize {
			return nil
		}
	}
}
