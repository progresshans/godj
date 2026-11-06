package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
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
	"github.com/progresshans/godj/orm"
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
	formValidationSessions(t, backend, f.credential)
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
	for _, mode := range []string{"partial_unique", "permission_change_partial_unique", "current_excluded", "stale", "missing", "group_selection", "group_change_partial_unique", "group_current_excluded", "group_stale", "group_missing", "revoked", "additional"} {
		t.Run(mode, func(t *testing.T) {
			bound := catalogBoundForm(t, permissions, map[string][]string{"code": {permission.Code}, "name": {""}}, nil)
			if bound.Form().Valid() {
				t.Fatal("partial-invalid fixture is valid")
			}
			expected := formNativeSnapshot(t, backend)
			var err error
			switch mode {
			case "partial_unique":
				err = permissions.CheckCreateForm(t.Context(), actor, bound)
				failures, rejected := validation.Rejected(err)
				if !rejected || failures.ByField("code").Empty() || !failures.ByField("name").Empty() {
					t.Fatal("valid code uniqueness was skipped or old errors repeated", err)
				}
			case "permission_change_partial_unique":
				current := f.account.grants[0]
				bound = catalogBoundForm(t, permissions, map[string][]string{"code": {permission.Code}, "name": {""}}, &current)
				err = permissions.CheckChangeForm(t.Context(), actor, current.ID, current.Revision, bound)
				failures, rejected := validation.Rejected(err)
				if !rejected || failures.Len() != 1 || failures.ByField("code").Len() != 1 {
					t.Fatal("partial permission change skipped independent uniqueness", err)
				}
				combined, applyErr := bound.WithErrors(failures)
				if applyErr != nil || combined.Form().Errors().Len() != 2 || combined.Form().Errors().ByField("name").Len() != 1 {
					t.Fatal("permission change replaced field diagnostics", applyErr)
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
			case "group_change_partial_unique":
				current := f.account.group
				bound = catalogBoundForm(t, groups, map[string][]string{"name": {group.Name}, "permissions": {"missing"}}, &current)
				err = groups.CheckChangeForm(t.Context(), actor, current.ID, current.Revision, bound)
				failures, rejected := validation.Rejected(err)
				if !rejected || failures.Len() != 1 || failures.ByField("name").Len() != 1 {
					t.Fatal("partial group change skipped independent uniqueness", err)
				}
				combined, applyErr := bound.WithErrors(failures)
				if applyErr != nil || combined.Form().Errors().Len() != 2 || combined.Form().Errors().ByField("permissions").Len() != 1 {
					t.Fatal("group change replaced field diagnostics", applyErr)
				}
			case "group_current_excluded", "group_stale", "group_missing":
				current := group
				if mode == "group_stale" {
					current.Revision++
				}
				if mode == "group_missing" {
					current.ID = mathMaxKey
				}
				bound = catalogBoundForm(t, groups, map[string][]string{"name": {group.Name}}, &current)
				err = groups.CheckChangeForm(t.Context(), actor, current.ID, current.Revision, bound)
				if mode == "group_current_excluded" {
					if err != nil {
						t.Fatal("group advisory uniqueness included the current row", err)
					}
				} else {
					code := accountmodel.CodeConflict
					if mode == "group_missing" {
						code = accountmodel.CodeNotFound
					}
					requireProfileCode(t, err, code)
				}
			case "revoked":
				link := f.grants[catalogConfig().Permission.Permissions.Add]
				if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &link); err != nil || count != 1 {
					t.Fatal(err)
				}
				expected = formNativeSnapshot(t, backend)
				t.Cleanup(func() {
					restored, err := models.UserPermissionsLinkObjects.Create(context.Background(), backend, models.NewUserPermissionsLinkCreate(link.SourceID, link.TargetID))
					if err != nil {
						t.Error(err)
					}
					f.grants[catalogConfig().Permission.Permissions.Add] = restored
				})
				err = permissions.CheckCreateForm(t.Context(), actor, bound)
				requireProfileCode(t, err, accountmodel.CodePermission)
			case "additional":
				config := catalogConfig()
				config.Permission.Permissions.Additional = []auth.Permission{"catalog.extra"}
				owner, err := project.NewAccountCatalog(catalogSessions(t, backend), auth.PrincipalAuthorizer{}, config)
				if err != nil {
					t.Fatal(err)
				}
				requireProfileCode(t, owner.Permissions().CheckCreateForm(t.Context(), actor, bound), accountmodel.CodePermission)
			}
			if !reflect.DeepEqual(expected, formNativeSnapshot(t, backend)) {
				t.Fatal("catalog advisory validation changed native state")
			}
		})
	}
	for _, operation := range []string{"create", "change"} {
		for _, mode := range []string{"stale_selection", "unordered_selection"} {
			t.Run("group_"+operation+"_"+mode, func(t *testing.T) {
				var current *models.Group
				if operation == "change" {
					row := f.account.group
					current = &row
				}
				name := "Advisory group"
				if mode == "stale_selection" {
					name = group.Name
				}
				keys := []string{strconv.FormatInt(permission.ID, 10), strconv.FormatInt(f.account.grants[0].ID, 10), strconv.FormatInt(permission.ID, 10)}
				bound := catalogBoundForm(t, groups, map[string][]string{"name": {name}, "permissions": keys}, current, permission.ID, f.account.grants[0].ID)
				if !bound.Form().Valid() {
					t.Fatal("catalog selection did not pass its independent input choices")
				}
				cleaned, _ := bound.Form().Cleaned().Integers("permissions")
				if mode == "stale_selection" {
					removed := permission
					if count, err := models.PermissionObjects.Delete(t.Context(), reopen(t), &removed); err != nil || count != 1 {
						t.Fatal("separate writer did not remove the selected permission", err)
					}
					t.Cleanup(func() {
						restored := permission
						if err := models.PermissionObjects.Save(context.Background(), backend, &restored, orm.ForceInsert[models.Permission]()); err != nil {
							t.Error(err)
						}
					})
				}
				before := formNativeSnapshot(t, backend)
				var err error
				if current == nil {
					err = groups.CheckCreateForm(t.Context(), actor, bound)
				} else {
					err = groups.CheckChangeForm(t.Context(), actor, current.ID, current.Revision, bound)
				}
				if mode == "stale_selection" {
					failures, rejected := validation.Rejected(err)
					if !rejected || failures.Len() != 2 || failures.ByField("name").Len() != 1 || failures.ByField("permissions").Len() != 1 {
						t.Fatal("stale choices suppressed independent uniqueness or remained valid", err)
					}
				} else if err != nil {
					t.Fatal("unordered duplicate choices were not normalized for validation", err)
				}
				after, _ := bound.Form().Cleaned().Integers("permissions")
				if !slices.Equal(cleaned, after) || !reflect.DeepEqual(before, formNativeSnapshot(t, backend)) {
					t.Fatal("advisory choice validation mutated input or native state")
				}
			})
		}
	}
	groupData := map[string][]string{"name": {group.Name}, "permissions": {"missing"}}
	permissionData := map[string][]string{"code": {permission.Code}, "name": {""}}
	groupCreate := catalogBoundForm(t, groups, groupData, nil)
	groupChange := catalogBoundForm(t, groups, groupData, &f.account.group)
	permissionCreate := catalogBoundForm(t, permissions, permissionData, nil)
	permissionChange := catalogBoundForm(t, permissions, permissionData, &f.account.grants[0])
	for _, probe := range []struct {
		name  string
		grant auth.Permission
		check func() error
	}{
		{"group_create", catalogConfig().Group.Permissions.Add, func() error { return groups.CheckCreateForm(t.Context(), actor, groupCreate) }},
		{"group_change", catalogConfig().Group.Permissions.Change, func() error {
			return groups.CheckChangeForm(t.Context(), actor, f.account.group.ID, f.account.group.Revision, groupChange)
		}},
		{"permission_create", catalogConfig().Permission.Permissions.Add, func() error { return permissions.CheckCreateForm(t.Context(), actor, permissionCreate) }},
		{"permission_change", catalogConfig().Permission.Permissions.Change, func() error {
			return permissions.CheckChangeForm(t.Context(), actor, f.account.grants[0].ID, f.account.grants[0].Revision, permissionChange)
		}},
	} {
		t.Run("current_authority_"+probe.name, func(t *testing.T) {
			if _, rejected := validation.Rejected(probe.check()); !rejected {
				t.Fatal("current-authority control did not produce private diagnostics")
			}
			link, exists := f.grants[probe.grant]
			if !exists {
				t.Fatal("catalog authority fixture has no direct grant", probe.grant)
			}
			if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), reopen(t), &link); err != nil || count != 1 {
				t.Fatal("separate writer did not revoke catalog authority", err)
			}
			t.Cleanup(func() {
				restored, err := models.UserPermissionsLinkObjects.Create(context.Background(), backend, models.NewUserPermissionsLinkCreate(link.SourceID, link.TargetID))
				if err != nil {
					t.Error(err)
				}
				f.grants[probe.grant] = restored
			})
			before := formNativeSnapshot(t, backend)
			err := probe.check()
			requireProfileCode(t, err, accountmodel.CodePermission)
			if _, rejected := validation.Rejected(err); rejected || !reflect.DeepEqual(before, formNativeSnapshot(t, backend)) {
				t.Fatal("cached authority disclosed diagnostics or changed native state", err)
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
	formValidationSessions(t, backend, f.credential)
	permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("catalog.form_failure", "Existing"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "denial_cleanup", "conflict_cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			fault := errors.New("private form snapshot details")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var retained db.Queryer
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
					retained = scope
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
			before := formNativeSnapshot(t, backend)
			if mode == "conflict_cleanup" {
				stale := permission
				stale.Revision++
				bound = catalogBoundForm(t, resource, map[string][]string{"code": {permission.Code}, "name": {""}}, &stale)
				err = resource.CheckChangeForm(ctx, actor, stale.ID, stale.Revision, bound)
			} else {
				err = resource.CheckCreateForm(ctx, actor, bound)
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if _, rejected := validation.Rejected(err); rejected {
				t.Fatal("failed snapshot disclosed input diagnostics or private cause")
			}
			for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, err), fault.Error()) {
					t.Fatal("advisory failure exposed private backend details")
				}
			}
			if mode == "query" || mode == "close" || mode == "swallowed" || strings.HasSuffix(mode, "cleanup") {
				if !errors.Is(err, fault) {
					t.Fatal("read failure lost cause", err)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("read failure lost cancellation")
			}
			if retained != nil {
				if _, err := models.PermissionObjects.Using(retained).Count(t.Context()); err == nil {
					t.Fatal("original catalog validation reader survived its scope")
				}
			}
			if !reflect.DeepEqual(before, formNativeSnapshot(t, backend)) {
				t.Fatal("failed catalog advisory scope changed native state")
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
