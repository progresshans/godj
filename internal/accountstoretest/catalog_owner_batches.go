package accountstoretest

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
)

func catalogPermissionOwnerBatches(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	permission, err := f.catalog.Permissions().Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("batch.shared"), "name": query.String("Shared grant")}))
	if err != nil {
		t.Fatal(err)
	}
	users, groups := make([]models.User, 257), make([]models.Group, 257)
	userIDs, groupIDs := make(map[int64]bool), make(map[int64]bool)
	var indirect models.User
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for index := range users {
			name := fmt.Sprintf("permission-owner-%03d", index)
			groups[index], err = models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate(name))
			if err != nil {
				return err
			}
			users[index], err = models.UserObjects.Create(t.Context(), scope, models.NewUserCreate(name, name, f.account.user.EncodedPassword, joined))
			if err != nil {
				return err
			}
			userIDs[users[index].ID], groupIDs[groups[index].ID] = true, true
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), scope, models.NewGroupPermissionsLinkCreate(groups[index].ID, permission.ID)); err != nil {
				return err
			}
			if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(users[index].ID, permission.ID)); err != nil {
				return err
			}
		}
		indirect, err = models.UserObjects.Create(t.Context(), scope, models.NewUserCreate("indirect-owner", "indirect-owner", f.account.user.EncodedPassword, joined))
		if err != nil {
			return err
		}
		_, err = models.UserGroupsLinkObjects.Create(t.Context(), scope, models.NewUserGroupsLinkCreate(indirect.ID, groups[len(groups)-1].ID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now := joined
	_, manager, _ := accountSessions(t, backend, &now)
	observed := make(map[string]auth.Credential)
	for _, user := range []models.User{f.account.user, users[0], users[len(users)-1], indirect} {
		credential, present, err := f.account.store.CredentialByID(t.Context(), user.PrincipalID)
		if err != nil || !present {
			t.Fatal("owner credential setup", err)
		}
		observed[user.PrincipalID] = credential
		passwordSession(t, manager, credential, "preserve-"+user.PrincipalID)
	}
	last := groups[len(groups)-1]
	if _, err := models.GroupObjects.Patch(t.Context(), backend, last, models.GroupPatch{}.WithRevision(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if !t.Run("late_owner_overflow", func(t *testing.T) {
		before := accountNativeSnapshot(t, reopen(t))
		result, err := f.catalog.Permissions().Delete(t.Context(), actor, permission.ID, 1)
		requireProfileCode(t, err, accountmodel.CodePersistence)
		if result != (accountmodel.CatalogDeletion{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("late group overflow preserved an earlier owner update or partial deletion")
		}
	}) {
		return
	}
	if _, err := models.GroupObjects.Patch(t.Context(), backend, last, models.GroupPatch{}.WithRevision(1)); err != nil {
		t.Fatal(err)
	}
	if !t.Run("complete_direct_owners", func(t *testing.T) {
		expected := accountNativeSnapshot(t, reopen(t))
		for index := range expected.users {
			if userIDs[expected.users[index].ID] {
				expected.users[index].Revision++
			}
		}
		for index := range expected.groups {
			if groupIDs[expected.groups[index].ID] {
				expected.groups[index].Revision++
			}
		}
		expected.permissions = slices.DeleteFunc(expected.permissions, func(row models.Permission) bool { return row.ID == permission.ID })
		expected.direct = slices.DeleteFunc(expected.direct, func(row models.UserPermissionsLink) bool { return row.TargetID == permission.ID })
		expected.grants = slices.DeleteFunc(expected.grants, func(row models.GroupPermissionsLink) bool { return row.TargetID == permission.ID })
		result, err := f.catalog.Permissions().Delete(t.Context(), actor, permission.ID, 1)
		if err != nil || result.ID != permission.ID || result.Revision != 1 || result.DeletedRows != 515 || result.RevokedSessions != 0 {
			t.Fatal("permission deletion omitted a direct owner batch", result, err)
		}
		assertCatalogState(t, reopen(t), expected, actor, catalogExpectedEvent{"permission", permission.ID, admin.ActionDelete, nil})
		for id, before := range observed {
			credential, present, err := f.account.store.CredentialByID(t.Context(), id)
			if err != nil || !present || !credential.MatchesSessionStamp(before.SessionStamp()) {
				t.Fatal("permission deletion changed an owner's credential stamp", err)
			}
			if id == actor.ID() {
				if !slices.Equal(credential.Principal().Permissions(), before.Principal().Permissions()) {
					t.Fatal("unrelated actor grants changed")
				}
			} else if len(credential.Principal().Permissions()) != 0 {
				t.Fatal("reopened direct or indirect owner retained the deleted grant")
			}
		}
	}) {
		return
	}
	t.Run("stale_owner_revisions", func(t *testing.T) {
		before := accountNativeSnapshot(t, reopen(t))
		editor, err := project.NewAccountEditor(catalogSessions(t, reopen(t)), auth.PrincipalAuthorizer{}, accountEditConfig())
		if err != nil {
			t.Fatal(err)
		}
		user, err := editor.Update(t.Context(), actor, users[0].ID, 1, profileInput(t, map[string]query.Value{"first_name": query.String("stale")}))
		requireProfileCode(t, err, accountmodel.CodeConflict)
		group, err := f.catalog.Groups().Update(t.Context(), actor, last.ID, 1, catalogGroupInput(t, map[string]query.Value{"name": query.String("stale")}, nil))
		requireProfileCode(t, err, accountmodel.CodeConflict)
		if !reflect.DeepEqual(user, accountmodel.AccountChange{}) || !reflect.DeepEqual(group, accountmodel.CatalogChange{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("catalog deletion left a stale owner writable")
		}
	})
}
