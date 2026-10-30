package accountstoretest

import (
	"fmt"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func creationBounds(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	hasher := &creationHasher{PasswordHasher: f.hasher}
	creator := accountCreator(t, backend, hasher, creationConfig())
	var groups, permissions []int64
	var wide models.Group
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for i := range 256 {
			group, err := models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate(fmt.Sprintf("creation-%03d", i)))
			if err != nil {
				return err
			}
			groups = append(groups, group.ID)
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), scope, models.NewGroupPermissionsLinkCreate(group.ID, f.grants[0].ID)); err != nil {
				return err
			}
			permission, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("creation.p%03d", i), "Created grant"))
			if err != nil {
				return err
			}
			permissions = append(permissions, permission.ID)
		}
		var err error
		wide, err = models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate("creation-wide"))
		if err != nil {
			return err
		}
		for _, key := range permissions {
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), scope, models.NewGroupPermissionsLinkCreate(wide.ID, key)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name                string
		groups, permissions []int64
		active, rejected    bool
	}{
		{name: "groups256", groups: groups, active: true},
		{name: "union256", groups: []int64{wide.ID}, active: true},
		{name: "direct256", permissions: permissions, active: true},
		{name: "union257", groups: []int64{wide.ID}, permissions: []int64{f.grants[0].ID}, active: true, rejected: true},
		{name: "inactive257", groups: []int64{wide.ID}, permissions: []int64{f.grants[0].ID}, rejected: true},
		{name: "raw_groups257", groups: make([]int64, 257), active: true, rejected: true},
		{name: "raw_direct257", permissions: make([]int64, 257), active: true, rejected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := hasher.calls.Load()
			input := roleInput(t, map[string]query.Value{"username": query.String("bounds-" + test.name), "active": query.Boolean(test.active)}, map[string][]int64{"groups": test.groups, "permissions": test.permissions})
			prepared, err := creator.Prepare(t.Context(), actor, input, password)
			if test.rejected {
				if _, rejected := validation.Rejected(err); !rejected || hasher.calls.Load() != before {
					t.Fatal("creation bound passed or hashed", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := creator.Commit(t.Context(), actor, prepared)
			if err != nil {
				t.Fatal(err)
			}
			row := profileRow(t, backend, result.ID)
			credential, found, err := f.store.CredentialByID(t.Context(), row.PrincipalID)
			want := 256
			if test.name == "groups256" {
				want = 1
			}
			if err != nil || !found || len(credential.Principal().Permissions()) != want || hasher.calls.Load() != before+1 {
				t.Fatal("created bound or overlap incorrect", err)
			}
		})
	}
	// Recheck a catalog expansion after preparation even for an inactive target.
	pending, err := creator.Prepare(t.Context(), actor, roleInput(t, map[string]query.Value{"username": query.String("expanded-before-commit"), "active": query.Boolean(false)}, map[string][]int64{"groups": {wide.ID}}), password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(wide.ID, f.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	if result, err := creator.Commit(t.Context(), actor, pending); result.Revision != 0 {
		t.Fatal("expanded catalog created overflowing account")
	} else if _, rejected := validation.Rejected(err); !rejected {
		t.Fatal("final grant bound missing", err)
	}
	if creationExists(t, backend, "expanded-before-commit") {
		t.Fatal("overflow left a partial account")
	}
}

func creationNonpositiveChoices(t *testing.T, backend Backend, fixtureKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	creator := accountCreator(t, backend, f.hasher, creationConfig())
	for _, key := range []int64{0, -7} {
		group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate(fmt.Sprintf("creation-key-%d", key)))
		if err != nil {
			t.Fatal(err)
		}
		fixtureKey(t, backend, "group", group.ID, key)
		permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(fmt.Sprintf("creation.key%d", key), "Signed key"))
		if err != nil {
			t.Fatal(err)
		}
		fixtureKey(t, backend, "permission", permission.ID, key)
		input := roleInput(t, map[string]query.Value{"username": query.String(fmt.Sprintf("creation-key-%d", key))}, map[string][]int64{"groups": {key}, "permissions": {key}})
		pending, err := creator.Prepare(t.Context(), actor, input, password)
		if err != nil {
			t.Fatal(err)
		}
		result, err := creator.Commit(t.Context(), actor, pending)
		if err != nil {
			t.Fatal(err)
		}
		assertRoleKeys(t, roleReader(t, backend), actor, result.ID, []int64{key}, []int64{key})
	}
}
