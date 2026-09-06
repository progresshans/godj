package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestPublicHelpdeskConsumerWithExistingDatabasePermissionsAndSelectedAdminFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "helpdesk.sqlite3")) + "?mode=rwc&_busy_timeout=5000"
	runPublicHelpdeskConsumer(t, ctx, func(ctx context.Context) (helpdeskBackend, error) { return sqlite.Open(ctx, dsn) })
}

type helpdeskBackend interface {
	systemstate.Backend
	db.Atomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

// Count application data reads separately from session/permission-store reads.
type helpdeskReadCounter struct {
	helpdesk.Backend
	queries int
	last    query.Plan
}

func (backend *helpdeskReadCounter) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.queries++
	backend.last = plan
	return backend.Backend.Query(ctx, plan)
}

type helpdeskDeniedPermission struct{ permission auth.Permission }

func (deny helpdeskDeniedPermission) Allowed(ctx context.Context, principal auth.Principal, permission auth.Permission) (bool, error) {
	if permission == deny.permission {
		return false, nil
	}
	return (auth.PrincipalAuthorizer{}).Allowed(ctx, principal, permission)
}

func runPublicHelpdeskConsumer(t *testing.T, ctx context.Context, open func(context.Context) (helpdeskBackend, error)) {
	t.Helper()
	backend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	loaded, _, err := definition.Load(systemstate.InitialDefinitionSource(), helpdesk.InitialMigrationSource())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	category, err := models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("Hardware & repairs"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("Other"))
	if err != nil {
		t.Fatal(err)
	}
	seed, err := models.TicketObjects.Create(ctx, backend, models.NewTicketCreate("Existing ticket", category.ID).WithDetailsNull())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.TicketObjects.Create(ctx, backend, models.NewTicketCreate("Other category ticket", other.ID))
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "helpdesk-operator", Active: true, Permissions: []auth.Permission{admin.DefaultAccessPermission}})
	if err != nil {
		t.Fatal(err)
	}
	before := systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}
	if err := systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{Username: "operator", Password: "helpdesk-example-password", CredentialPolicy: before}); err != nil {
		t.Fatal(err)
	}
	permissions := append([]auth.Permission{admin.DefaultAccessPermission}, helpdesk.Permissions()...)
	after, err := before.WithPermissions(permissions...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := systemstate.OpenExisting(ctx, backend, systemstate.RuntimeConfig{CredentialPolicy: after}); err == nil {
		t.Fatal("new permissions were silently adopted")
	}
	verifyConcurrentPermissionMaintenance(t, ctx, open, backend, before, permissions)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := systemstate.OpenExisting(ctx, backend, systemstate.RuntimeConfig{CredentialPolicy: after})
	if err != nil {
		t.Fatal(err)
	}
	if value, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(seed.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx); err != nil || !found || value.Subject != "Existing ticket" {
		t.Fatalf("existing model data lost during permission update/reopen: %v", err)
	}
	reads := &helpdeskReadCounter{Backend: runtime}
	application, err := helpdesk.New(reads, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	categoryInfo, ok := application.Registry().Lookup("helpdesk", "category")
	if !ok || !categoryInfo.ReadOnly || len(categoryInfo.FormFields) != 0 {
		t.Fatal("Category should need no form/CRUD adapter")
	}
	ticketInfo, ok := application.Registry().Lookup("helpdesk", "ticket")
	if !ok || len(ticketInfo.FormFields) != 3 {
		t.Fatal("Ticket scalar selection")
	}
	for _, field := range ticketInfo.FormFields {
		if field.Name() == "category" || field.Name() == "id" {
			t.Fatal("relation/key became writable")
		}
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	if response := client.request("GET", "/api/tickets/", "", false); response.Code != http.StatusForbidden {
		t.Fatalf("anonymous API: %d", response.Code)
	}
	detailPath := fmt.Sprintf("/api/tickets/%d/", seed.ID)
	beforeDetail := reads.queries
	if response := client.request("GET", detailPath, "", false); response.Code != http.StatusForbidden || reads.queries != beforeDetail {
		t.Fatalf("anonymous detail performed a data query or bypassed permission: %d", response.Code)
	}
	if response := client.request("GET", "/admin/login/", "", false); response.Code != http.StatusOK || client.csrf == "" {
		t.Fatalf("login form: %d", response.Code)
	}
	login := url.Values{"username": {"operator"}, "password": {"helpdesk-example-password"}, "next": {"/admin/"}, "csrfmiddlewaretoken": {client.csrf}}
	if response := client.request("POST", "/admin/login/", login.Encode(), false); response.Code != http.StatusFound {
		t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	beforeDetail = reads.queries
	detailResponse := client.request("GET", detailPath, "", false)
	var detail map[string]map[string]json.RawMessage
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil || detailResponse.Code != http.StatusOK || reads.queries != beforeDetail+1 {
		t.Fatalf("joined detail: status=%d queries=%d body=%s err=%v", detailResponse.Code, reads.queries-beforeDetail, detailResponse.Body, err)
	}
	if len(detail) != 2 || len(detail["ticket"]) != 5 || len(detail["category"]) != 2 || string(detail["ticket"]["id"]) != strconv.FormatInt(seed.ID, 10) || string(detail["ticket"]["details"]) != "null" || string(detail["category"]["id"]) != strconv.FormatInt(category.ID, 10) {
		t.Fatalf("detail output fields/values: %s", detailResponse.Body)
	}
	var categoryName string
	if err := json.Unmarshal(detail["category"]["name"], &categoryName); err != nil || categoryName != category.Name {
		t.Fatalf("category label = %q, %v", categoryName, err)
	}
	if limit, _ := reads.last.Limit(); limit != 1 {
		t.Fatalf("detail limit = %d", limit)
	}
	if projection, ok := reads.last.RelationProjection(); !ok || projection.Hop().Field() != "category" {
		t.Fatal("detail did not use the category projection")
	}
	for _, id := range []int64{outside.ID, 0, outside.ID + 1000} {
		response := client.request("GET", fmt.Sprintf("/api/tickets/%d/", id), "", false)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), other.Name) || strings.Contains(response.Body.String(), outside.Subject) {
			t.Fatalf("missing/scoped detail: %d %s", response.Code, response.Body)
		}
	}
	for _, denied := range []auth.Permission{helpdesk.ViewTicket, helpdesk.ViewCategory} {
		limitedClient := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{denied})
		limitedClient.cookies = client.cookies
		before := reads.queries
		response := limitedClient.request("GET", detailPath, "", false)
		wantStatus, wantReads := http.StatusForbidden, 0
		if denied == helpdesk.ViewCategory {
			wantStatus, wantReads = http.StatusOK, 1
		}
		if response.Code != wantStatus || reads.queries-before != wantReads {
			t.Fatalf("detail permission %s: status=%d data queries=%d", denied, response.Code, reads.queries-before)
		}
	}
	if response := client.request("GET", "/admin/categories/", "", false); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Hardware &amp; repairs") || strings.Contains(response.Body.String(), "Add Category") {
		t.Fatalf("read-only categories: %d %s", response.Code, response.Body)
	}
	if response := client.request("POST", "/admin/categories/add/", "", false); response.Code != http.StatusNotFound {
		t.Fatalf("readonly mutation route exists: %d", response.Code)
	}
	if response := client.request("GET", "/admin/tickets/add/", "", false); response.Code != http.StatusOK || strings.Contains(response.Body.String(), `name="category"`) {
		t.Fatalf("selected ticket form: %d %s", response.Code, response.Body)
	}
	values := url.Values{"subject": {"Admin ticket"}, "details": {""}, "closed": {"false"}, "csrfmiddlewaretoken": {client.csrf}, "category": {strconv.FormatInt(other.ID, 10)}}
	if response := client.request("POST", "/admin/tickets/add/", values.Encode(), false); response.Code != http.StatusBadRequest {
		t.Fatalf("injected relationship accepted: %d", response.Code)
	}
	values.Del("category")
	if response := client.request("POST", "/admin/tickets/add/", values.Encode(), false); response.Code != http.StatusFound {
		t.Fatalf("Admin create: %d %s", response.Code, response.Body)
	}
	if response := client.request("GET", "/admin/tickets/", "", false); response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Other category ticket") {
		t.Fatalf("scoped Admin list: %d %s", response.Code, response.Body)
	}
	if response := client.request("GET", fmt.Sprintf("/admin/tickets/change/?id=%d", outside.ID), "", false); response.Code != http.StatusNotFound {
		t.Fatalf("unselected category leaked through direct object read: %d", response.Code)
	}
	response := client.request("POST", "/api/tickets/", `{"subject":"JSON ticket","details":null}`, true)
	if response.Code != http.StatusCreated {
		t.Fatalf("JSON create: %d %s", response.Code, response.Body)
	}
	var created struct {
		ID       int64   `json:"id"`
		Category int64   `json:"category"`
		Closed   bool    `json:"closed"`
		Details  *string `json:"details"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID <= seed.ID || created.Category != category.ID || created.Closed || created.Details != nil {
		t.Fatalf("typed JSON projection: %+v %v", created, err)
	}
	for _, document := range []string{`{"subject":"Bad","category":` + strconv.FormatInt(other.ID, 10) + `}`, `{"subject":"` + strings.Repeat("x", 121) + `"}`} {
		if response := client.request("POST", "/api/tickets/", document, true); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid API data accepted: %d %s", response.Code, response.Body)
		}
	}
	changePath := fmt.Sprintf("/admin/tickets/change/?id=%d", created.ID)
	if response := client.request("GET", changePath, "", false); response.Code != http.StatusOK {
		t.Fatalf("metadata-derived initial values: %d %s", response.Code, response.Body)
	}
	values = url.Values{"subject": {"Resolved"}, "details": {"done"}, "closed": {"true"}, "csrfmiddlewaretoken": {client.csrf}}
	if response := client.request("POST", changePath, values.Encode(), false); response.Code != http.StatusFound {
		t.Fatalf("Admin update: %d %s", response.Code, response.Body)
	}
	stored, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.CategoryID != category.ID || stored.Subject != "Resolved" || !stored.Closed || stored.Details == nil || *stored.Details != "done" {
		t.Fatalf("real stored mutation: %+v %v", stored, err)
	}
	if count, err := models.TicketObjects.Using(backend).Count(ctx); err != nil || count != 4 {
		t.Fatalf("invalid requests changed rows: %d %v", count, err)
	}
}

func verifyConcurrentPermissionMaintenance(t *testing.T, ctx context.Context, open func(context.Context) (helpdeskBackend, error), first helpdeskBackend, before systemstate.CredentialPolicy, permissions []auth.Permission) {
	t.Helper()
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	oldRuntime, err := systemstate.OpenExisting(ctx, second, systemstate.RuntimeConfig{CredentialPolicy: before})
	if err != nil {
		t.Fatal(err)
	}
	id, err := sessions.ParseID(strings.Repeat("A", 43))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record, err := sessions.RestoreRecord(sessions.RecordSnapshot{ID: id, Values: map[string]string{"principal_id": before.Principal.ID()}, CreatedAt: now, AccessedAt: now, AbsoluteExpiresAt: now.Add(time.Hour), IdleExpiresAt: now.Add(time.Minute)}, sessions.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if created, err := oldRuntime.SessionStore().Create(ctx, record); err != nil || !created {
		t.Fatalf("old durable session: %v", err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, backend := range []helpdeskBackend{first, second} {
		go func() {
			<-start
			results <- systemstate.UpdateOperatorPermissions(ctx, backend, before, permissions)
		}()
	}
	close(start)
	wins, stale := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch}) {
			stale++
		} else {
			t.Fatalf("concurrent permission maintenance: %v", err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("expected-policy maintenance winners=%d stale=%d", wins, stale)
	}
	if _, found, err := oldRuntime.SessionStore().Load(ctx, id); err != nil || found {
		t.Fatalf("permission change retained old durable session: %v", err)
	}
	if principal, err := oldRuntime.Authenticator().Resolve(ctx, before.Principal.ID()); principal.Authenticated() || !errors.Is(err, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch}) {
		t.Fatalf("stale runtime retained grants: %v", err)
	}
}

type helpdeskClient struct {
	t           *testing.T
	application *web.Application
	cookies     map[string]*http.Cookie
	csrf        string
}

var csrfPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`)

func helpdeskHTTP(t *testing.T, application *helpdesk.Application, runtime *systemstate.Runtime, authorizer auth.Authorizer) *helpdeskClient {
	t.Helper()
	configured, err := settings.New(settings.Definition{ProjectName: "helpdesk", InstalledApps: helpdesk.InstalledApps()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := admin.SiteAllowedNextPaths(application.Registry(), "/admin")
	if err != nil {
		t.Fatal(err)
	}
	webAuth, err := websessionauth.New(websessionauth.Config{Sessions: manager, Authenticator: runtime.Authenticator(), Authorizer: authorizer, SessionCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: allowed})
	if err != nil {
		t.Fatal(err)
	}
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "helpdesk", Registry: application.Registry(), Auth: webAuth})
	if err != nil {
		t.Fatal(err)
	}
	apiAuth, err := apisessionauth.New(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	apiRoutes, err := application.APIRoutes(apiAuth)
	if err != nil {
		t.Fatal(err)
	}
	webApp, err := web.NewApplication(web.Config{Settings: configured, Routes: append(site.Routes(), apiRoutes...)})
	if err != nil {
		t.Fatal(err)
	}
	return &helpdeskClient{t: t, application: webApp, cookies: map[string]*http.Cookie{}}
}

func (c *helpdeskClient) request(method, path, body string, jsonBody bool) *httptest.ResponseRecorder {
	c.t.Helper()
	request := httptest.NewRequest(method, "http://helpdesk.test"+path, strings.NewReader(body))
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	if method == "POST" {
		if jsonBody {
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(websessionauth.DefaultCSRFHeader, c.csrf)
		} else {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	recorder := httptest.NewRecorder()
	c.application.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	if match := csrfPattern.FindStringSubmatch(recorder.Body.String()); len(match) == 2 {
		c.csrf = match[1]
	}
	return recorder
}
