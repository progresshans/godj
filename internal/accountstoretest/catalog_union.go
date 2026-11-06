package accountstoretest

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func catalogBoundGrants(t *testing.T, backend Backend) []int64 {
	t.Helper()
	keys := make([]int64, auth.MaximumPermissions)
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for index := range keys {
			row, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("union.p%03d", index), "Union grant"))
			if err != nil {
				return err
			}
			keys[index] = row.ID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return keys
}

func catalogGroupUnionBatches(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor, groups := f.credential.Principal(), f.catalog.Groups()
	keys := catalogBoundGrants(t, backend)
	group, err := groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("Large union")}, map[string][]int64{"permissions": keys[:255]}))
	if err != nil {
		t.Fatal(err)
	}
	var last models.User
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for index := range 257 {
			name := fmt.Sprintf("union-member-%03d", index)
			last, err = models.UserObjects.Create(t.Context(), scope, models.NewUserCreate(name, name, f.account.user.EncodedPassword, joined))
			if err != nil {
				return err
			}
			if _, err := models.UserGroupsLinkObjects.Create(t.Context(), scope, models.NewUserGroupsLinkCreate(last.ID, group.ID)); err != nil {
				return err
			}
		}
		_, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(last.ID, f.account.grants[0].ID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	credential, present, err := f.account.store.CredentialByID(t.Context(), last.PrincipalID)
	if err != nil || !present || len(credential.Principal().Permissions()) != 256 {
		t.Fatal("last member's exact bound control", err)
	}
	formValidationSessions(t, backend, credential)
	proposed := catalogGroupInput(t, map[string]query.Value{"name": query.String("Expanded union")}, map[string][]int64{"permissions": keys})
	if !t.Run("late_member_rejection", func(t *testing.T) {
		before := accountNativeSnapshot(t, reopen(t))
		result, err := groups.Update(t.Context(), actor, group.ID, 1, proposed)
		failures, rejected := validation.Rejected(err)
		if !rejected || failures.ByField("permissions").Empty() || !reflect.DeepEqual(result, accountmodel.CatalogChange{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("last member's direct grants were excluded from the complete union", err)
		}
	}) {
		return
	}
	if !t.Run("clear_direct_and_expand", func(t *testing.T) {
		expected := accountNativeSnapshot(t, reopen(t))
		for index := range expected.users {
			if expected.users[index].ID == last.ID {
				expected.users[index].Revision = 2
			}
		}
		expected.direct = slices.DeleteFunc(expected.direct, func(row models.UserPermissionsLink) bool { return row.SourceID == last.ID })
		user, err := roleEditor(t, catalogSessions(t, reopen(t))).Update(t.Context(), actor, last.ID, 1, roleInput(t, nil, map[string][]int64{"permissions": nil}))
		if err != nil || user.ID != last.ID || user.Revision != 2 || user.RevokedSessions != 0 || !slices.Equal(user.ChangedFields, []string{"permissions"}) {
			t.Fatal("selected member could not clear its direct grant", err)
		}
		assertCatalogState(t, reopen(t), expected, actor, catalogExpectedEvent{"user", last.ID, admin.ActionChange, []string{"permissions"}})
		expected = accountNativeSnapshot(t, reopen(t))
		for index := range expected.groups {
			if expected.groups[index].ID == group.ID {
				expected.groups[index].Name, expected.groups[index].Revision = "Expanded union", 2
			}
		}
		result, err := groups.Update(t.Context(), actor, group.ID, 1, proposed)
		if err != nil || result.ID != group.ID || result.Revision != 2 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, []string{"name", "permissions"}) {
			t.Fatal("representable full group expansion failed", err)
		}
		after := accountNativeSnapshot(t, reopen(t))
		expected.grants = catalogExpectedGrantAdditions(t, expected.grants, after.grants, group.ID, keys[255:])
		assertCatalogState(t, reopen(t), expected, actor, catalogExpectedEvent{"group", group.ID, admin.ActionChange, []string{"name", "permissions"}})
		current, present, err := f.account.store.CredentialByID(t.Context(), last.PrincipalID)
		if err != nil || !present || len(current.Principal().Permissions()) != 256 || !current.MatchesSessionStamp(credential.SessionStamp()) {
			t.Fatal("last-page expansion lost the exact permission bound or credential", err)
		}
	}) {
		return
	}
	t.Run("oversized_selection", func(t *testing.T) {
		before := accountNativeSnapshot(t, reopen(t))
		tooMany := append(slices.Clone(keys), f.account.grants[0].ID)
		created, err := groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("Too many grants")}, map[string][]int64{"permissions": tooMany}))
		if _, rejected := validation.Rejected(err); !rejected || !reflect.DeepEqual(created, accountmodel.CatalogChange{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("oversized group selection changed native state", err)
		}
	})
}

func catalogOverlappingGroupUnion(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor, groups := f.credential.Principal(), f.catalog.Groups()
	keys := catalogBoundGrants(t, backend)
	other, err := groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("Other union")}, map[string][]int64{"permissions": keys[:255]}))
	if err != nil {
		t.Fatal(err)
	}
	group, err := groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("Edited union")}, map[string][]int64{"permissions": keys[:1]}))
	if err != nil {
		t.Fatal(err)
	}
	member := roleTarget(t, backend, f.account, "overlapping-union")
	for _, id := range []int64{other.ID, group.ID} {
		if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(member.ID, id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(member.ID, f.account.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	credential, present, err := f.account.store.CredentialByID(t.Context(), member.PrincipalID)
	if err != nil || !present || len(credential.Principal().Permissions()) != 256 {
		t.Fatal("overlapping union's exact bound control", err)
	}
	formValidationSessions(t, backend, credential)
	if !t.Run("other_group_overflow", func(t *testing.T) {
		before := accountNativeSnapshot(t, reopen(t))
		changed, err := groups.Update(t.Context(), actor, group.ID, 1, catalogGroupInput(t, nil, map[string][]int64{"permissions": keys[255:]}))
		failures, rejected := validation.Rejected(err)
		if !rejected || failures.ByField("permissions").Empty() || !reflect.DeepEqual(changed, accountmodel.CatalogChange{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("union validation ignored another group's grants", err)
		}
	}) {
		return
	}
	t.Run("overlap_is_counted_once", func(t *testing.T) {
		expected := accountNativeSnapshot(t, reopen(t))
		for index := range expected.groups {
			if expected.groups[index].ID == group.ID {
				expected.groups[index].Revision = 2
			}
		}
		changed, err := groups.Update(t.Context(), actor, group.ID, 1, catalogGroupInput(t, nil, map[string][]int64{"permissions": keys[:255]}))
		if err != nil || changed.ID != group.ID || changed.Revision != 2 || changed.RevokedSessions != 0 || !slices.Equal(changed.ChangedFields, []string{"permissions"}) {
			t.Fatal("overlapping permissions were counted more than once", err)
		}
		after := accountNativeSnapshot(t, reopen(t))
		expected.grants = catalogExpectedGrantAdditions(t, expected.grants, after.grants, group.ID, keys[1:255])
		assertCatalogState(t, reopen(t), expected, actor, catalogExpectedEvent{"group", group.ID, admin.ActionChange, []string{"permissions"}})
		current, present, err := f.account.store.CredentialByID(t.Context(), member.PrincipalID)
		if err != nil || !present || !slices.Equal(current.Principal().Permissions(), credential.Principal().Permissions()) || !current.MatchesSessionStamp(credential.SessionStamp()) {
			t.Fatal("overlap changed effective grants or credential state", err)
		}
	})
}

func catalogGroupSelfRevocation(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor, groups := f.credential.Principal(), f.catalog.Groups()
	permission := f.grants[catalogConfig().Group.Permissions.Change]
	group, err := groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("Actor authority")}, map[string][]int64{"permissions": {permission.TargetID}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.account.user.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &permission); err != nil || count != 1 {
		t.Fatal("actor grant was not moved into the group", err)
	}
	bare, err := auth.NewPrincipal(auth.PrincipalConfig{ID: actor.ID(), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	domain, _ := actor.CredentialDomain()
	claim, err := auth.NewCredentialForDomain(domain, f.account.user.Username, f.account.user.EncodedPassword, bare)
	if err != nil {
		t.Fatal(err)
	}
	formValidationSessions(t, backend, f.credential)
	if !t.Run("current_group_authority", func(t *testing.T) {
		expected := accountNativeSnapshot(t, reopen(t))
		for index := range expected.groups {
			if expected.groups[index].ID == group.ID {
				expected.groups[index].Revision = 2
			}
		}
		expected.grants = slices.DeleteFunc(expected.grants, func(row models.GroupPermissionsLink) bool { return row.SourceID == group.ID })
		changed, err := groups.Update(t.Context(), claim.Principal(), group.ID, 1, catalogGroupInput(t, nil, map[string][]int64{"permissions": nil}))
		if err != nil || changed.ID != group.ID || changed.Revision != 2 || changed.RevokedSessions != 0 || !slices.Equal(changed.ChangedFields, []string{"permissions"}) {
			t.Fatal("current group authority could not revoke itself", err)
		}
		assertCatalogState(t, reopen(t), expected, actor, catalogExpectedEvent{"group", group.ID, admin.ActionChange, []string{"permissions"}})
	}) {
		return
	}
	t.Run("stale_authority_rejected", func(t *testing.T) {
		before := accountNativeSnapshot(t, reopen(t))
		changed, err := groups.Update(t.Context(), actor, group.ID, 2, catalogGroupInput(t, map[string]query.Value{"name": query.String("Stale authority")}, nil))
		requireProfileCode(t, err, accountmodel.CodePermission)
		if !reflect.DeepEqual(changed, accountmodel.CatalogChange{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("past actor grants survived self-revocation")
		}
		current, present, err := f.account.store.CredentialByID(t.Context(), actor.ID())
		if err != nil || !present || current.Principal().Has(catalogConfig().Group.Permissions.Change) || !current.MatchesSessionStamp(f.credential.SessionStamp()) {
			t.Fatal("self-revocation was absent from the current credential", err)
		}
	})
}
