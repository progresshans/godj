package accountstoretest

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func catalogAffectedBatches(t *testing.T, backend Backend, reopen func(*testing.T) Backend, storedKey StoredKeyFixture) {
	f := provisionCatalog(t, backend)
	actor, groups := f.credential.Principal(), f.catalog.Groups()
	group, err := groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("batch baseline")}, map[string][]int64{"permissions": {f.account.grants[0].ID}}))
	if err != nil {
		t.Fatal(err)
	}
	// The overflow/failing owner sorts strictly after the first 256 rows.
	// Explicit stored zero/negative owners are on that first page.
	members := make([]models.User, 257)
	for index, key := range []int64{0, -17} {
		members[index], err = models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(fmt.Sprintf("batch-signed-%d", index), fmt.Sprintf("batch-signed-%d", index), f.account.user.EncodedPassword, joined))
		if err != nil {
			t.Fatal(err)
		}
		storedKey(t, backend, "user", members[index].ID, key)
		(models.UserDescriptor{}).SetPrimaryKey(&members[index], key)
	}
	var extra models.Permission
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for index := range members {
			if index >= 2 {
				name := fmt.Sprintf("batch-%03d", index)
				members[index], err = models.UserObjects.Create(t.Context(), scope, models.NewUserCreate(name, name, f.account.user.EncodedPassword, joined))
				if err != nil {
					return err
				}
			}
			if _, err := models.UserGroupsLinkObjects.Create(t.Context(), scope, models.NewUserGroupsLinkCreate(members[index].ID, group.ID)); err != nil {
				return err
			}
		}
		late := members[len(members)-1]
		if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(late.ID, f.account.grants[0].ID)); err != nil {
			return err
		}
		for index := 1; index < 256; index++ {
			permission, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("boundary.p%03d", index), "Boundary"))
			if err != nil {
				return err
			}
			if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(late.ID, permission.ID)); err != nil {
				return err
			}
		}
		extra, err = models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate("boundary.extra", "Overflow"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	late := members[len(members)-1]
	if credential, present, err := f.account.store.CredentialByID(t.Context(), late.PrincipalID); err != nil || !present || len(credential.Principal().Permissions()) != 256 {
		t.Fatal("late-member positive bound control", err)
	}
	if !t.Run("permission_expansion_late_member", func(t *testing.T) {
		input := catalogGroupInput(t, map[string]query.Value{"name": query.String("must roll back")}, map[string][]int64{"permissions": {f.account.grants[0].ID, extra.ID}})
		result, err := groups.Update(t.Context(), actor, group.ID, 1, input)
		failures, rejected := validation.Rejected(err)
		if !rejected || !reflect.DeepEqual(failures.All(), validation.NewErrors(validation.New("permissions", "max_items")).All()) || result.Revision != 0 {
			t.Fatal("late-member overflow was not rejected", err)
		}
		current, err := groups.Get(t.Context(), actor, group.ID)
		if err != nil || current.Revision() != 1 {
			t.Fatal("late rejection changed group revision", err)
		}
		if value, _ := current.View().Get("name"); !value.Equal(query.String("batch baseline")) {
			t.Fatal("late rejection persisted scalar candidate")
		}
		if keys, _ := current.View().Collection("permissions"); !slices.Equal(keys, []int64{f.account.grants[0].ID}) {
			t.Fatal("late rejection persisted partial grants")
		}
		if entries := catalogAudit(t, reopen(t), actor, "group", group.ID); len(entries) != 1 {
			t.Fatal("late rejection appended audit")
		}
	}) {
		return
	}
	if _, err := models.UserObjects.Patch(t.Context(), backend, late, models.UserPatch{}.WithRevision(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(members))
	for index, member := range members {
		ids[index] = member.ID
	}
	checkOwners := func(t *testing.T, revision, lastRevision int64) {
		t.Helper()
		rows, err := models.UserObjects.Using(reopen(t)).Filter(models.UserFields.ID.In(ids...)).OrderBy(models.UserFields.ID.Asc()).All(t.Context())
		if err != nil || len(rows) != len(members) {
			t.Fatal("owner batch observation is incomplete", err)
		}
		for _, row := range rows {
			want := revision
			if row.ID == late.ID {
				want = lastRevision
			}
			if row.Revision != want || row.EncodedPassword != f.account.user.EncodedPassword || !row.DateJoined.Equal(joined) {
				t.Fatal("partial owner update survived", row.ID, row.Revision, want)
			}
		}
	}
	if !t.Run("owner_revision_late_failure", func(t *testing.T) {
		result, err := groups.Delete(t.Context(), actor, group.ID, 1)
		if result.DeletedRows != 0 {
			t.Fatal("late owner failure published deletion")
		}
		requireProfileCode(t, err, accountmodel.CodePersistence)
		checkOwners(t, 1, math.MaxInt64)
		if current, err := groups.Get(t.Context(), actor, group.ID); err != nil || current.Revision() != 1 {
			t.Fatal("failed deletion removed target", err)
		}
		if total, err := models.UserGroupsLinkObjects.Using(backend).Filter(models.UserGroupsLinkFields.TargetID.Exact(group.ID)).Count(t.Context()); err != nil || total != 257 {
			t.Fatal("failed deletion changed member links", total, err)
		}
		if entries := catalogAudit(t, backend, actor, "group", group.ID); len(entries) != 1 {
			t.Fatal("failed deletion appended audit")
		}
	}) {
		return
	}
	if _, err := models.UserObjects.Patch(t.Context(), backend, late, models.UserPatch{}.WithRevision(1)); err != nil {
		t.Fatal(err)
	}
	t.Run("complete_owner_delete", func(t *testing.T) {
		result, err := groups.Delete(t.Context(), actor, group.ID, 1)
		if err != nil || result.DeletedRows < 259 {
			t.Fatal("complete owner deletion", result, err)
		}
		checkOwners(t, 2, 2)
		if count, err := models.UserGroupsLinkObjects.Using(backend).Filter(models.UserGroupsLinkFields.TargetID.Exact(group.ID)).Count(t.Context()); err != nil || count != 0 {
			t.Fatal("late owner links retained", err)
		}
		if entries := catalogAudit(t, backend, actor, "group", group.ID); len(entries) != 2 {
			t.Fatal("owner propagation appended per-owner or missing audit")
		}
	})
}
