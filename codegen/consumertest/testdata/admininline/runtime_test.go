package consumer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"example.com/godj-project-bundle/directory"
	"example.com/godj-project-bundle/inventory"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const workspacePermission auth.Permission = "directory.change_workspace"

func assetPermission() auth.Permission {
	if evolved {
		return "inventory.manage_asset"
	}
	return "inventory.change_asset"
}

// Requests are synchronous. Hooks introduce a stored change after HTTP
// validation and before the writer's borrowed transaction, never via a mock DB.
type inlineHarness struct {
	t                         *testing.T
	backend                   resourceBackend
	application               *web.Application
	jar                       http.CookieJar
	workspace                 directory.Workspace
	asset                     inventory.Asset
	rows                      func(context.Context, db.Queryer, int64) ([]inventory.Asset, error)
	beforeWrite               func()
	writerCalls, currentLoads int
	saved, deleted, failAfter int
	borrowed                  db.Session
}

type workspaceWrite struct {
	value   directory.Workspace
	changed []string
}

func newInlineHarness(t *testing.T, backend resourceBackend, name string) *inlineHarness {
	t.Helper()
	h := &inlineHarness{t: t, backend: backend}
	var err error
	h.workspace, err = directory.WorkspaceObjects.Create(t.Context(), backend, directory.NewWorkspaceCreate(name))
	check(t, err)
	h.asset, err = inventory.AssetObjects.Create(t.Context(), backend, inventory.NewAssetCreate(name+"-base", h.workspace.ID))
	check(t, err)
	binding, err := project.Bind()
	check(t, err)
	relations, err := project.BindRelations()
	check(t, err)
	h.rows = func(ctx context.Context, reader db.Queryer, id int64) ([]inventory.Asset, error) {
		rows, err := inventory.AssetObjects.Using(reader).Filter(relations.InventoryAsset.Workspace.ID.Exact(id)).OrderBy(inventory.AssetFields.ID.Asc()).Limit(5)
		if err != nil {
			return nil, err
		}
		values, err := rows.All(ctx)
		if err == nil && len(values) > 4 {
			err = errors.New("inline consumer cohort exceeded its bound")
		}
		return values, err
	}
	fields := []string{"code", "enabled"}
	if evolved {
		fields = append(fields, "priority")
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "assets", 0, 4, 5
	policy.CanDelete, policy.ValidateMax = true, true
	inline, err := admin.NewInline(admin.InlineConfig[directory.Workspace, inventory.Asset]{
		Project: binding, Parent: directory.WorkspaceObjects, Child: inventory.AssetObjects,
		ParentWithID: directory.NewWorkspaceWithID, ForeignKey: "workspace", Label: "Assets", Set: policy,
		Form: admin.FormConfig{Definition: formmodel.Definition{Fields: fields, Overrides: []formmodel.Override{
			formmodel.OverrideField("code", formmodel.WithStringNormalizer(strings.ToUpper)),
		}}},
		Permissions: admin.Permissions{View: "inventory.view_asset", Add: "inventory.add_asset", Change: assetPermission(), Delete: "inventory.delete_asset"},
		Load: func(ctx context.Context, _ auth.Principal, id int64) (admin.InlineSnapshot[inventory.Asset], error) {
			rows, err := h.rows(ctx, backend, id)
			return admin.InlineSnapshot[inventory.Asset]{Current: rows}, err
		},
	})
	check(t, err)
	parent, err := crud.New(directory.WorkspaceObjects, crud.Config{Fields: []string{"name"}})
	check(t, err)
	parentModel := (directory.WorkspaceDescriptor{}).Metadata()
	parentForm, err := formmodel.NewSpecForFields(parentModel, parent.Fields())
	check(t, err)
	projector, err := admin.NewModelProjector(parentModel, (directory.WorkspaceDescriptor{}).WriteFieldValue, "id", "name")
	check(t, err)
	save := func(ctx context.Context, actor auth.Principal, id int64, input formmodel.BoundForm, submission admin.InlineSubmission) (workspaceWrite, error) {
		h.writerCalls++
		if !actor.Has(workspacePermission) {
			return workspaceWrite{}, admin.NewOperationError(admin.OperationDenied, nil)
		}
		if h.beforeWrite != nil {
			hook := h.beforeWrite
			h.beforeWrite = nil
			hook()
		}
		return db.RelationAtomicResult(ctx, backend, func(work context.Context, session db.RelationSession) (workspaceWrite, error) {
			h.borrowed = session
			var current directory.Workspace
			if id != 0 {
				value, found, err := parent.Get(work, session, id)
				if err != nil {
					return workspaceWrite{}, err
				}
				if !found {
					return workspaceWrite{}, admin.ErrObjectNotFound
				}
				if !value.Writable {
					return workspaceWrite{}, admin.NewOperationError(admin.OperationDenied, nil)
				}
				current = value
			}
			bound, present, err := inline.BindCurrent(work, actor, submission, func(ctx context.Context, access admin.InlineAccess) (admin.InlineCurrent[directory.Workspace, inventory.Asset], error) {
				h.currentLoads++
				result := admin.InlineCurrent[directory.Workspace, inventory.Asset]{Parent: current}
				if id != 0 && (access.View || access.Change) {
					var err error
					result.Children.Current, err = h.rows(ctx, session, id)
					return result, err
				}
				return result, nil
			})
			if err != nil {
				return workspaceWrite{}, err
			}
			if !present || !bound.Set().Valid() {
				return workspaceWrite{}, admin.RejectInline("assets", -1, validation.NewErrors(validation.New(validation.NonField, "stale_inline")), nil)
			}
			preparedInput, err := formmodel.PrepareInput(directory.WorkspaceObjects, input)
			if err != nil {
				return workspaceWrite{}, err
			}
			var mutation crud.Mutation[directory.Workspace]
			if id == 0 {
				mutation, err = parent.CreateInSession(work, session, preparedInput, crud.SaveOptions[directory.Workspace]{})
			} else {
				mutation, err = parent.UpdateInSession(work, session, id, preparedInput, crud.SaveOptions[directory.Workspace]{})
			}
			if err != nil {
				return workspaceWrite{}, err
			}
			prepared, err := bound.Set().PrepareWithParent(mutation.Value)
			if err != nil {
				return workspaceWrite{}, err
			}
			plan, err := prepared.SavePlan()
			if err != nil {
				return workspaceWrite{}, err
			}
			writes, err := plan.Save(work, session, formmodel.SetSaveOptions[inventory.Asset]{
				Save: func(ctx context.Context, write db.Session, row *formmodel.SetSaveRow[inventory.Asset]) error {
					admitted, permission := bound.Access().Add, auth.Permission("inventory.add_asset")
					if row.Existing() {
						admitted, permission = bound.Access().Change, assetPermission()
					}
					if !admitted || !actor.Has(permission) || write != session || ctx != work {
						return admin.NewOperationError(admin.OperationDenied, nil)
					}
					if err := row.Save(ctx, write); err != nil {
						return err
					}
					h.saved++
					if h.failAfter != 0 && h.saved == h.failAfter {
						return errors.New("synthetic late audit failure")
					}
					return nil
				},
				Delete: func(ctx context.Context, write db.Session, row formmodel.DeletedSetRow[inventory.Asset]) error {
					if !bound.Access().Delete || !actor.Has("inventory.delete_asset") || write != session || ctx != work {
						return admin.NewOperationError(admin.OperationDenied, nil)
					}
					value, err := row.Model()
					if err != nil {
						return err
					}
					// Asset has no incoming relation in either generated project.
					if _, err = inventory.AssetObjects.Delete(ctx, write, &value); err == nil {
						h.deleted++
					}
					return err
				},
			})
			if err != nil {
				return workspaceWrite{}, err
			}
			changed := mutation.ChangedFields
			if len(writes) != 0 {
				changed = append(changed, "assets")
			}
			return workspaceWrite{value: mutation.Value, changed: changed}, nil
		})
	}
	configured, err := settings.New(settings.Definition{ProjectName: "inline_consumer", InstalledApps: []apps.Config{
		{Name: "example.com/godj-project-bundle/directory", Label: "directory"},
		{Name: "example.com/godj-project-bundle/inventory", Label: "inventory"},
	}})
	check(t, err)
	builder := admin.NewBuilder(configured.Apps())
	check(t, admin.RegisterModel(builder, admin.ModelConfig[directory.Workspace]{
		AppLabel: "directory", Slug: "workspaces", Model: parentModel, FormFields: parent.Fields(),
		ListFields: []string{"id", "name"}, Inlines: []admin.Inline{inline.View()},
		Permissions: admin.Permissions{View: "directory.view_workspace", Add: workspacePermission, Change: workspacePermission, Delete: workspacePermission},
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[directory.Workspace], error) {
			page, err := parent.List(ctx, backend, crud.ListOptions[directory.Workspace]{Offset: request.Offset, Limit: request.Limit})
			return admin.Page[directory.Workspace]{Items: page.Items, Total: page.Total, Offset: page.Offset, Limit: page.Limit}, err
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (directory.Workspace, bool, error) {
			return parent.Get(ctx, backend, id)
		},
		Snapshot: func(value directory.Workspace) (admin.Object, error) {
			return projector.Project(value, value.ID, value.Name)
		},
		Initial: func(value directory.Workspace) (map[string]forms.Value, error) {
			return formmodel.InitialValues(parentModel, parentForm, value, (directory.WorkspaceDescriptor{}).WriteFieldValue)
		},
		Create: func(ctx context.Context, actor auth.Principal, input formmodel.BoundForm, submission admin.InlineSubmission) (directory.Workspace, error) {
			result, err := save(ctx, actor, 0, input, submission)
			return result.value, err
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, input formmodel.BoundForm, submission admin.InlineSubmission) (directory.Workspace, []string, error) {
			result, err := save(ctx, actor, mutation.ID, input, submission)
			return result.value, result.changed, err
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (directory.Workspace, error) {
			return directory.Workspace{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
	}))
	registry, err := builder.Build()
	check(t, err)
	store, err := sessions.NewMemoryStore(8)
	check(t, err)
	manager, err := sessions.NewManager(store, sessions.Config{})
	check(t, err)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	check(t, err)
	password, err := hasher.Hash(t.Context(), "inline-test-password")
	check(t, err)
	var credentials []auth.Credential
	for _, name := range []string{"editor", "readonly"} {
		permissions := []auth.Permission{"directory.view_workspace", workspacePermission, "inventory.view_asset", "inventory.add_asset"}
		if name == "editor" {
			permissions = append(permissions, assetPermission(), "inventory.delete_asset")
		}
		actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: name, Active: true, Staff: true, Permissions: permissions})
		check(t, err)
		credential, err := auth.NewCredential(name, password, actor)
		check(t, err)
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
	check(t, err)
	allowed, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(t, err)
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: allowed})
	check(t, err)
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "inventory", Registry: registry, Auth: runtime})
	check(t, err)
	h.application, err = web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	check(t, err)
	h.login("editor")
	return h
}

var csrfToken = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

func token(t *testing.T, page *httptest.ResponseRecorder) string {
	t.Helper()
	match := csrfToken.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || len(match) != 2 {
		t.Fatal("inline page has no CSRF token", page.Code, page.Body.String())
	}
	return match[1]
}

func (h *inlineHarness) send(method, path string, values url.Values) *httptest.ResponseRecorder {
	h.t.Helper()
	location, err := url.Parse("http://inline.test" + path)
	check(h.t, err)
	request := httptest.NewRequest(method, location.String(), strings.NewReader(values.Encode()))
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range h.jar.Cookies(location) {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	h.application.ServeHTTP(response, request)
	h.jar.SetCookies(location, response.Result().Cookies())
	return response
}

func (h *inlineHarness) login(actor string) {
	h.t.Helper()
	var err error
	h.jar, err = cookiejar.New(nil)
	check(h.t, err)
	page := h.send(http.MethodGet, "/admin/login/", nil)
	response := h.send(http.MethodPost, "/admin/login/", url.Values{"username": {actor}, "password": {"inline-test-password"}, "csrfmiddlewaretoken": {token(h.t, page)}})
	if response.Code != http.StatusFound {
		h.t.Fatal("inline consumer login failed", response.Code)
	}
}

func (h *inlineHarness) input(code string, extra bool) url.Values {
	values := url.Values{"name": {h.workspace.Name + " changed"}, "assets-TOTAL_FORMS": {"1"}, "assets-INITIAL_FORMS": {"1"},
		"assets-0-id": {strconv.FormatInt(h.asset.ID, 10)}, "assets-0-workspace": {strconv.FormatInt(h.workspace.ID, 10)}, "assets-0-code": {code}, "assets-0-enabled": {"on"}}
	if evolved {
		values.Set("assets-0-priority", "9")
	}
	if extra {
		values.Set("assets-TOTAL_FORMS", "2")
		values.Set("assets-1-code", h.workspace.Name+"-extra")
		if evolved {
			values.Set("assets-1-priority", "1")
		}
	}
	return values
}

func (h *inlineHarness) submit(path string, values url.Values) *httptest.ResponseRecorder {
	h.t.Helper()
	values.Set("csrfmiddlewaretoken", token(h.t, h.send(http.MethodGet, path, nil)))
	return h.send(http.MethodPost, path, values)
}

func (h *inlineHarness) path() string {
	return fmt.Sprintf("/admin/workspaces/change/?id=%d", h.workspace.ID)
}

func (h *inlineHarness) current() (directory.Workspace, []inventory.Asset) {
	h.t.Helper()
	parent, found, err := directory.WorkspaceObjects.Using(h.backend).Filter(directory.WorkspaceFields.ID.Exact(h.workspace.ID)).OrderBy(directory.WorkspaceFields.ID.Asc()).First(h.t.Context())
	check(h.t, err)
	if !found {
		h.t.Fatal("stored parent disappeared")
	}
	rows, err := h.rows(h.t.Context(), h.backend, parent.ID)
	check(h.t, err)
	return parent, rows
}

func TestAdminInlineConsumer(t *testing.T) {
	withResourceBackends(t, func(t *testing.T, backend resourceBackend) {
		t.Run("current_values_and_changed_model", func(t *testing.T) {
			h := newInlineHarness(t, backend, "fresh")
			page := h.send(http.MethodGet, h.path(), nil)
			if page.Code != http.StatusOK || strings.Contains(page.Body.String(), `name="assets-0-priority"`) != evolved || strings.Contains(page.Body.String(), `name="assets-0-stamp"`) {
				t.Fatal("inline did not use its selected generated model fields", page.Code)
			}
			values := h.input(" fresh-new ", false)
			if evolved {
				values.Set("assets-0-priority", "2")
				response := h.submit(h.path(), values)
				if response.Code != http.StatusOK || h.writerCalls != 0 || !strings.Contains(response.Body.String(), "invalid_choice") {
					t.Fatal("changed model choices did not reject before the writer", response.Code, h.writerCalls)
				}
				values.Set("assets-0-priority", "9")
			}
			h.beforeWrite = func() {
				value := h.asset
				value.Note, value.Stamp = new("fresh excluded note"), "fresh server stamp"
				check(t, inventory.AssetObjects.Save(t.Context(), backend, &value))
			}
			response := h.submit(h.path(), values)
			parent, rows := h.current()
			if response.Code != http.StatusFound || parent.Name != "fresh changed" || len(rows) != 1 || rows[0].Code != "FRESH-NEW" || rows[0].Note == nil || *rows[0].Note != "fresh excluded note" || rows[0].Stamp != "fresh server stamp" || h.writerCalls != 1 || h.currentLoads != 1 || h.saved != 1 {
				t.Fatal("current inline lost its shared normalizer, fresh data or write scope", response.Code, parent, rows, h.writerCalls, h.currentLoads, h.saved)
			}
			modelValues, err := inventory.AssetObjects.ModelValues(rows[0])
			check(t, err)
			if value, found := modelValues["priority"]; found != evolved || evolved && !value.Equal(query.Integer(9)) {
				t.Fatal("changed inline needed a separate writer field declaration")
			}
			if _, err := inventory.AssetObjects.Using(h.borrowed).Count(context.Background()); err == nil {
				t.Fatal("completed inline retained a usable borrowed session")
			}
		})
		t.Run("pending_parent_and_delete", func(t *testing.T) {
			h := newInlineHarness(t, backend, "new")
			values := url.Values{"name": {"pending workspace"}, "assets-TOTAL_FORMS": {"1"}, "assets-INITIAL_FORMS": {"0"}, "assets-0-code": {"created"}}
			if evolved {
				values.Set("assets-0-priority", "9")
			}
			response := h.submit("/admin/workspaces/add/", values)
			parent, found, err := directory.WorkspaceObjects.Using(backend).Filter(directory.WorkspaceFields.Name.Exact("pending workspace")).OrderBy(directory.WorkspaceFields.ID.Asc()).First(t.Context())
			check(t, err)
			rows, err := h.rows(t.Context(), backend, parent.ID)
			check(t, err)
			if response.Code != http.StatusFound || !found || len(rows) != 1 || rows[0].WorkspaceID != parent.ID || rows[0].Code != "CREATED" || rows[0].Enabled || rows[0].Note != nil || rows[0].Stamp != "server" {
				t.Fatal("new parent binding or model defaults failed", response.Code, parent, rows)
			}
			remove := h.input("ignored", false)
			remove.Set("assets-0-DELETE", "on")
			response = h.submit(h.path(), remove)
			_, rows = h.current()
			if response.Code != http.StatusFound || len(rows) != 0 || h.deleted != 1 {
				t.Fatal("current inline deletion failed", response.Code, rows, h.deleted)
			}
		})
		t.Run("late_failure_rolls_back", func(t *testing.T) {
			h := newInlineHarness(t, backend, "late")
			h.failAfter = 2
			response := h.submit(h.path(), h.input("late-new", true))
			parent, rows := h.current()
			if response.Code != http.StatusInternalServerError || h.saved != 2 || !reflect.DeepEqual(parent, h.workspace) || !reflect.DeepEqual(rows, []inventory.Asset{h.asset}) {
				t.Fatal("late failure committed a parent or child write", response.Code, h.saved, parent, rows)
			}
		})
		t.Run("current_scope_denial", func(t *testing.T) {
			h := newInlineHarness(t, backend, "deny")
			h.beforeWrite = func() {
				current := h.workspace
				current.Writable = false
				check(t, directory.WorkspaceObjects.Save(t.Context(), backend, &current))
			}
			response := h.submit(h.path(), h.input("denied", true))
			parent, rows := h.current()
			if response.Code != http.StatusForbidden || parent.Name != h.workspace.Name || parent.Writable || !reflect.DeepEqual(rows, []inventory.Asset{h.asset}) || h.currentLoads != 0 || h.saved != 0 {
				t.Fatal("revoked stored scope reached child loading or persistence", response.Code, parent, rows, h.currentLoads, h.saved)
			}
		})
		t.Run("foreign_parent_and_cohort", func(t *testing.T) {
			h := newInlineHarness(t, backend, "keys")
			other, err := directory.WorkspaceObjects.Create(t.Context(), backend, directory.NewWorkspaceCreate("foreign workspace"))
			check(t, err)
			foreign, err := inventory.AssetObjects.Create(t.Context(), backend, inventory.NewAssetCreate("foreign", other.ID))
			check(t, err)
			for _, mode := range []string{"parent", "child", "management", "new identity"} {
				values := h.input("forged", mode == "new identity")
				switch mode {
				case "parent":
					values.Set("assets-0-workspace", strconv.FormatInt(other.ID, 10))
				case "child":
					values.Set("assets-0-id", strconv.FormatInt(foreign.ID, 10))
				case "management":
					values.Set("assets-INITIAL_FORMS", "0")
				case "new identity":
					values.Set("assets-1-id", strconv.FormatInt(foreign.ID, 10))
				}
				response := h.submit(h.path(), values)
				parent, rows := h.current()
				if response.Code != http.StatusOK || h.writerCalls != 0 || !reflect.DeepEqual(parent, h.workspace) || !reflect.DeepEqual(rows, []inventory.Asset{h.asset}) {
					t.Fatal("forged inline identity reached its writer", mode, response.Code, h.writerCalls, parent, rows)
				}
			}
			h.beforeWrite = func() {
				_, err := inventory.AssetObjects.Create(t.Context(), backend, inventory.NewAssetCreate("new-cohort", h.workspace.ID))
				check(t, err)
			}
			response := h.submit(h.path(), h.input("stale", false))
			parent, rows := h.current()
			if response.Code != http.StatusOK || parent.Name != h.workspace.Name || len(rows) != 2 || !reflect.DeepEqual(rows[0], h.asset) || h.writerCalls != 1 || h.currentLoads != 1 || h.saved != 0 {
				t.Fatal("cohort changed after admission without rejecting the stale write", response.Code, parent, rows, h.writerCalls, h.currentLoads, h.saved)
			}
		})
		t.Run("read_only_initial_and_new_rows", func(t *testing.T) {
			h := newInlineHarness(t, backend, "ro")
			h.login("readonly")
			response := h.submit(h.path(), h.input("forged", true))
			parent, rows := h.current()
			if response.Code != http.StatusFound || parent.Name != "ro changed" || len(rows) != 2 || !reflect.DeepEqual(rows[0], h.asset) || rows[1].Code != "RO-EXTRA" || h.saved != 1 {
				t.Fatal("read-only initial row was changed or denied a separately admitted new row", response.Code, parent, rows, h.saved)
			}
		})
	})
}
