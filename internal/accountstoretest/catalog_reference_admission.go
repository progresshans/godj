package accountstoretest

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func catalogReferenceAdmissionCalls[M any](t *testing.T, backend Backend, reopen func(*testing.T) Backend, resource accountmodel.CatalogResource[M], actor auth.Principal, kind string, target int64, name, code string, input, empty orm.ModelInput[M]) map[string]bool {
	t.Helper()
	before := accountNativeSnapshot(t, reopen(t))
	detail, getErr := resource.Get(t.Context(), actor, target)
	page, listErr := resource.List(t.Context(), actor, 0, 100)
	if (getErr == nil) != (listErr == nil) {
		t.Fatal("selected catalog detail/list admission diverged")
	}
	if getErr == nil {
		if detail.ID() != target || detail.Revision() != 1 || page.Total == 0 || len(page.Items) == 0 {
			t.Fatal("allowed catalog read did not return its actual rows")
		}
	} else {
		requireProfileCode(t, getErr, accountmodel.CodePermission)
		requireProfileCode(t, listErr, accountmodel.CodePermission)
		if detail.Revision() != 0 || detail.View().Values() != nil || page.Items != nil || page.Total != 0 {
			t.Fatal("denied catalog read disclosed selected data")
		}
	}
	if !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
		t.Fatal("catalog admission reads changed native state")
	}
	changed, updateErr := resource.Update(t.Context(), actor, target, 1, empty)
	if updateErr == nil {
		if changed.ID != target || changed.Revision != 1 || len(changed.ChangedFields) != 0 || changed.RevokedSessions != 0 {
			t.Fatal("allowed catalog no-op changed its revision or mutation metadata")
		}
	} else {
		requireProfileCode(t, updateErr, accountmodel.CodePermission)
		if !reflect.DeepEqual(changed, accountmodel.CatalogChange{}) {
			t.Fatal("denied catalog edit published mutation metadata")
		}
	}
	if !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
		t.Fatal("catalog no-op admission changed native state")
	}
	created, createErr := resource.Create(t.Context(), actor, input)
	if createErr != nil {
		requireProfileCode(t, createErr, accountmodel.CodePermission)
		if !reflect.DeepEqual(created, accountmodel.CatalogChange{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("denied catalog creation published data or native writes")
		}
	} else {
		fields := []string{"name", "permissions"}
		if kind == "group" {
			row := models.NewGroupWithID(created.ID)
			row.Name, row.Revision = name, 1
			before.groups = append(before.groups, row)
		} else {
			fields = []string{"code", "name"}
			row := models.NewPermissionWithID(created.ID)
			row.Code, row.Name, row.Revision = code, name, 1
			before.permissions = append(before.permissions, row)
		}
		if created.ID <= 0 || created.ID == target || created.Revision != 1 || created.RevokedSessions != 0 || !slices.Equal(created.ChangedFields, fields) {
			t.Fatal("allowed catalog creation lost its exact mutation metadata")
		}
		assertCatalogState(t, reopen(t), before, actor, catalogExpectedEvent{kind, created.ID, admin.ActionAdd, fields})
	}
	before = accountNativeSnapshot(t, reopen(t))
	deleted, deleteErr := resource.Delete(t.Context(), actor, target, 1)
	if deleteErr != nil {
		requireProfileCode(t, deleteErr, accountmodel.CodePermission)
		if deleted != (accountmodel.CatalogDeletion{}) || !reflect.DeepEqual(before, accountNativeSnapshot(t, reopen(t))) {
			t.Fatal("denied catalog deletion published metadata or native writes")
		}
	} else {
		if deleted.ID != target || deleted.Revision != 1 || deleted.DeletedRows != 1 || deleted.RevokedSessions != 0 {
			t.Fatal("allowed catalog deletion lost its exact mutation metadata")
		}
		if kind == "group" {
			before.groups = slices.DeleteFunc(before.groups, func(row models.Group) bool { return row.ID == target })
		} else {
			before.permissions = slices.DeleteFunc(before.permissions, func(row models.Permission) bool { return row.ID == target })
		}
		assertCatalogState(t, reopen(t), before, actor, catalogExpectedEvent{kind, target, admin.ActionDelete, nil})
	}
	return map[string]bool{"view": getErr == nil, "create": createErr == nil, "change": updateErr == nil, "delete": deleteErr == nil}
}

func selectedCatalogAdmissionReference(t *testing.T, vendor string, backend Backend, reopen func(*testing.T) Backend) {
	reference := loadCatalogReference(t, vendor)
	f := provisionCatalog(t, backend)
	privileged, err := models.UserObjects.Patch(t.Context(), backend, f.account.user, models.UserPatch{}.WithStaff(true).WithSuperuser(true).WithRevision(2))
	if err != nil {
		t.Fatal(err)
	}
	held, present, err := f.account.store.CredentialByID(t.Context(), privileged.PrincipalID)
	if err != nil || !present || !held.Principal().Superuser() {
		t.Fatal("catalog admission fixture did not observe its privileged actor", err)
	}
	if _, err := models.UserObjects.Patch(t.Context(), reopen(t), privileged, models.UserPatch{}.WithSuperuser(false).WithRevision(3)); err != nil {
		t.Fatal(err)
	}
	actor := held.Principal()
	formValidationSessions(t, backend, held)
	// Retain the actually observed old superuser and permission claims. Each
	// operation must use the sole current database grant established below.
	for _, link := range f.grants {
		if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &link); err != nil || count != 1 {
			t.Fatal("catalog admission fixture did not revoke its old authority", err)
		}
	}
	observations := make(map[string]map[string]map[string]bool)
	for _, kind := range []string{"group", "permission"} {
		observations[kind] = make(map[string]map[string]bool)
		for _, action := range []string{"none", "view", "add", "change", "delete"} {
			if !t.Run(kind+"_"+action, func(t *testing.T) {
				policy := catalogConfig().Group.Permissions
				if kind == "permission" {
					policy = catalogConfig().Permission.Permissions
				}
				if action != "none" {
					permission := map[string]auth.Permission{"view": policy.View, "add": policy.Add, "change": policy.Change, "delete": policy.Delete}[action]
					original := f.grants[permission]
					grant, err := models.UserPermissionsLinkObjects.Create(t.Context(), reopen(t), models.NewUserPermissionsLinkCreate(original.SourceID, original.TargetID))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if count, err := models.UserPermissionsLinkObjects.Delete(context.Background(), backend, &grant); err != nil || count != 1 {
							t.Error("catalog admission fixture did not release its current grant", err)
						}
					})
				}
				name, code := "Created "+kind+" "+action, "reference.created_"+action
				if kind == "group" {
					target, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Target "+kind+" "+action))
					if err != nil {
						t.Fatal(err)
					}
					observations[kind][action] = catalogReferenceAdmissionCalls(t, backend, reopen, f.catalog.Groups(), actor, kind, target.ID, name, code,
						catalogGroupInput(t, map[string]query.Value{"name": query.String(name)}, nil), catalogGroupInput(t, nil, nil))
				} else {
					target, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("reference.target_"+action, "Target "+kind+" "+action))
					if err != nil {
						t.Fatal(err)
					}
					observations[kind][action] = catalogReferenceAdmissionCalls(t, backend, reopen, f.catalog.Permissions(), actor, kind, target.ID, name, code,
						catalogPermissionInput(t, map[string]query.Value{"code": query.String(code), "name": query.String(name)}), catalogPermissionInput(t, nil))
				}
			}) {
				return
			}
		}
	}
	reference.assert(t, "admission", observations)
}
