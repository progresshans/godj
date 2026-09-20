package helpdesk_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
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
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
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
	sources := append([]definition.Source{systemstate.InitialDefinitionSource()}, helpdesk.MigrationSources()...)
	loaded, _, err := definition.Load(sources[:2]...)
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
	seedID, err := insertHistoricalTicket(ctx, backend, "Existing ticket", category.ID)
	if err != nil {
		t.Fatal(err)
	}
	outsideID, err := insertHistoricalTicket(ctx, backend, "Other category ticket", other.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err = definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	integerState, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0003_ticket_resolution"})))
	if err != nil {
		t.Fatal(err)
	}
	integerModel, found := integerState.Model("helpdesk", "ticket")
	if !found {
		t.Fatal("integer migration removed the model")
	}
	for _, field := range integerModel.Fields {
		if field.Name == "due_at" {
			t.Fatal("datetime field appeared before its migration")
		}
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0004_ticket_due_at"}))); err != nil {
		t.Fatal(err)
	}
	// Historical migrations expose only their own columns; the current model
	// reader also selects reviewed, service_on and service_at, which belong to later migrations.
	assertHistoricalPriority(t, ctx, backend, seedID, nil)
	setHistoricalPriority(t, ctx, backend, seedID, query.Integer(99))
	for _, name := range []string{"0005_alter_ticket_priority", "0006_alter_ticket_priority", "0004_ticket_due_at", "0006_alter_ticket_priority"} {
		state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded,
			migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: name})))
		if err != nil {
			t.Fatal(err)
		}
		model, found := state.Model("helpdesk", "ticket")
		if !found {
			t.Fatal("choice migration removed ticket")
		}
		for _, field := range model.Fields {
			if field.Name != "priority" {
				continue
			}
			if name == "0004_ticket_due_at" {
				if field.Choices != nil {
					t.Fatal("reverse metadata retained choices")
				}
			} else if len(field.Choices) != 3 {
				t.Fatal("historical choices missing")
			} else if name == "0006_alter_ticket_priority" && (field.Choices[0].Value.Integer != 1 || field.Choices[0].Label != "Urgent") {
				t.Fatal("choice label/order change missing")
			}
		}
		value := int64(99)
		assertHistoricalPriority(t, ctx, backend, seedID, &value)
	}
	setHistoricalPriority(t, ctx, backend, seedID, query.Null())

	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	seed := readGrownTicket(t, ctx, backend, seedID, "Existing ticket", category.ID)
	outside := readGrownTicket(t, ctx, backend, outsideID, "Other category ticket", other.ID)
	state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0001_initial"})))
	if err != nil {
		t.Fatal(err)
	}
	historical, found := state.Model("helpdesk", "ticket")
	if !found {
		t.Fatal("reverse field migrations removed the model")
	}
	for _, field := range historical.Fields {
		if field.Name == "priority" || field.Name == "resolution" || field.Name == "due_at" || field.Name == "reviewed" || field.Name == "service_on" || field.Name == "service_at" || field.Name == "elapsed" {
			t.Fatal("reverse field migrations retained a later column")
		}
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	readGrownTicket(t, ctx, backend, seedID, "Existing ticket", category.ID)
	readGrownTicket(t, ctx, backend, outsideID, "Other category ticket", other.ID)
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
	verifyHelpdeskEagerCount(t, ctx, reads, category, seed)
	application, err := helpdesk.New(reads, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	categoryInfo, ok := application.Registry().Lookup("helpdesk", "category")
	if !ok || !categoryInfo.ReadOnly || len(categoryInfo.FormFields) != 0 {
		t.Fatal("Category should need no form/CRUD adapter")
	}
	ticketInfo, ok := application.Registry().Lookup("helpdesk", "ticket")
	if !ok || len(ticketInfo.FormFields) != 12 {
		t.Fatal("Ticket scalar selection")
	}
	for _, field := range ticketInfo.FormFields {
		if field.Name() == "priority" && (field.Widget() != forms.Select || len(field.Choices()) != 3) {
			t.Fatal("priority metadata did not select choices")
		}
		if field.Name() == "resolution" && field.Widget() != forms.Textarea {
			t.Fatal("model Text did not select a textarea")
		}
		if field.Name() == "category" || field.Name() == "id" {
			t.Fatal("relation/key became writable")
		}
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	assertHelpdeskOperationContracts(t, client.document, client.api.Routes())
	if response := client.request("GET", "/api/tickets/", "", false); response.Code != http.StatusForbidden {
		t.Fatalf("anonymous API: %d", response.Code)
	} else {
		assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/", response)
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
	listRequest := httptest.NewRequest(http.MethodGet, "http://helpdesk.test/api/tickets/?search=ignored&page=999&unknown=ignored", nil)
	listRequest.Header.Set("Accept", "text/html")
	for _, cookie := range client.cookies {
		listRequest.AddCookie(cookie)
	}
	listResponse := httptest.NewRecorder()
	client.application.ServeHTTP(listResponse, listRequest)
	assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/", listResponse)
	var listed []struct {
		ID       int64 `json:"id"`
		Category int64 `json:"category"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil || listResponse.Code != http.StatusOK || len(listed) != 1 || listed[0].ID != seed.ID || listed[0].Category != category.ID {
		t.Fatalf("documented bare list, ignored query, or absent Accept policy changed: %d %s %v", listResponse.Code, listResponse.Body, err)
	}
	beforeDetail = reads.queries
	detailResponse := client.request("GET", detailPath, "", false)
	assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/{id}/", detailResponse)
	var detail map[string]map[string]json.RawMessage
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil || detailResponse.Code != http.StatusOK || reads.queries != beforeDetail+1 {
		t.Fatalf("joined detail: status=%d queries=%d body=%s err=%v", detailResponse.Code, reads.queries-beforeDetail, detailResponse.Body, err)
	}
	if len(detail) != 2 || len(detail["ticket"]) != 14 || len(detail["category"]) != 2 || string(detail["ticket"]["id"]) != strconv.FormatInt(seed.ID, 10) || string(detail["ticket"]["details"]) != "null" || string(detail["ticket"]["priority"]) != "null" || string(detail["ticket"]["resolution"]) != "null" || string(detail["ticket"]["due_at"]) != "null" || string(detail["ticket"]["reviewed"]) != "null" || string(detail["ticket"]["service_on"]) != "null" || string(detail["ticket"]["service_at"]) != "null" || string(detail["ticket"]["elapsed"]) != "null" || string(detail["ticket"]["effort"]) != "null" || string(detail["ticket"]["expected_cost"]) != "null" || string(detail["category"]["id"]) != strconv.FormatInt(category.ID, 10) {
		t.Fatalf("detail output fields/values: %s", detailResponse.Body)
	}
	var categoryName string
	if err := json.Unmarshal(detail["category"]["name"], &categoryName); err != nil || categoryName != category.Name {
		t.Fatalf("category label = %q, %v", categoryName, err)
	}
	if limit, _ := reads.last.Limit(); limit != 1 {
		t.Fatalf("detail limit = %d", limit)
	}
	if projections := reads.last.RelationProjections(); len(projections) != 1 || projections[0].TerminalHop().Field() != "category" {
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
	if response := client.request("GET", "/admin/tickets/add/", "", false); response.Code != http.StatusOK || strings.Contains(response.Body.String(), `name="category"`) || !strings.Contains(response.Body.String(), `<select name="priority">`) || !strings.Contains(response.Body.String(), `<textarea`) || !strings.Contains(response.Body.String(), `name="resolution"`) {
		t.Fatalf("selected ticket form: %d %s", response.Code, response.Body)
	}
	values := url.Values{"subject": {"Admin ticket"}, "due_at": {"0001-01-01T00:00:00Z"}, "details": {""}, "closed": {"false"}, "priority": {"1"}, "csrfmiddlewaretoken": {client.csrf}, "category": {strconv.FormatInt(other.ID, 10)}}
	if response := client.request("POST", "/admin/tickets/add/", values.Encode(), false); response.Code != http.StatusBadRequest {
		t.Fatalf("injected relationship accepted: %d", response.Code)
	}
	values.Del("category")
	if response := client.request("POST", "/admin/tickets/add/", values.Encode(), false); response.Code != http.StatusFound {
		t.Fatalf("Admin create: %d %s", response.Code, response.Body)
	}
	if response := client.request("GET", "/admin/tickets/", "", false); response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Other category ticket") || !strings.Contains(response.Body.String(), "Urgent") || !strings.Contains(response.Body.String(), "0001-01-01T00:00:00.000000Z") {
		t.Fatalf("scoped Admin list: %d %s", response.Code, response.Body)
	}
	if response := client.request("GET", fmt.Sprintf("/admin/tickets/change/?id=%d", outside.ID), "", false); response.Code != http.StatusNotFound {
		t.Fatalf("unselected category leaked through direct object read: %d", response.Code)
	}
	resolution := "first line\n" + strings.Repeat("Long explanation. ", 100) + "\n</textarea><script>alert(1)</script>&\""
	createJSON, err := json.Marshal(map[string]any{"subject": "JSON ticket", "details": nil, "priority": int64(-1), "resolution": resolution, "due_at": "2026-09-19T12:34:56.123456789+09:00"})
	if err != nil {
		t.Fatal(err)
	}
	response := client.request("POST", "/api/tickets/", string(createJSON), true)
	assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", response)
	if response.Code != http.StatusCreated {
		t.Fatalf("JSON create: %d %s", response.Code, response.Body)
	}
	var created struct {
		ID         int64      `json:"id"`
		Category   int64      `json:"category"`
		Closed     bool       `json:"closed"`
		Details    *string    `json:"details"`
		Priority   *int64     `json:"priority"`
		Resolution *string    `json:"resolution"`
		DueAt      *time.Time `json:"due_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID <= seed.ID || created.Category != category.ID || created.Closed || created.Details != nil || created.Priority == nil || *created.Priority != -1 || created.Resolution == nil || *created.Resolution != resolution || created.DueAt == nil || !created.DueAt.Equal(time.Date(2026, 9, 19, 3, 34, 56, 123456000, time.UTC)) {
		t.Fatalf("typed JSON projection: %+v %v", created, err)
	}
	if response.Header().Get("Location") != "" {
		t.Fatal("create added an undocumented Location header")
	}
	for _, document := range []string{`{"subject":"Bad","due_at":0}`, `{"subject":"Bad","due_at":"2026-09-19"}`, `{"subject":"Bad","due_at":"2026-02-30T00:00:00Z"}`, `{"subject":"Bad","due_at":"0001-01-01T00:00:00+01:00"}`, `{"subject":"Bad","resolution":17}`, `{"subject":"Bad","resolution":"bad\u0000text"}`} {
		if response := client.request("POST", "/api/tickets/", document, true); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid Text accepted: %d %s", response.Code, response.Body)
		}
	}
	if response := client.request("POST", "/api/tickets/", `{"subject":"Too large","resolution":"`+strings.Repeat("x", 4096)+`"}`, true); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("unbounded Text bypassed the HTTP body budget: %d", response.Code)
	} else {
		assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", response)
	}
	for _, document := range []string{`{"subject":"Bad","priority":99}`, `{"subject":"Bad","priority":9223372036854775807}`, `{"subject":"Bad","priority":-9223372036854775808}`, `{"subject":"Bad","priority":9223372036854775808}`, `{"subject":"Bad","priority":-9223372036854775809}`, `{"subject":"Bad","priority":1.0}`, `{"subject":"Bad","priority":"1"}`, `{"subject":"Bad","priority":true}`, `{"subject":"Bad","category":` + strconv.FormatInt(other.ID, 10) + `}`, `{"subject":"Bad","id":1}`, `{"subject":"` + strings.Repeat("x", 121) + `"}`} {
		if response := client.request("POST", "/api/tickets/", document, true); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid API data accepted: %d %s", response.Code, response.Body)
		} else {
			assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", response)
		}
	}
	changePath := fmt.Sprintf("/admin/tickets/change/?id=%d", created.ID)
	if response := client.request("GET", changePath, "", false); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<option value="-1" selected>Low</option>`) || !strings.Contains(response.Body.String(), `<select name="priority">`) || !strings.Contains(response.Body.String(), `value="2026-09-19T03:34:56.123456Z"`) || !strings.Contains(response.Body.String(), "UTC if no offset is provided.") {
		t.Fatalf("metadata-derived initial values: %d %s", response.Code, response.Body)
	} else if !strings.Contains(response.Body.String(), ">\n"+html.EscapeString(resolution)+"</textarea>") || strings.Contains(response.Body.String(), "<script>") {
		t.Fatal("textarea lost its content/newline prefix or failed to escape HTML")
	}
	updatedResolution := "Resolved first line\n" + resolution
	values = url.Values{"subject": {""}, "details": {"done"}, "closed": {"true"}, "priority": {"+000.0"}, "resolution": {"\n\n" + updatedResolution}, "due_at": {"9999-12-31 23:59:59.999999"}, "csrfmiddlewaretoken": {client.csrf}}
	if response := client.request("POST", changePath, values.Encode(), false); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), ">\n\n\n"+html.EscapeString(updatedResolution)+"</textarea>") || strings.Contains(response.Body.String(), "<script>") {
		t.Fatalf("invalid form did not preserve escaped raw Text: %d", response.Code)
	}
	unchanged, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || unchanged.Subject != "JSON ticket" || unchanged.Priority == nil || *unchanged.Priority != -1 || unchanged.Resolution == nil || *unchanged.Resolution != resolution || unchanged.DueAt == nil || !unchanged.DueAt.Equal(*created.DueAt) {
		t.Fatal("invalid multiline form changed stored data")
	}
	values.Set("priority", "0")
	values.Set("subject", "Resolved")
	values.Set("due_at", "2026-02-30 10:11:12")
	if response := client.request("POST", changePath, values.Encode(), false); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="due_at"`) {
		t.Fatalf("invalid datetime form accepted: %d", response.Code)
	}
	afterInvalid, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || afterInvalid.Subject != unchanged.Subject || afterInvalid.DueAt == nil || !afterInvalid.DueAt.Equal(*unchanged.DueAt) {
		t.Fatal("invalid datetime form changed stored data")
	}
	values.Set("due_at", "9999-12-31 23:59:59.999999")
	if response := client.request("POST", changePath, values.Encode(), false); response.Code != http.StatusFound {
		t.Fatalf("Admin update: %d %s", response.Code, response.Body)
	}
	stored, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.CategoryID != category.ID || stored.Subject != "Resolved" || !stored.Closed || stored.Details == nil || *stored.Details != "done" || stored.Priority == nil || *stored.Priority != 0 || stored.Resolution == nil || *stored.Resolution != updatedResolution || stored.DueAt == nil || !stored.DueAt.Equal(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)) {
		t.Fatalf("real stored mutation: %+v %v", stored, err)
	}
	type timeBounds struct{ Min, Max orm.Optional[time.Time] }
	bounds, err := orm.AggregateInto(ctx, models.TicketObjects.Using(backend), orm.Aggregate2(orm.Min(models.TicketFields.DueAt), orm.Max(models.TicketFields.DueAt), func(min, max orm.Optional[time.Time]) timeBounds { return timeBounds{min, max} }))
	if err != nil {
		t.Fatal("aggregate nullable datetime on the real database:", err)
	}
	if minimum, ok := bounds.Min.Get(); !ok || !minimum.Equal(time.Time{}) {
		t.Fatal("MIN datetime lost the valid year 1 value")
	}
	if maximum, ok := bounds.Max.Get(); !ok || !maximum.Equal(*stored.DueAt) {
		t.Fatal("MAX datetime lost the valid year 9999 value")
	}
	values.Set("priority", "")
	values.Set("resolution", "")
	values.Set("due_at", "")
	if response := client.request("POST", changePath, values.Encode(), false); response.Code != http.StatusFound {
		t.Fatalf("clear integer priority: %d %s", response.Code, response.Body)
	}
	stored, found, err = models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Priority != nil || stored.Resolution == nil || *stored.Resolution != "" || stored.DueAt != nil {
		t.Fatal("blank form did not preserve integer null versus Text empty string")
	}
	if count, err := models.TicketObjects.Using(backend).Count(ctx); err != nil || count != 4 {
		t.Fatalf("invalid requests changed rows: %d %v", count, err)
	}
	verifyHelpdeskNullableBoolean(t, ctx, runtime, open, client, category.ID, created.ID, outside.ID)
	verifyHelpdeskCalendarDate(t, ctx, runtime, open, client, category.ID, created.ID)
	verifyHelpdeskClockTime(t, ctx, runtime, open, client, category.ID, created.ID)
	verifyHelpdeskDuration(t, ctx, runtime, open, client, category.ID, created.ID)
	verifyHelpdeskFloat(t, ctx, runtime, open, client, category.ID, created.ID)
	verifyHelpdeskDecimal(t, ctx, runtime, open, client, category.ID, created.ID, outside.ID)
	// Ordinary ORM writes keep the complete int64 storage range. Both API and
	// Admin display those old/out-of-choice values without substituting labels.
	for _, value := range []int64{math.MinInt64, math.MaxInt64} {
		legacy, err := models.TicketObjects.Create(ctx, backend, models.NewTicketCreate("Legacy priority", category.ID).WithPriority(value))
		if err != nil {
			t.Fatalf("choices incorrectly constrained ORM save: %v", err)
		}
		response := client.request("GET", fmt.Sprintf("/api/tickets/%d/", legacy.ID), "", false)
		var detail struct{ Ticket struct{ Priority *int64 } }
		if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil || response.Code != http.StatusOK || detail.Ticket.Priority == nil || *detail.Ticket.Priority != value {
			t.Fatalf("out-of-choice response: %d %v", response.Code, err)
		}
		assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/{id}/", response)
		response = client.request("GET", fmt.Sprintf("/admin/tickets/change/?id=%d", legacy.ID), "", false)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<option value="`+strconv.FormatInt(value, 10)+`" selected>`) {
			t.Fatal("old priority silently selected another choice")
		}
	}
}

// Seed through the historical column set before the new generated model can be
// used. This makes the upgrade preserve actual pre-existing rows on both DBs.
func insertHistoricalTicket(ctx context.Context, backend db.Mutator, subject string, category int64) (int64, error) {
	return backend.Insert(ctx, query.NewInsertPlanReturningKey("helpdesk_ticket", []query.Assignment{
		query.NewAssignment(query.NewFieldRef("subject", "subject", query.FieldString, false), query.String(subject)),
		query.NewAssignment(query.NewFieldRef("details", "details", query.FieldString, true), query.Null()),
		query.NewAssignment(query.NewFieldRef("closed", "closed", query.FieldBoolean, false), query.Boolean(false)),
		query.NewAssignment(query.NewFieldRef("category", "category_id", query.FieldInteger, false), query.Integer(category)),
	}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
}

func setHistoricalPriority(t *testing.T, ctx context.Context, backend db.Mutator, id int64, value query.Value) {
	t.Helper()
	count, err := backend.Update(ctx, query.NewUpdatePlan("helpdesk_ticket", []query.Assignment{
		query.NewAssignment(query.NewFieldRef("priority", "priority", query.FieldInteger, true), value),
	}, query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(id)))
	if err != nil || count != 1 {
		t.Fatalf("historical priority update: %d %v", count, err)
	}
}

func assertHistoricalPriority(t *testing.T, ctx context.Context, backend db.Queryer, id int64, expected *int64) {
	t.Helper()
	key := query.NewFieldRef("id", "id", query.FieldInteger, false)
	priority := query.NewFieldRef("priority", "priority", query.FieldInteger, true)
	plan, err := query.NewPlan("helpdesk_ticket", []query.FieldRef{key, priority}).WithConditions(query.NewCondition(key, query.LookupExact, query.Integer(id)))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var storedID int64
	var stored sql.NullInt64
	if !rows.Next() {
		t.Fatalf("historical row absent: %v", rows.Err())
	}
	if err := rows.Scan(&storedID, &stored); err != nil {
		t.Fatal(err)
	}
	if storedID != id || stored.Valid != (expected != nil) || expected != nil && stored.Int64 != *expected {
		t.Fatal("historical choices changed stored priority")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("historical lookup returned extra rows or failed")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func readGrownTicket(t *testing.T, ctx context.Context, backend db.Queryer, id int64, subject string, category int64) models.Ticket {
	t.Helper()
	value, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || value.Subject != subject || value.CategoryID != category || value.Details != nil || value.Closed || value.Priority != nil || value.Resolution != nil || value.DueAt != nil || value.Reviewed != nil || value.ServiceOn != nil {
		t.Fatalf("field migrations did not preserve the historical row and NULL backfill: %v", err)
	}
	return value
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
	api         *helpdesk.API
	document    helpdeskDocument
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
	adapter, err := application.API(apiAuth)
	if err != nil {
		t.Fatal(err)
	}
	document, err := adapter.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	webApp, err := web.NewApplication(web.Config{Settings: configured, Routes: append(site.Routes(), adapter.Routes()...)})
	if err != nil {
		t.Fatal(err)
	}
	return &helpdeskClient{t: t, application: webApp, api: adapter, document: decodeHelpdeskDocument(t, document.Bytes()), cookies: map[string]*http.Cookie{}}
}

func (c *helpdeskClient) request(method, path, body string, jsonBody bool) *httptest.ResponseRecorder {
	c.t.Helper()
	request := httptest.NewRequest(method, "http://helpdesk.test"+path, strings.NewReader(body))
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	if method == "POST" || method == "PUT" || method == "PATCH" {
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
