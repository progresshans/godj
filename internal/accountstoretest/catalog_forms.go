package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/validation"
)

func catalogBoundForm[M any](t *testing.T, resource accountmodel.CatalogResource[M], data map[string][]string, current *M, choices ...int64) formmodel.BoundForm {
	t.Helper()
	model, err := resource.Manager().Metadata()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewSpecForFields(model, resource.WriteFields())
	if err != nil {
		t.Fatal(err)
	}
	description, err := resource.Description()
	if err != nil {
		t.Fatal(err)
	}
	if description.CollectionField != "" {
		items := make([]forms.Choice, len(choices))
		for index, key := range choices {
			items[index] = forms.Choice{Value: forms.Integer(key), Label: "Submitted selection"}
		}
		spec, err = spec.WithModelChoices(description.CollectionField, items...)
		if err != nil {
			t.Fatal(err)
		}
	}
	bound, err := formmodel.BindInstance(t.Context(), resource.Manager(), spec, forms.NewData(data), current, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	return bound.BoundForm()
}

func catalogFormChecks(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	permissions, groups := f.catalog.Permissions(), f.catalog.Groups()
	permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("catalog.form_check", "Existing"))
	if err != nil {
		t.Fatal(err)
	}
	group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Existing group"))
	if err != nil {
		t.Fatal(err)
	}
	beforePermissions, err := models.PermissionObjects.Using(backend).OrderBy(models.PermissionFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	beforeGroups, err := models.GroupObjects.Using(backend).OrderBy(models.GroupFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"partial_unique", "current_excluded", "stale", "missing", "group_selection", "revoked", "additional"} {
		t.Run(mode, func(t *testing.T) {
			bound := catalogBoundForm(t, permissions, map[string][]string{"code": {permission.Code}, "name": {""}}, nil)
			if bound.Form().Valid() {
				t.Fatal("partial-invalid fixture is valid")
			}
			var err error
			switch mode {
			case "partial_unique":
				err = permissions.CheckCreateForm(t.Context(), actor, bound)
				failures, rejected := validation.Rejected(err)
				if !rejected || failures.ByField("code").Empty() || !failures.ByField("name").Empty() {
					t.Fatal("valid code uniqueness was skipped or old errors repeated", err)
				}
			case "current_excluded":
				bound = catalogBoundForm(t, permissions, map[string][]string{"code": {permission.Code}, "name": {"Edited"}}, &permission)
				if err := permissions.CheckChangeForm(t.Context(), actor, permission.ID, permission.Revision, bound); err != nil {
					t.Fatal("current row included in uniqueness", err)
				}
			case "stale":
				old := permission
				old.Revision++
				bound = catalogBoundForm(t, permissions, map[string][]string{"code": {permission.Code}, "name": {""}}, &old)
				err = permissions.CheckChangeForm(t.Context(), actor, old.ID, old.Revision, bound)
				requireProfileCode(t, err, accountmodel.CodeConflict)
			case "missing":
				missing := permission
				missing.ID = mathMaxKey
				bound = catalogBoundForm(t, permissions, map[string][]string{"code": {permission.Code}, "name": {""}}, &missing)
				requireProfileCode(t, permissions.CheckChangeForm(t.Context(), actor, missing.ID, missing.Revision, bound), accountmodel.CodeNotFound)
			case "group_selection":
				bound = catalogBoundForm(t, groups, map[string][]string{"name": {group.Name}, "permissions": {strconv.FormatInt(mathMaxKey, 10)}}, nil, mathMaxKey)
				err = groups.CheckCreateForm(t.Context(), actor, bound)
				failures, rejected := validation.Rejected(err)
				if !rejected || failures.ByField("name").Empty() || failures.ByField("permissions").Empty() {
					t.Fatal("relation refusal suppressed independent uniqueness", err)
				}
			case "revoked":
				link := f.grants[catalogConfig().Permission.Permissions.Add]
				if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &link); err != nil || count != 1 {
					t.Fatal(err)
				}
				err = permissions.CheckCreateForm(t.Context(), actor, bound)
				requireProfileCode(t, err, accountmodel.CodePermission)
				link, err = models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(link.SourceID, link.TargetID))
				if err != nil {
					t.Fatal(err)
				}
				f.grants[catalogConfig().Permission.Permissions.Add] = link
			case "additional":
				config := catalogConfig()
				config.Permission.Permissions.Additional = []auth.Permission{"catalog.extra"}
				owner, err := project.NewAccountCatalog(catalogSessions(t, backend), auth.PrincipalAuthorizer{}, config)
				if err != nil {
					t.Fatal(err)
				}
				requireProfileCode(t, owner.Permissions().CheckCreateForm(t.Context(), actor, bound), accountmodel.CodePermission)
			}
		})
	}
	afterPermissions, err := models.PermissionObjects.Using(reopen(t)).OrderBy(models.PermissionFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	afterGroups, err := models.GroupObjects.Using(reopen(t)).OrderBy(models.GroupFields.ID.Asc()).All(t.Context())
	if err != nil || !reflect.DeepEqual(beforePermissions, afterPermissions) || !reflect.DeepEqual(beforeGroups, afterGroups) {
		t.Fatal("advisory validation wrote catalog rows", err)
	}
	if entries := catalogAudit(t, backend, actor, "permission", permission.ID); len(entries) != 0 {
		t.Fatal("advisory validation wrote audit")
	}
	// An advisory success cannot reserve uniqueness against a later writer.
	bound := catalogBoundForm(t, permissions, map[string][]string{"code": {"catalog.form_race"}, "name": {"Race"}}, nil)
	if err := permissions.CheckCreateForm(t.Context(), actor, bound); err != nil {
		t.Fatal(err)
	}
	if _, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("catalog.form_race", "Other writer")); err != nil {
		t.Fatal(err)
	}
	input, err := formmodel.PrepareInput(permissions.Manager(), bound)
	if err != nil {
		t.Fatal(err)
	}
	result, err := permissions.Create(t.Context(), actor, input)
	if _, rejected := validation.Rejected(err); !rejected || result.Revision != 0 {
		t.Fatal("advisory validation bypassed final unique check", err)
	}
}

func catalogFormFailures(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("catalog.form_failure", "Existing"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "denial_cleanup", "conflict_cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			fault := errors.New("private form snapshot details")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			owner := &catalogReadBackend{Backend: backend}
			owner.read = func(ctx context.Context, observe func(db.Queryer) error) error {
				switch mode {
				case "skipped":
					return nil
				case "nil":
					return observe(nil)
				case "root":
					return observe(backend)
				}
				err := backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
					probe := &catalogReadProbe{Queryer: scope, validator: scope.(db.SessionValidator), table: "godj_identity_permission", skip: 1}
					if mode == "query" || mode == "close" || mode == "swallowed" {
						probe.fault, probe.closeFail = fault, mode == "close"
					}
					err := observe(probe)
					if mode == "swallowed" {
						return nil
					}
					if mode == "repeated" {
						return errors.Join(err, observe(probe))
					}
					return err
				})
				if strings.HasSuffix(mode, "cleanup") {
					return errors.Join(err, fault)
				}
				if mode == "cancel" {
					cancel()
				}
				return err
			}
			config := catalogConfig()
			if mode == "denial_cleanup" {
				config.Permission.Permissions.Add = "catalog.denied"
			}
			catalog, err := project.NewAccountCatalog(catalogSessions(t, owner), auth.PrincipalAuthorizer{}, config)
			if err != nil {
				t.Fatal(err)
			}
			resource := catalog.Permissions()
			bound := catalogBoundForm(t, resource, map[string][]string{"code": {permission.Code}, "name": {""}}, nil)
			if mode == "conflict_cleanup" {
				stale := permission
				stale.Revision++
				bound = catalogBoundForm(t, resource, map[string][]string{"code": {permission.Code}, "name": {""}}, &stale)
				err = resource.CheckChangeForm(ctx, actor, stale.ID, stale.Revision, bound)
			} else {
				err = resource.CheckCreateForm(ctx, actor, bound)
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if _, rejected := validation.Rejected(err); rejected || strings.Contains(fmt.Sprintf("%+v", err), "private") {
				t.Fatal("failed snapshot disclosed input diagnostics or private cause")
			}
			if mode == "query" || mode == "close" || mode == "swallowed" || strings.HasSuffix(mode, "cleanup") {
				if !errors.Is(err, fault) {
					t.Fatal("read failure lost cause", err)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("read failure lost cancellation")
			}
		})
	}
}

func catalogFormNonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provisionCatalog(t, backend)
	resource, actor := f.catalog.Permissions(), f.credential.Principal()
	for _, id := range []int64{0, -19} {
		t.Run(strconv.FormatInt(id, 10), func(t *testing.T) {
			permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(fmt.Sprintf("catalog.signed%d", -id), "Signed key"))
			if err != nil {
				t.Fatal(err)
			}
			storedKey(t, backend, "permission", permission.ID, id)
			permission.ID = id
			bound := catalogBoundForm(t, resource, map[string][]string{"code": {permission.Code}, "name": {"Changed"}}, &permission)
			if err := resource.CheckChangeForm(t.Context(), actor, id, 1, bound); err != nil {
				t.Fatal("existing signed key treated as create", err)
			}
			requireProfileCode(t, resource.CheckCreateForm(t.Context(), actor, bound), accountmodel.CodeInvalidInput)
		})
	}
}
