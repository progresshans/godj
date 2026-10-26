package identityapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type selectedProbe struct{ reads, writes atomic.Int64 }

func (probe *selectedProbe) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	probe.reads.Add(1)
	return errors.New("private read must not be exposed")
}
func (probe *selectedProbe) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	probe.writes.Add(1)
	return errors.New("private write must not be exposed")
}
func (probe *selectedProbe) CoordinatedAtomicRelation(context.Context, func(db.RelationSession) error) error {
	probe.writes.Add(1)
	return errors.New("private relation write must not be exposed")
}

func selectedOwners(t *testing.T, readFields ...[]string) (*accountmodel.AccountManagement[models.User], *accountmodel.Catalog[models.Group, models.Permission], *selectedProbe) {
	t.Helper()
	probe := &selectedProbe{}
	store, err := project.NewAccountSessionStore(probe, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	read := []string{"username", "email", "first_name", "active", "groups"}
	if len(readFields) != 0 {
		read = readFields[0]
	}
	owner, err := project.NewAccountManagement(store, verifier, auth.PrincipalAuthorizer{}, accountmodel.AccountManagementConfig[models.User]{
		ReadFields: read, CreateFields: []string{"username", "email", "first_name", "groups"}, EditFields: []string{"email", "first_name", "active", "groups"},
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete", Additional: []auth.Permission{"account.manage"}},
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := project.NewAccountCatalog(store, auth.PrincipalAuthorizer{}, accountmodel.CatalogConfig[models.Group, models.Permission]{
		Group:              accountmodel.CatalogModelConfig[models.Group]{ReadFields: []string{"name", "permissions"}, WriteFields: []string{"name", "permissions"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group", Additional: []auth.Permission{"catalog.manage"}}},
		Permission:         accountmodel.CatalogModelConfig[models.Permission]{ReadFields: []string{"code", "name"}, WriteFields: []string{"code", "name"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission", Additional: []auth.Permission{"catalog.manage"}}},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner, catalog, probe
}

func TestSelectedManagementDeclaresActualOwnersAndCredentialNamespaceBeforeIO(t *testing.T) {
	owner, catalog, probe := selectedOwners(t)
	calls := 0
	authentication, err := bearerauth.New(bearerauth.Config{Verifier: documentVerifier{&calls}, Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewManagement(owner, catalog, ManagementConfig{Authentication: authentication})
	if err != nil {
		t.Fatal(err)
	}
	document, err := application.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if len(application.Routes()) != 22 || len(document.Routes()) != 22 || calls != 0 || probe.reads.Load() != 0 || probe.writes.Load() != 0 {
		t.Fatal("partial registration or startup I/O")
	}
	var schema map[string]any
	decoder := json.NewDecoder(bytes.NewReader(document.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	paths := schema["paths"].(map[string]any)
	read := paths["/api/identity/accounts/"].(map[string]any)["get"].(map[string]any)
	if !slices.Equal(read["x-godj-any-permissions"].([]any), []any{"account.view", "account.change"}) || !slices.Equal(read["x-godj-additional-permissions"].([]any), []any{"account.manage"}) {
		t.Fatal("read policy differs from owner")
	}
	create := paths["/api/identity/accounts/"].(map[string]any)["post"].(map[string]any)
	if create["x-godj-permission"] != "account.add" || !slices.Equal(create["x-godj-additional-permissions"].([]any), []any{"account.change", "account.manage"}) {
		t.Fatal("create lost add/change/common grants")
	}
	definitions := schema["components"].(map[string]any)["schemas"].(map[string]any)
	createProperties := definitions["AccountCreate"].(map[string]any)["properties"].(map[string]any)
	modelFields := createProperties["fields"].(map[string]any)["properties"].(map[string]any)
	if len(createProperties) != 2 || createProperties["credential"] == nil || modelFields["username"] == nil || modelFields["groups"] == nil {
		t.Fatal("creation is not the actual model plus command")
	}
	for _, name := range []string{"id", "revision", "principal_id", "encoded_password", "password_usable", "password"} {
		if modelFields[name] != nil {
			t.Fatal("server/command field became writable", name)
		}
	}
	login := modelFields["username"].(map[string]any)["x-godj-normalization"].(map[string]any)
	if login["transform"] != "unicode16_nfkc" || login["maxInputBytes"] != json.Number("1024") {
		t.Fatal("selected role input policy omitted")
	}
	for _, name := range []string{"Account", "AccountSummary"} {
		properties := definitions[name].(map[string]any)["properties"].(map[string]any)
		if properties["password_usable"].(map[string]any)["type"] != "boolean" {
			t.Fatal("password-state metadata lost")
		}
		fields := properties["fields"].(map[string]any)["properties"].(map[string]any)
		if fields["principal_id"] != nil || fields["encoded_password"] != nil || fields["permissions"] != nil || (fields["groups"] != nil) != (name == "Account") {
			t.Fatal("read or list exposure widened", name)
		}
	}
	patch := paths["/api/identity/accounts/{id}/"].(map[string]any)["patch"].(map[string]any)
	for _, code := range []string{"400", "412", "428", "503"} {
		if patch["responses"].(map[string]any)[code] == nil {
			t.Fatal("failure missing from document", code)
		}
	}
	for _, mode := range []string{"account", "catalog", "authentication", "prefix", "namespace"} {
		t.Run(mode, func(t *testing.T) {
			a, c, config := owner, catalog, ManagementConfig{Authentication: authentication}
			switch mode {
			case "account":
				a = nil
			case "catalog":
				c = nil
			case "authentication":
				config.Authentication = (*bearerauth.Runtime)(nil)
			case "prefix":
				config.BasePath = "/api/<str:untrusted>/"
			case "namespace":
				config.Namespace = "invalid namespace"
			}
			if app, err := NewManagement(a, c, config); err == nil || app != nil {
				t.Fatal("invalid declaration published")
			}
		})
	}
	for _, application := range []*Management{nil, {}} {
		if _, err := application.OpenAPI(); err == nil {
			t.Fatal("zero document published")
		}
	}
	if calls != 0 || probe.reads.Load() != 0 || probe.writes.Load() != 0 {
		t.Fatal("declaration executed a dependency")
	}
}

func TestSelectedManagementCollectionOnlyReadDoesNotInventScalarExposure(t *testing.T) {
	owner, catalog, probe := selectedOwners(t, []string{"groups"})
	calls := 0
	authentication, err := bearerauth.New(bearerauth.Config{Verifier: documentVerifier{&calls}, Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	management, err := NewManagement(owner, catalog, ManagementConfig{Authentication: authentication})
	if err != nil {
		t.Fatal(err)
	}
	document, err := management.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(document.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"Account", "AccountSummary"} {
		fields := definitions[name].(map[string]any)["properties"].(map[string]any)["fields"].(map[string]any)["properties"].(map[string]any)
		if name == "Account" && (len(fields) != 1 || fields["groups"] == nil) || name == "AccountSummary" && len(fields) != 0 {
			t.Fatal("collection-only selection widened output", name)
		}
	}
	if calls != 0 || probe.reads.Load() != 0 || probe.writes.Load() != 0 {
		t.Fatal("empty scalar projection performed I/O")
	}
}

type selectedVerifier struct {
	permissions []auth.Permission
	calls       *atomic.Int64
}

func (verifier selectedVerifier) Verify(context.Context, bearerauth.Token) (auth.Principal, error) {
	verifier.calls.Add(1)
	return auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true, Permissions: verifier.permissions})
}

func selectedHTTPFixture(t *testing.T, permissions []auth.Permission) (*web.Application, *selectedProbe, *atomic.Int64) {
	t.Helper()
	owner, catalog, probe := selectedOwners(t)
	calls := &atomic.Int64{}
	authentication, err := bearerauth.New(bearerauth.Config{Verifier: selectedVerifier{permissions, calls}, Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	management, err := NewManagement(owner, catalog, ManagementConfig{Namespace: "selected", Authentication: authentication})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "selected_api", InstalledApps: []apps.Config{{Name: "example.test/selected", Label: "selected"}}})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: management.Routes(), Middleware: management.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	return application, probe, calls
}

func TestSelectedManagementHTTPRejectsBeforeBodyAndOwner(t *testing.T) {
	permissions := []auth.Permission{"account.manage", "account.view", "account.add", "account.change", "account.delete"}
	for _, test := range []struct {
		name, method, path, revision, body string
		status                             int
		read                               bool
		grants                             []auth.Permission
	}{
		{"missing_condition", "PATCH", "/api/identity/accounts/0/", "", "invalid", 428, false, permissions},
		{"invalid_condition", "PATCH", "/api/identity/accounts/-1/", "01", "invalid", 400, false, permissions},
		{"condition_overflow", "PATCH", "/api/identity/accounts/1/", "9223372036854775807", "invalid", 400, false, permissions},
		{"noncanonical_key", "PATCH", "/api/identity/accounts/-0/", "1", "invalid", 400, false, permissions},
		{"key_overflow", "PATCH", "/api/identity/accounts/9223372036854775808/", "1", "invalid", 400, false, permissions},
		{"query", "PATCH", "/api/identity/accounts/1/?extra=1", "1", "invalid", 400, false, permissions},
		{"minimum_key", "PATCH", "/api/identity/accounts/-9223372036854775808/", "1", "invalid", 400, true, permissions},
		{"protected", "PATCH", "/api/identity/accounts/1/", "1", `{"fields":{"revision":2}}`, 400, true, permissions},
		{"source_namespace", "PATCH", "/api/identity/accounts/1/", "1", `{"email":"someone@example.test"}`, 400, true, permissions},
		{"missing_credential", "POST", "/api/identity/accounts/", "", `{"fields":{"username":"new"}}`, 400, true, permissions},
		{"raw_login_bytes", "POST", "/api/identity/accounts/", "", `{"fields":{"username":"` + strings.Repeat(" ", 1025) + `x"},"credential":{"password":"secret"}}`, 400, true, permissions},
		{"common_denied", "PATCH", "/api/identity/accounts/1/", "", "invalid", 403, false, []auth.Permission{"account.change"}},
		{"add_requires_change", "POST", "/api/identity/accounts/", "", "invalid", 403, false, []auth.Permission{"account.manage", "account.add"}},
		{"unknown_page", "GET", "/api/identity/accounts/?limit=1&unknown=2", "", "", 400, false, permissions},
		{"history_query", "GET", "/api/identity/accounts/-1/history/?limit=101", "", "", 400, false, permissions},
		{"missing_password_condition", "POST", "/api/identity/accounts/0/password/", "", "invalid", 428, false, permissions},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, probe, calls := selectedHTTPFixture(t, test.grants)
			body := &headerObservedBody{Reader: strings.NewReader(test.body)}
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Body = body
			request.ContentLength = -1
			request.Header.Set("Authorization", "Bearer selected")
			request.Header.Set("Content-Type", api.JSONContentType)
			if test.revision != "" {
				request.Header.Set("If-Revision", test.revision)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.status || (body.reads > 0) != test.read || body.closes != 0 || probe.reads.Load() != 0 || probe.writes.Load() != 0 || calls.Load() != 1 {
				t.Fatal("admission/input/owner order", response.Code, response.Body, body, probe.reads.Load(), calls.Load())
			}
			if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private") {
				t.Fatal("response policy or private failure leak")
			}
		})
	}
}

func TestSelectedManagementReadsUseEitherGrantAndEveryCommonGrant(t *testing.T) {
	for _, test := range []struct {
		name        string
		permissions []auth.Permission
		status      int
		reads       int64
	}{
		{"view", []auth.Permission{"account.manage", "account.view"}, 500, 1},
		{"change", []auth.Permission{"account.manage", "account.change"}, 500, 1},
		{"common_missing", []auth.Permission{"account.view"}, 403, 0},
		{"alternatives_missing", []auth.Permission{"account.manage"}, 403, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, probe, calls := selectedHTTPFixture(t, test.permissions)
			request := httptest.NewRequest("GET", "/api/identity/accounts/", nil)
			request.Header.Set("Authorization", "Bearer selected")
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.status || probe.reads.Load() != test.reads || calls.Load() != 1 || strings.Contains(response.Body.String(), "private") {
				t.Fatal("read policy broadened or lost execution failure", response.Code, probe.reads.Load(), response.Body)
			}
		})
	}
}

func TestSelectedManagementFailureKeepsCommandAndModelDiagnosticsDistinct(t *testing.T) {
	owner, _, probe := selectedOwners(t)
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := auth.NewCredentialDomain(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredentialForDomain(domain, "actor", "!unusable", principal)
	if err != nil {
		t.Fatal(err)
	}
	_, password := owner.Creator().Prepare(t.Context(), credential.Principal(), orm.ModelInput[models.User]{}, "")
	model := validation.Reject(validation.NewErrors(validation.New("password", "model_choice")), nil)
	for _, test := range []struct {
		name    string
		failure error
		path    string
		status  int
	}{
		{"password", password, "/credential/password", 400},
		{"profile_password", model, "/fields/password", 400},
		{"permission", &accountmodel.Error{Code: accountmodel.CodePermission}, "", 403},
		{"missing", &accountmodel.Error{Code: accountmodel.CodeNotFound}, "", 404},
		{"conflict", &accountmodel.Error{Code: accountmodel.CodeConflict}, "", 412},
		{"unknown", &accountmodel.Error{Code: accountmodel.CodeOutcomeUnknown}, "", 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := selectedFailure(test.failure)
			if err != nil || response.Status() != test.status {
				t.Fatal("wrong public failure", response.Status(), err)
			}
			body, _ := response.Body()
			if test.path != "" && !strings.Contains(string(body), `"field":"`+test.path+`"`) {
				t.Fatal("input namespace changed", string(body))
			}
			if response.Header().Get("Retry-After") != "" {
				t.Fatal("uncertain operation advertised retry")
			}
		})
	}
	for _, failure := range []error{errors.Join(model), errors.Join(password, context.Canceled), fmt.Errorf("private: %w", &accountmodel.Error{Code: accountmodel.CodePermission}), &accountmodel.Error{Code: accountmodel.CodePersistence}} {
		if response, err := selectedFailure(failure); err != failure || response.Status() != 0 {
			t.Fatal("execution failure classified as input", err)
		}
	}
	if probe.reads.Load() != 0 || probe.writes.Load() != 0 {
		t.Fatal("password preflight performed I/O")
	}
}

func TestSelectedMutationReceiptPreservesEverySignedKeyWithoutFurtherWork(t *testing.T) {
	for _, id := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		t.Run(strconv.FormatInt(id, 10), func(t *testing.T) {
			response, err := selectedMutationResponse(200, id, math.MaxInt64)
			if err != nil || response.Header().Get("Revision") != strconv.FormatInt(math.MaxInt64, 10) {
				t.Fatal(err)
			}
			body, _ := response.Body()
			var result struct{ ID, Revision int64 }
			if err := json.Unmarshal(body, &result); err != nil || result.ID != id || result.Revision != math.MaxInt64 {
				t.Fatal("receipt changed committed metadata", err, string(body))
			}
		})
	}
}
