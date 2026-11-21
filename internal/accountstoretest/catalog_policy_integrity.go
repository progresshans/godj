package accountstoretest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func catalogIndependentPolicy[M any](t *testing.T, backend Backend, f *catalogFixture, resource accountmodel.CatalogResource[M], policy accountmodel.CatalogPermissions, input orm.ModelInput[M]) {
	t.Helper()
	actor := f.credential.Principal()
	target, err := resource.Create(t.Context(), actor, input)
	if err != nil {
		t.Fatal(err)
	}
	revoke := func(permission auth.Permission) {
		link := f.grants[permission]
		if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &link); err != nil || count != 1 {
			t.Fatal("remove one policy grant", err)
		}
	}
	restore := func(permission auth.Permission) {
		link := f.grants[permission]
		current, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.account.user.ID, link.TargetID))
		if err != nil {
			t.Fatal(err)
		}
		f.grants[permission] = current
	}
	for _, operation := range []string{"add", "change", "delete", "view", "view_and_change"} {
		t.Run(operation, func(t *testing.T) {
			permission := map[string]auth.Permission{"add": policy.Add, "change": policy.Change, "delete": policy.Delete, "view": policy.View, "view_and_change": policy.View}[operation]
			revoke(permission)
			defer restore(permission)
			if operation == "view_and_change" {
				revoke(policy.Change)
				defer restore(policy.Change)
			}
			var err error
			switch operation {
			case "add":
				value, failure := resource.Create(t.Context(), actor, input)
				err = failure
				if value.Revision != 0 {
					t.Fatal("missing add grant published mutation")
				}
			case "change":
				value, failure := resource.Update(t.Context(), actor, target.ID, 1, input)
				err = failure
				if value.Revision != 0 {
					t.Fatal("missing change grant admitted no-op")
				}
				if _, failure := resource.Get(t.Context(), actor, target.ID); failure != nil {
					t.Fatal("current view grant did not permit read", failure)
				}
			case "delete":
				value, failure := resource.Delete(t.Context(), actor, target.ID, 1)
				err = failure
				if value != (accountmodel.CatalogDeletion{}) {
					t.Fatal("missing delete grant published mutation")
				}
			case "view":
				value, failure := resource.Get(t.Context(), actor, target.ID)
				if failure != nil || value.Revision() != 1 {
					t.Fatal("current change grant did not permit read", failure)
				}
				return
			case "view_and_change":
				err = catalogFailedRead(t, t.Context(), resource, actor, target.ID, "get")
			}
			if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
				t.Fatal("catalog used wrong operation policy", operation, err)
			}
		})
	}
	if history, err := resource.History(t.Context(), actor, target.ID, 100); err != nil || len(history) != 1 {
		t.Fatal("policy denial mutated catalog audit", err)
	}
	if result, err := resource.Delete(t.Context(), actor, target.ID, 1); err != nil || result.DeletedRows != 1 {
		t.Fatal("restored delete grant control", err)
	}
}

func catalogOperationPolicies(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	config := catalogConfig()
	t.Run("group", func(t *testing.T) {
		catalogIndependentPolicy(t, backend, f, f.catalog.Groups(), config.Group.Permissions, catalogGroupInput(t, map[string]query.Value{"name": query.String("independent operation grants")}, nil))
	})
	t.Run("permission", func(t *testing.T) {
		catalogIndependentPolicy(t, backend, f, f.catalog.Permissions(), config.Permission.Permissions, catalogPermissionInput(t, map[string]query.Value{"code": query.String("policy.independent"), "name": query.String("Independent operations")}))
	})
}

func catalogTargetIntegrity(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("invalid stored catalog"))
	if err != nil {
		t.Fatal(err)
	}
	input := catalogGroupInput(t, map[string]query.Value{"name": query.String("must not repair silently")}, nil)
	check := func(t *testing.T) {
		t.Helper()
		for _, operation := range []string{"get", "history"} {
			requireProfileCode(t, catalogFailedRead(t, t.Context(), f.catalog.Groups(), actor, group.ID, operation), accountmodel.CodePersistence)
		}
		if result, err := f.catalog.Groups().Update(t.Context(), actor, group.ID, 1, input); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePersistence}) || result.Revision != 0 {
			t.Fatal("invalid stored catalog admitted update", err)
		}
		if result, err := f.catalog.Groups().Delete(t.Context(), actor, group.ID, 1); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePersistence}) || result.DeletedRows != 0 {
			t.Fatal("invalid stored catalog admitted deletion", err)
		}
		if len(catalogAudit(t, reopen(t), actor, "group", group.ID)) != 0 {
			t.Fatal("invalid catalog operation appended audit")
		}
	}
	t.Run("revision", func(t *testing.T) {
		if _, err := models.GroupObjects.Patch(t.Context(), backend, group, models.GroupPatch{}.WithRevision(0)); err != nil {
			t.Fatal(err)
		}
		check(t)
		if row := catalogRow(t, models.GroupObjects.Using(reopen(t)).Filter(models.GroupFields.ID.Exact(group.ID)).OrderBy(models.GroupFields.ID.Asc())); row.Revision != 0 || row.Name != group.Name {
			t.Fatal("corrupt group was silently repaired")
		}
	})
	if _, err := models.GroupObjects.Patch(t.Context(), backend, group, models.GroupPatch{}.WithRevision(1)); err != nil {
		t.Fatal(err)
	}
	permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("UPPER.invalid", "Invalid stored code"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
		t.Fatal(err)
	}
	t.Run("permission_code", func(t *testing.T) {
		check(t)
		for _, operation := range []string{"get", "history"} {
			requireProfileCode(t, catalogFailedRead(t, t.Context(), f.catalog.Permissions(), actor, permission.ID, operation), accountmodel.CodePersistence)
		}
		if result, err := f.catalog.Permissions().Update(t.Context(), actor, permission.ID, 1, catalogPermissionInput(t, map[string]query.Value{"code": query.String("repair.forbidden")})); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePersistence}) || result.Revision != 0 {
			t.Fatal("invalid permission was silently repaired", err)
		}
	})
	if _, err := models.PermissionObjects.Patch(t.Context(), backend, permission, models.PermissionPatch{}.WithCode("integrity.base")); err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for index := 0; index < auth.MaximumPermissions; index++ {
			permission, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("integrity.extra%d", index), "Stored overflow"))
			if err != nil {
				return err
			}
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), scope, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("stored_permission_overflow", func(t *testing.T) {
		check(t)
		if count, err := models.GroupPermissionsLinkObjects.Using(reopen(t)).Filter(models.GroupPermissionsLinkFields.SourceID.Exact(group.ID)).Count(t.Context()); err != nil || count != 257 {
			t.Fatal("invalid stored membership was truncated", err)
		}
		// A scalar list intentionally does not claim complete grant validation.
		if page, err := f.catalog.Groups().List(t.Context(), actor, 0, 100); err != nil || page.Total != 2 {
			t.Fatal("scalar catalog page acquired an implicit grant load", err)
		}
	})
}
