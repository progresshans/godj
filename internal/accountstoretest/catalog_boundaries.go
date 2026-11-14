package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func catalogInputBoundaries(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalogOperation(t, backend)
	actor, groups, permissions := f.credential.Principal(), f.catalog.Groups(), f.catalog.Permissions()
	before := catalogHostSnapshot(t, reopen(t))
	for _, test := range []struct {
		name string
		call func(*testing.T) (accountmodel.CatalogChange, error)
	}{
		{"group_missing_name", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return groups.Create(t.Context(), actor, catalogGroupInput(t, nil, nil))
		}},
		{"group_empty_name", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("")}, nil))
		}},
		{"group_unicode_limit", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String(strings.Repeat("한", 151))}, nil))
		}},
		{"group_nul", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("nul\x00name")}, nil))
		}},
		{"group_duplicate", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String(f.group.Name)}, nil))
		}},
		{"group_missing_permission", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("missing choice")}, map[string][]int64{"permissions": {mathMaxKey}}))
		}},
		{"group_missing_negative_permission", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return groups.Update(t.Context(), actor, f.group.ID, 1, catalogGroupInput(t, nil, map[string][]int64{"permissions": {-1}}))
		}},
		{"permission_missing_fields", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return permissions.Create(t.Context(), actor, catalogPermissionInput(t, nil))
		}},
		{"permission_invalid_code", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return permissions.Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("Malformed"), "name": query.String("Name")}))
		}},
		{"permission_empty_name", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return permissions.Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("catalog.valid"), "name": query.String("")}))
		}},
		{"permission_invalid_utf8", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return permissions.Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("catalog.valid"), "name": query.String("bad\xff")}))
		}},
		{"permission_duplicate_create", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return permissions.Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String(f.permission.Code), "name": query.String("Duplicate")}))
		}},
		{"permission_duplicate_update", func(t *testing.T) (accountmodel.CatalogChange, error) {
			return permissions.Update(t.Context(), actor, f.permission.ID, 1, catalogPermissionInput(t, map[string]query.Value{"code": query.String(f.view.Code)}))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.call(t)
			if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.Empty() || !reflect.DeepEqual(result, accountmodel.CatalogChange{}) {
				t.Fatal("catalog input did not reject without publishing metadata", err)
			}
			if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
				t.Fatal("rejected catalog input changed native account/catalog/host/session/domain/audit state")
			}
		})
	}
	for _, test := range []struct {
		name string
		code accountmodel.ErrorCode
		call func(*testing.T) (any, error)
	}{
		{"group_unbound_input", accountmodel.CodeInvalidInput, func(t *testing.T) (any, error) {
			return groups.Create(t.Context(), actor, orm.ModelInput[models.Group]{})
		}},
		{"permission_unbound_input", accountmodel.CodeInvalidInput, func(t *testing.T) (any, error) {
			return permissions.Create(t.Context(), actor, orm.ModelInput[models.Permission]{})
		}},
		{"group_stale_update", accountmodel.CodeConflict, func(t *testing.T) (any, error) {
			return groups.Update(t.Context(), actor, f.group.ID, 2, catalogGroupInput(t, map[string]query.Value{"name": query.String("stale")}, nil))
		}},
		{"group_stale_delete", accountmodel.CodeConflict, func(t *testing.T) (any, error) {
			return groups.Delete(t.Context(), actor, f.group.ID, 2)
		}},
		{"permission_stale_update", accountmodel.CodeConflict, func(t *testing.T) (any, error) {
			return permissions.Update(t.Context(), actor, f.permission.ID, 2, catalogPermissionInput(t, map[string]query.Value{"name": query.String("stale")}))
		}},
		{"permission_stale_delete", accountmodel.CodeConflict, func(t *testing.T) (any, error) {
			return permissions.Delete(t.Context(), actor, f.permission.ID, 2)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.call(t)
			requireProfileCode(t, err, test.code)
			if !reflect.ValueOf(result).IsZero() || !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
				t.Fatal("invalid binding or stale catalog revision published a result or changed native state", err)
			}
		})
	}
}

func catalogPageBoundary[M any](t *testing.T, resource accountmodel.CatalogResource[M], actor auth.Principal, total, secondID int64, secondName string) {
	t.Helper()
	page, err := resource.List(t.Context(), actor, 1, 1)
	if err != nil || page.Total != total || len(page.Items) != 1 || page.Items[0].ID() != secondID || catalogReferenceText(t, page.Items[0].View(), "name") != secondName {
		t.Fatal("catalog page did not preserve the native count, offset and selected row", err)
	}
	values := page.Items[0].View().Values()
	values["name"] = query.String("caller mutation")
	page.Items[0] = accountmodel.CatalogRecord[M]{}
	again, err := resource.List(t.Context(), actor, 1, 1)
	if err != nil || again.Total != total || len(again.Items) != 1 || again.Items[0].ID() != secondID || catalogReferenceText(t, again.Items[0].View(), "name") != secondName {
		t.Fatal("catalog page retained caller mutation", err)
	}
	for _, bounds := range [][2]int{{-1, 1}, {0, 101}} {
		page, err := resource.List(t.Context(), actor, bounds[0], bounds[1])
		requireProfileCode(t, err, accountmodel.CodeInvalidInput)
		if page.Items != nil || page.Total != 0 {
			t.Fatal("invalid catalog page published rows or a count")
		}
	}
}

func catalogValueSemantics(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalogOperation(t, backend)
	actor := f.credential.Principal()
	if !t.Run("unicode_omission", func(t *testing.T) {
		expected := catalogHostSnapshot(t, reopen(t))
		name := strings.Repeat("한", 150)
		for index := range expected.account.groups {
			if expected.account.groups[index].ID == f.group.ID {
				expected.account.groups[index].Name, expected.account.groups[index].Revision = name, 2
			}
		}
		result, err := f.catalog.Groups().Update(t.Context(), actor, f.group.ID, 1, catalogGroupInput(t, map[string]query.Value{"name": query.String(name)}, nil))
		if err != nil || result.ID != f.group.ID || result.Revision != 2 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, []string{"name"}) {
			t.Fatal("catalog rejected the Unicode character boundary or changed an omitted collection", err)
		}
		assertCatalogHostState(t, reopen(t), expected, actor, catalogExpectedEvent{"group", f.group.ID, admin.ActionChange, []string{"name"}})
		value, err := f.catalog.Groups().Get(t.Context(), actor, f.group.ID)
		if err != nil || catalogReferenceText(t, value.View(), "name") != name {
			t.Fatal("Unicode catalog value was not preserved", err)
		}
		if keys, loaded := value.View().Collection("permissions"); !loaded || !slices.Equal(keys, []int64{f.view.ID, f.permission.ID}) {
			t.Fatal("omitted catalog membership was cleared")
		}
	}) {
		return
	}
	if !t.Run("explicit_clear", func(t *testing.T) {
		expected := catalogHostSnapshot(t, reopen(t))
		for index := range expected.account.groups {
			if expected.account.groups[index].ID == f.group.ID {
				expected.account.groups[index].Revision++
			}
		}
		expected.account.grants = slices.DeleteFunc(expected.account.grants, func(row models.GroupPermissionsLink) bool { return row.SourceID == f.group.ID })
		result, err := f.catalog.Groups().Update(t.Context(), actor, f.group.ID, 2, catalogGroupInput(t, nil, map[string][]int64{"permissions": {}}))
		if err != nil || result.ID != f.group.ID || result.Revision != 3 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, []string{"permissions"}) {
			t.Fatal("explicit empty catalog membership did not clear exactly once", err)
		}
		assertCatalogHostState(t, reopen(t), expected, actor, catalogExpectedEvent{"group", f.group.ID, admin.ActionChange, []string{"permissions"}})
		value, err := f.catalog.Groups().Get(t.Context(), actor, f.group.ID)
		if err != nil {
			t.Fatal(err)
		}
		if keys, loaded := value.View().Collection("permissions"); !loaded || len(keys) != 0 {
			t.Fatal("explicit empty catalog membership became unloaded")
		}
	}) {
		return
	}
	before := catalogHostSnapshot(t, reopen(t))
	t.Run("group_page", func(t *testing.T) {
		rows := before.account.groups
		if len(rows) < 2 {
			t.Fatal("catalog page fixture is incomplete")
		}
		catalogPageBoundary(t, f.catalog.Groups(), actor, int64(len(rows)), rows[1].ID, rows[1].Name)
	})
	t.Run("permission_page", func(t *testing.T) {
		rows := before.account.permissions
		if len(rows) < 2 {
			t.Fatal("permission page fixture is incomplete")
		}
		catalogPageBoundary(t, f.catalog.Permissions(), actor, int64(len(rows)), rows[1].ID, rows[1].Name)
	})
	store, err := hostproject.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := hostproject.NewAccountCatalog(store, overlayFunc(func(context.Context, auth.Principal, auth.Permission) (bool, error) { return false, nil }), catalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	cause := &accountmodel.Error{Code: accountmodel.CodePermission}
	t.Run("deny_overlay_group", func(t *testing.T) {
		catalogDeniedOperations(t, denied.Groups(), actor, f.group.ID, 3, catalogGroupInput(t, map[string]query.Value{"name": query.String("Denied")}, nil), cause)
	})
	t.Run("deny_overlay_permission", func(t *testing.T) {
		catalogDeniedOperations(t, denied.Permissions(), actor, f.permission.ID, 1, catalogPermissionInput(t, map[string]query.Value{"code": query.String("catalog.denied"), "name": query.String("Denied")}), cause)
	})
	if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
		t.Fatal("catalog pages or denied operations changed native account/catalog/host/session/domain/audit state")
	}
}

func catalogRejectedCleanup(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalogOperation(t, backend)
	actor := f.credential.Principal()
	for _, kind := range []string{"group", "permission"} {
		t.Run(kind, func(t *testing.T) {
			before := catalogHostSnapshot(t, reopen(t))
			fault := errors.New("private catalog rejected-write cleanup")
			owner := &profileFaultBackend{Backend: backend, mode: "cleanup_failure", fault: fault}
			store, err := hostproject.NewAccountSessionStore(owner, sessiondb.Config{})
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := hostproject.NewAccountCatalog(store, auth.PrincipalAuthorizer{}, catalogConfig())
			if err != nil {
				t.Fatal(err)
			}
			var result accountmodel.CatalogChange
			if kind == "group" {
				result, err = catalog.Groups().Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String(f.group.Name)}, nil))
			} else {
				result, err = catalog.Permissions().Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String(f.permission.Code), "name": query.String("Duplicate")}))
			}
			if diagnostics, rejected := validation.Rejected(owner.nativeError); !rejected || diagnostics.Empty() || owner.calls != 1 {
				t.Fatal("catalog cleanup probe did not observe an actual native refusal", err)
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if _, rejected := validation.Rejected(err); rejected || !errors.Is(err, fault) || !errors.Is(err, owner.nativeError) || !reflect.DeepEqual(result, accountmodel.CatalogChange{}) || strings.Contains(fmt.Sprintf("%+v", err), "private") {
				t.Fatal("catalog cleanup failure became input, lost its cause or published metadata", err)
			}
			if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
				t.Fatal("catalog rejected-write cleanup changed native state")
			}
		})
	}
}
