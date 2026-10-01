package identitytest

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

func formCatalogSnapshot(t *testing.T, f *managementFixture) []any {
	t.Helper()
	sessions, audit := snapshotIdentitySystemRows(t, f.backend)
	return []any{
		catalogRows(t, models.UserObjects.Using(f.backend).OrderBy(models.UserFields.ID.Asc())),
		catalogRows(t, models.GroupObjects.Using(f.backend).OrderBy(models.GroupFields.ID.Asc())),
		catalogRows(t, models.PermissionObjects.Using(f.backend).OrderBy(models.PermissionFields.ID.Asc())),
		catalogRows(t, models.UserGroupsLinkObjects.Using(f.backend).OrderBy(models.UserGroupsLinkFields.ID.Asc())),
		catalogRows(t, models.UserPermissionsLinkObjects.Using(f.backend).OrderBy(models.UserPermissionsLinkFields.ID.Asc())),
		catalogRows(t, models.GroupPermissionsLinkObjects.Using(f.backend).OrderBy(models.GroupPermissionsLinkFields.ID.Asc())),
		sessions, audit,
	}
}

func runCatalogModelValidation(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	t.Run("catalog_model_validation", func(t *testing.T) {
		for _, target := range []string{"group_create", "group_change", "permission_create", "permission_change"} {
			t.Run(target, func(t *testing.T) {
				group, change := strings.HasPrefix(target, "group_"), strings.HasSuffix(target, "_change")
				modes := []string{"duplicate", "field_error", "authority_changed", "read_failure", "cleanup_failure", "wrong_model", "wrong_key", "wrong_revision", "unbound", "canceled"}
				if change {
					modes = append(modes, "same_row", "revision_changed", "missing_row")
				}
				if group {
					modes = append(modes, "stale_permissions", "unordered_permissions")
				}
				for _, mode := range modes {
					t.Run(mode, func(t *testing.T) {
						backend, second := open(t)
						f := newManagementFixture(t, backend, 3)
						currentGroup, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Current group"))
						if err != nil {
							t.Fatal(err)
						}
						otherGroup, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Used group"))
						if err != nil {
							t.Fatal(err)
						}
						currentPermission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("form.current", "Current permission"))
						if err != nil {
							t.Fatal(err)
						}
						otherPermission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("form.used", "Used permission"))
						if err != nil {
							t.Fatal(err)
						}
						metadata := models.PermissionDescriptor{}.Metadata()
						fields := []string{"code", "name"}
						keyField, invalidField := "code", "name"
						id := currentPermission.ID
						data := map[string][]string{"code": {otherPermission.Code}, "name": {"Candidate"}}
						if group {
							metadata, fields = models.GroupDescriptor{}.Metadata(), []string{"name", "permissions"}
							keyField, invalidField, id = "name", "permissions", currentGroup.ID
							data = map[string][]string{"name": {otherGroup.Name}}
						}
						if mode == "wrong_model" {
							metadata.Name = "foreign_model"
						}
						spec, err := formmodel.NewSpecForFields(metadata, fields)
						if err != nil {
							t.Fatal(err)
						}
						if group {
							spec, err = spec.WithModelChoices("permissions", forms.Choice{Value: forms.Integer(currentPermission.ID), Label: currentPermission.Name}, forms.Choice{Value: forms.Integer(otherPermission.ID), Label: otherPermission.Name})
							if err != nil {
								t.Fatal(err)
							}
						}
						initial := map[string]forms.Value{}
						if change {
							initial["id"], initial["revision"] = forms.Integer(id), forms.Integer(1)
						}
						switch mode {
						case "wrong_key":
							initial["id"] = forms.Integer(id + 1)
						case "wrong_revision":
							initial["revision"] = forms.Integer(2)
						case "field_error":
							data[invalidField] = []string{""}
							if group {
								data[invalidField] = []string{"missing"}
							}
						case "same_row":
							data[keyField] = []string{currentPermission.Code}
							if group {
								data[keyField] = []string{currentGroup.Name}
							}
						case "stale_permissions":
							data["permissions"] = []string{strconv.FormatInt(otherPermission.ID, 10)}
						case "unordered_permissions":
							data["name"] = []string{"New group"}
							data["permissions"] = []string{strconv.FormatInt(otherPermission.ID, 10), strconv.FormatInt(currentPermission.ID, 10), strconv.FormatInt(otherPermission.ID, 10)}
						}
						bound, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(data), initial, formmodel.PostClean{})
						if err != nil {
							t.Fatal(err)
						}
						if mode == "field_error" && bound.Form().Errors().ByField(validation.Field(invalidField)).Empty() {
							t.Fatal("fixture did not fail field cleaning")
						}
						if mode == "unbound" {
							bound = formmodel.BoundForm{}
						}
						expected := formCatalogSnapshot(t, f)
						secret := errors.New("private-catalog-form-read-failure")
						boundary := &modelFormReadBoundary{ManagementBackend: f.runtime}
						if mode == "read_failure" {
							boundary.readFailure = secret
						}
						if mode == "cleanup_failure" {
							boundary.finish = secret
						}
						if mode == "authority_changed" || mode == "revision_changed" || mode == "missing_row" || mode == "stale_permissions" {
							boundary.before = func(ctx context.Context) error {
								err := second.CoordinatedAtomic(ctx, func(session db.Session) error {
									switch mode {
									case "authority_changed":
										_, err := models.UserObjects.Update(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
										return err
									case "stale_permissions":
										_, err := models.PermissionObjects.Delete(ctx, session, &otherPermission)
										return err
									case "missing_row":
										if group {
											_, err := models.GroupObjects.Delete(ctx, session, &currentGroup)
											return err
										}
										_, err := models.PermissionObjects.Delete(ctx, session, &currentPermission)
										return err
									default:
										if group {
											_, err := models.GroupObjects.Update(ctx, session, currentGroup, models.GroupPatch{}.WithRevision(2))
											return err
										}
										_, err := models.PermissionObjects.Update(ctx, session, currentPermission, models.PermissionPatch{}.WithRevision(2))
										return err
									}
								})
								if err == nil {
									expected = formCatalogSnapshot(t, f)
								}
								return err
							}
						}
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						if mode == "canceled" {
							cancel()
						}
						manager := f.manager(t, boundary)
						switch target {
						case "group_create":
							err = manager.CheckGroupCreate(ctx, f.actor, bound)
						case "group_change":
							err = manager.CheckGroupChange(ctx, f.actor, id, 1, bound)
						case "permission_create":
							err = manager.CheckPermissionCreate(ctx, f.actor, bound)
						case "permission_change":
							err = manager.CheckPermissionChange(ctx, f.actor, id, 1, bound)
						}
						failures, rejected := validation.Rejected(err)
						switch mode {
						case "same_row", "unordered_permissions":
							if err != nil {
								t.Fatal("valid catalog form rejected", err)
							}
						case "duplicate", "field_error", "stale_permissions":
							want := 1
							if mode == "stale_permissions" {
								want = 2
							}
							if !rejected || failures.Len() != want || failures.ByField(validation.Field(keyField)).Len() != 1 {
								t.Fatal("catalog model diagnostics missing", err)
							}
							checked, applyErr := bound.WithErrors(failures)
							if applyErr != nil {
								t.Fatal(applyErr)
							}
							if mode == "field_error" && checked.Form().Errors().Len() != 2 {
								t.Fatal("unrelated field error suppressed catalog uniqueness")
							}
							if mode == "stale_permissions" && failures.ByField("permissions").Len() != 1 {
								t.Fatal("stale permission selection accepted")
							}
						case "authority_changed":
							if !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || rejected {
								t.Fatal("cached actor authorized catalog validation", err)
							}
						case "revision_changed":
							if !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || rejected {
								t.Fatal("stale catalog revision accepted", err)
							}
						case "missing_row":
							if !errors.Is(err, &identity.Error{Code: identity.CodeNotFound}) || rejected {
								t.Fatal("missing catalog row accepted", err)
							}
						case "read_failure", "cleanup_failure":
							if !errors.Is(err, secret) || rejected {
								t.Fatal("catalog execution failure became input diagnostics", err)
							}
						case "canceled":
							if !errors.Is(err, context.Canceled) || rejected {
								t.Fatal("canceled catalog validation accepted", err)
							}
						default:
							if !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) || rejected {
								t.Fatal("foreign catalog candidate accepted", err)
							}
						}
						wantCalls := 1
						if strings.HasPrefix(mode, "wrong_") || mode == "unbound" || mode == "canceled" {
							wantCalls = 0
						}
						if boundary.calls != wantCalls {
							t.Fatal("unexpected catalog scope count", boundary.calls)
						}
						if boundary.reader != nil {
							if _, err := models.GroupObjects.Using(boundary.reader).Count(t.Context()); err == nil {
								t.Fatal("catalog reader escaped its scope")
							}
						}
						if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(expected, formCatalogSnapshot(t, f)) {
							t.Fatal("catalog validation changed durable state")
						}
					})
				}
			})
		}
	})
	t.Run("catalog_model_validation_http", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Used group"))
		if err != nil {
			t.Fatal(err)
		}
		otherGroup, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Current group"))
		if err != nil {
			t.Fatal(err)
		}
		permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("form.used", "Used permission"))
		if err != nil {
			t.Fatal(err)
		}
		otherPermission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("form.current", "Current permission"))
		if err != nil {
			t.Fatal(err)
		}
		h := newIdentityAdminHTTP(t, f, f.runtime)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		for _, target := range []struct {
			path, key, invalid string
			data               url.Values
		}{
			{"/admin/groups/add/", "name", "permissions", url.Values{"name": {group.Name}, "permissions": {"missing"}}},
			{adminObjectPath("groups", "change", otherGroup.ID), "name", "permissions", url.Values{"name": {group.Name}, "permissions": {"missing"}, "expected_revision": {"1"}}},
			{"/admin/permissions/add/", "code", "name", url.Values{"code": {permission.Code}, "name": {""}}},
			{adminObjectPath("permissions", "change", otherPermission.ID), "code", "name", url.Values{"code": {permission.Code}, "name": {""}, "expected_revision": {"1"}}},
		} {
			h.call(t, h.client, "GET", target.path, nil, 200)
			before := formCatalogSnapshot(t, f)
			h.postForm(t, target.path, target.data, 200).contains(t, `data-error-field="`+target.key+`" data-error-code="unique"`, `data-error-field="`+target.invalid+`"`)
			if !reflect.DeepEqual(before, formCatalogSnapshot(t, f)) || f.hasher.calls.Load() != 0 {
				t.Fatal("invalid catalog HTTP form changed durable state")
			}
		}
	})
}
