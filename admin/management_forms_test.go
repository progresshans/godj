package admin

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type managementFormRow struct {
	id       int64
	username string
	active   bool
	revision int64
}
type managementFormState struct {
	mu            sync.Mutex
	row           managementFormRow
	found         bool
	reads, writes int
	actor         string
	password      string
	race, unknown bool
}

type managementFormAuthorizer func(context.Context, auth.Principal, auth.Permission) (bool, error)

func (f managementFormAuthorizer) Allowed(ctx context.Context, p auth.Principal, permission auth.Permission) (bool, error) {
	return f(ctx, p, permission)
}

func newManagementFormSite(t *testing.T, authorizer auth.Authorizer, customize ...func(*ModelConfig[managementFormRow])) (*siteHTTPClient, *managementFormState, Registry) {
	t.Helper()
	state := &managementFormState{row: managementFormRow{1, "Original", true, 1}, found: true}
	passwords := make([]forms.Field, 0, 2)
	for _, name := range []string{"password1", "password2"} {
		field, err := forms.CharField(name, forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithMaxLength(100))
		if err != nil {
			t.Fatal(err)
		}
		passwords = append(passwords, field)
	}
	config := ModelConfig[managementFormRow]{
		AppLabel: "godj_conformance", Slug: "accounts",
		Model: ir.Model{Name: "account", GoName: "Account", Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "username", GoName: "Username", Column: "username", Kind: ir.FieldChar, MaxLength: 100},
			{Name: "active", GoName: "Active", Column: "active", Kind: ir.FieldBoolean},
			{Name: "revision", GoName: "Revision", Column: "revision", Kind: ir.FieldInteger},
		}},
		ListFields: []string{"id", "username"}, FormFields: []string{"username", "active"}, RevisionField: "revision",
		Permissions:              Permissions{View: "accounts.view", Add: "accounts.add", Change: "accounts.change", Delete: "accounts.delete"},
		AdditionalAddPermissions: []auth.Permission{"accounts.change"},
		CreateForm: &FormConfig{Definition: formmodel.Definition{Fields: []string{"username"}, ExtraFields: passwords, Validators: []forms.CrossValidator{forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
			one, a := values.String("password1")
			two, b := values.String("password2")
			if a && b && one != two {
				return validation.NewErrors(validation.New("password2", "password_mismatch"))
			}
			return validation.NewErrors()
		})}}},
		List: func(ctx context.Context, p auth.Principal, r ListRequest) (Page[managementFormRow], error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.reads++
			state.actor = p.ID()
			rows := []managementFormRow{}
			var total int64
			if state.found {
				total = 1
				if r.Offset == 0 {
					rows = append(rows, state.row)
				}
			}
			return Page[managementFormRow]{Items: rows, Total: total, Offset: r.Offset, Limit: r.Limit}, ctx.Err()
		},
		Get: func(ctx context.Context, p auth.Principal, id int64) (managementFormRow, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.reads++
			state.actor = p.ID()
			return state.row, state.found && id == state.row.id, ctx.Err()
		},
		Snapshot: func(row managementFormRow) (Object, error) {
			return NewObject(row.id, row.username, map[string]templates.Value{"id": templates.Integer(row.id), "username": templates.String(row.username), "active": templates.Bool(row.active), "revision": templates.Integer(row.revision)})
		},
		Initial: func(row managementFormRow) (map[string]forms.Value, error) {
			return map[string]forms.Value{"username": forms.String(row.username), "active": forms.Boolean(row.active)}, nil
		},
		Create: func(ctx context.Context, p auth.Principal, bound formmodel.BoundForm) (managementFormRow, error) {
			values, inputErr := bound.Input()
			if inputErr != nil {
				var zero managementFormRow
				return zero, inputErr
			}

			state.mu.Lock()
			defer state.mu.Unlock()
			state.writes++
			state.actor = p.ID()
			state.password, _ = values.String("password1")
			name, _ := values.String("username")
			state.row = managementFormRow{2, name, true, 1}
			state.found = true
			return state.row, ctx.Err()
		},
		Update: func(ctx context.Context, p auth.Principal, m Mutation, bound formmodel.BoundForm) (managementFormRow, []string, error) {
			values, inputErr := bound.Input()
			if inputErr != nil {
				var zero managementFormRow
				return zero, nil, inputErr
			}

			state.mu.Lock()
			defer state.mu.Unlock()
			state.actor = p.ID()
			if state.race {
				state.row.username = "Concurrent"
				state.row.revision++
				state.race = false
			}
			if !state.found || state.row.id != m.ID {
				return managementFormRow{}, nil, ErrObjectNotFound
			}
			if state.row.revision != m.Revision {
				return managementFormRow{}, nil, &OperationError{Code: OperationConflict}
			}
			name, _ := values.String("username")
			active, _ := values.Boolean("active")
			changed := []string{}
			if name != state.row.username {
				changed = append(changed, "username")
			}
			if active != state.row.active {
				changed = append(changed, "active")
			}
			if len(changed) != 0 {
				state.row.username = name
				state.row.active = active
				state.row.revision++
				state.writes++
			}
			if state.unknown {
				return managementFormRow{}, nil, NewOperationError(OperationOutcomeUnknown, errors.New("private storage failure"))
			}
			return state.row, changed, ctx.Err()
		},
		Delete: func(ctx context.Context, p auth.Principal, m Mutation) (managementFormRow, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.actor = p.ID()
			if !state.found || state.row.id != m.ID {
				return managementFormRow{}, ErrObjectNotFound
			}
			if state.row.revision != m.Revision {
				return managementFormRow{}, &OperationError{Code: OperationConflict}
			}
			state.found = false
			state.writes++
			return state.row, ctx.Err()
		},
		History: func(ctx context.Context, p auth.Principal, _ int64, _ HistoryRequest) ([]AuditEntry, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.actor = p.ID()
			return nil, ctx.Err()
		},
	}
	for _, change := range customize {
		change(&config)
	}
	installed := mustApps(t)
	builder := NewBuilder(installed)
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	identities := []siteIdentity{
		{"admin", sitePrincipal(t, "manager", true, "accounts.view", "accounts.add", "accounts.change", "accounts.delete")},
		{"creator", sitePrincipal(t, "creator", true, "accounts.add")},
		{"changer", sitePrincipal(t, "changer", true, "accounts.change")},
		{"viewer", sitePrincipal(t, "viewer", true, "accounts.view")},
	}
	runtime, _ := siteTestRuntimeConfigured(t, paths, "/admin/login/", "/admin/", identities, "/", "/", authorizer)
	site, err := NewSite(SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: runtime})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "admin_management_form", InstalledApps: installed.All()})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	if err != nil {
		t.Fatal(err)
	}
	return newSiteHTTPClient(application), state, registry
}

func managementRevision(t *testing.T, body string) string {
	t.Helper()
	matched := regexp.MustCompile(`name="expected_revision" value="([0-9]+)"`).FindStringSubmatch(body)
	if len(matched) != 2 {
		t.Fatal("form lacks a revision condition")
	}
	return matched[1]
}

func TestAdminCreateFormKeepsPasswordPrivateAndAdditionalPermissions(t *testing.T) {
	client, state, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{})
	client.login(t, "admin", "secret", "/admin/accounts/")
	get := client.do("GET", "/admin/accounts/add/", nil)
	if get.Code != 200 || !strings.Contains(get.Body.String(), `type="password" name="password1"`) || strings.Contains(get.Body.String(), `name="active"`) || strings.Contains(get.Body.String(), `name="expected_revision"`) {
		t.Fatal("creation form projection differs")
	}
	const secret = "  private-λ<&>  "
	values := url.Values{"username": {"Created"}, "password1": {secret}, "password2": {"different"}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
	bad := client.do("POST", "/admin/accounts/add/", values)
	if bad.Code != 200 || !strings.Contains(bad.Body.String(), `data-error-code="password_mismatch"`) || strings.Contains(bad.Body.String(), "private") || strings.Contains(bad.Body.String(), "different") || state.writes != 0 {
		t.Fatal("password rejection leaked or wrote input")
	}
	values.Set("csrfmiddlewaretoken", siteCSRFToken(t, bad.Body.String()))
	values.Set("password2", secret)
	saved := client.do("POST", "/admin/accounts/add/", values)
	if saved.Code != 302 || state.writes != 1 || state.password != secret || state.row.username != "Created" || !state.row.active || state.actor != "manager" {
		t.Fatal("creation form did not deliver its own cleaned fields")
	}
	edit := client.do("GET", "/admin/accounts/change/?id=2", nil)
	if edit.Code != 200 || strings.Contains(edit.Body.String(), `name="password1"`) || !strings.Contains(edit.Body.String(), `name="active"`) || managementRevision(t, edit.Body.String()) != "1" {
		t.Fatal("change form inherited creation command inputs")
	}
	metadata := registry.All()[0]
	if len(metadata.CreateFormFields) != 3 || len(metadata.FormFields) != 2 || len(metadata.AddPermissions) != 2 || metadata.RevisionField != "revision" {
		t.Fatal("detached descriptor lost action fields")
	}
	metadata.AddPermissions[0] = "forged.permission"
	if registry.All()[0].AddPermissions[0] == "forged.permission" {
		t.Fatal("descriptor aliases permission policy")
	}
	values.Set("active", "false")
	if response := client.do("POST", "/admin/accounts/add/", values); response.Code != 400 || state.writes != 1 {
		t.Fatal("creation accepted a change-only field")
	}
	editValues := url.Values{"username": {"Created"}, "active": {"on"}, "expected_revision": {"1"}, "password1": {secret}, "csrfmiddlewaretoken": {siteCSRFToken(t, edit.Body.String())}}
	if response := client.do("POST", "/admin/accounts/change/?id=2", editValues); response.Code != 400 || state.writes != 1 {
		t.Fatal("change accepted a creation-only password")
	}
	creator, restricted, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{})
	creator.login(t, "creator", "secret", "/admin/accounts/")
	if response := creator.do("GET", "/admin/accounts/add/", nil); response.Code != 403 || restricted.reads != 0 || restricted.writes != 0 {
		t.Fatal("add-only actor passed additional creation permission")
	}
	index := creator.do("GET", "/admin/", nil)
	deniedValues := url.Values{"username": {"Forbidden"}, "password1": {secret}, "password2": {secret}, "csrfmiddlewaretoken": {siteCSRFToken(t, index.Body.String())}}
	if response := creator.do("POST", "/admin/accounts/add/", deniedValues); response.Code != 403 || restricted.reads != 0 || restricted.writes != 0 {
		t.Fatal("creation POST bypassed its additional permission")
	}
}

func TestAdminRevisionSurvivesValidationAndRejectsConcurrentWrites(t *testing.T) {
	for _, mode := range []string{"stale", "late", "invalid", "unknown", "no_op", "delete", "large_revision"} {
		t.Run(mode, func(t *testing.T) {
			client, state, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{})
			if mode == "large_revision" {
				state.row.revision = 1152921504606846977
			}
			client.login(t, "admin", "secret", "/admin/accounts/")
			get := client.do("GET", "/admin/accounts/change/?id=1", nil)
			if get.Code != 200 {
				t.Fatal("change GET")
			}
			values := url.Values{"username": {"Updated"}, "active": {"on"}, "expected_revision": {managementRevision(t, get.Body.String())}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
			switch mode {
			case "stale":
				state.row.username = "Concurrent"
				state.row.revision = 2
			case "late":
				state.race = true
			case "invalid":
				values.Set("username", "")
				response := client.do("POST", "/admin/accounts/change/?id=1", values)
				if response.Code != 200 || managementRevision(t, response.Body.String()) != "1" || state.writes != 0 {
					t.Fatal("validation lost original condition")
				}
				for _, raw := range []string{"", "0", "01", "+1", "-1", strconv.FormatInt(math.MaxInt64, 10)} {
					values.Set("username", "Updated")
					values.Set("expected_revision", raw)
					values.Set("csrfmiddlewaretoken", siteCSRFToken(t, response.Body.String()))
					if rejected := client.do("POST", "/admin/accounts/change/?id=1", values); rejected.Code != 400 || state.writes != 0 {
						t.Fatal("noncanonical revision accepted")
					}
				}
				return
			case "unknown":
				state.unknown = true
			case "no_op":
				values.Set("username", "Original")
			case "delete":
				del := client.do("GET", "/admin/accounts/delete/?id=1", nil)
				if del.Code != 200 || managementRevision(t, del.Body.String()) != "1" {
					t.Fatal("delete condition missing")
				}
				state.row.revision = 2
				request := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, del.Body.String())}, "confirm": {"yes"}, "expected_revision": {"1"}}
				if response := client.do("POST", "/admin/accounts/delete/?id=1", request); response.Code != 409 || !state.found || state.writes != 0 {
					t.Fatal("stale delete removed current object")
				}
				request.Set("expected_revision", "2")
				if response := client.do("POST", "/admin/accounts/delete/?id=1", request); response.Code != 302 || state.found || state.writes != 1 {
					t.Fatal("confirmed delete failed")
				}
				return
			}
			response := client.do("POST", "/admin/accounts/change/?id=1", values)
			switch mode {
			case "stale", "late":
				if response.Code != 409 || state.row.username != "Concurrent" || state.writes != 0 {
					t.Fatal("revision race overwrote current state")
				}
			case "unknown":
				if response.Code != 503 || response.Header().Get("Location") != "" || response.Header().Get("Retry-After") != "" || state.writes != 1 || strings.Contains(response.Body.String(), "private storage") {
					t.Fatal("unknown outcome retried, exposed cause or reported success")
				}
			case "no_op":
				if response.Code != 302 || state.row.revision != 1 || state.writes != 0 {
					t.Fatal("no-op advanced revision")
				}
			case "large_revision":
				if response.Code != 302 || state.row.revision != 1152921504606846978 || state.writes != 1 {
					t.Fatal("form revision lost int64 precision")
				}
			}
		})
	}
}

func TestAdminCreationProjectionAndRevisionRejectInvalidStartupWithoutIO(t *testing.T) {
	for _, change := range []func(*ModelConfig[registryArticle]){
		func(c *ModelConfig[registryArticle]) {
			c.CreateForm = &FormConfig{Definition: formmodel.Definition{Fields: []string{"title"}, ExtraFields: []forms.Field{cField(t, "summary")}}}
		},
		func(c *ModelConfig[registryArticle]) {
			c.CreateForm = &FormConfig{Definition: formmodel.Definition{Fields: []string{"title"}, ExtraFields: []forms.Field{cField(t, "csrfmiddlewaretoken")}}}
		},
		func(c *ModelConfig[registryArticle]) {
			c.CreateForm = &FormConfig{Definition: formmodel.Definition{Fields: []string{"title"}, ExtraFields: []forms.Field{cField(t, "expected_revision")}}}
		},
		func(c *ModelConfig[registryArticle]) {
			c.CreateForm = &FormConfig{Definition: formmodel.Definition{Fields: []string{"unknown"}}}
		},
		func(c *ModelConfig[registryArticle]) {
			c.CreateForm = &FormConfig{Definition: formmodel.Definition{Fields: []string{"title"}, Validators: []forms.CrossValidator{nil}}}
		},
		func(c *ModelConfig[registryArticle]) {
			c.AdditionalAddPermissions = []auth.Permission{"invalid permission"}
		},
		func(c *ModelConfig[registryArticle]) { c.RevisionField = "unknown" },
		func(c *ModelConfig[registryArticle]) { c.RevisionField = "id" },
		func(c *ModelConfig[registryArticle]) { c.RevisionField = "title" },
		func(c *ModelConfig[registryArticle]) {
			c.Model.Fields = append(c.Model.Fields, ir.Field{Name: "revision", GoName: "Revision", Column: "revision", Kind: ir.FieldInteger})
			c.RevisionField = "revision"
		},
	} {
		config := validRegistryConfig(t)
		change(&config)
		config.List = func(context.Context, auth.Principal, ListRequest) (Page[registryArticle], error) {
			t.Fatal("startup queried a callback")
			return Page[registryArticle]{}, nil
		}
		if err := RegisterModel(NewBuilder(mustApps(t)), config); err == nil {
			t.Fatal("invalid create/revision definition accepted")
		}
	}
}

func cField(t *testing.T, name string) forms.Field {
	t.Helper()
	field, err := forms.CharField(name)
	if err != nil {
		t.Fatal(err)
	}
	return field
}

func TestAdminReadUsesChangePermissionAndForwardsActorWithoutMaskingErrors(t *testing.T) {
	client, state, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{})
	client.login(t, "changer", "secret", "/admin/accounts/")
	for _, path := range []string{"/admin/", "/admin/accounts/", "/admin/accounts/change/?id=1", "/admin/accounts/history/?id=1"} {
		response := client.do(http.MethodGet, path, nil)
		if response.Code != 200 {
			t.Fatal("change-only read denied", path, response.Code)
		}
	}
	if state.actor != "changer" {
		t.Fatal("read callback lost current actor")
	}
	failure := managementFormAuthorizer(func(_ context.Context, _ auth.Principal, p auth.Permission) (bool, error) {
		if p == "accounts.view" {
			return false, errors.New("private authorization failure")
		}
		return true, nil
	})
	broken, blocked, _ := newManagementFormSite(t, failure)
	broken.login(t, "admin", "secret", "/admin/accounts/")
	response := broken.do("GET", "/admin/accounts/", nil)
	if response.Code != 500 || blocked.reads != 0 || strings.Contains(response.Body.String(), "private authorization") {
		t.Fatal("read authorization failure fell back to change")
	}
}
