package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/uuid"
)

func readUniqueTicket(t *testing.T, ctx context.Context, backend db.Queryer, id int64) models.Ticket {
	t.Helper()
	row, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal("unique ticket missing", err)
	}
	return row
}

func verifyHistoricalExternalReferenceUniqueness(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, firstID, secondID int64) {
	t.Helper()
	beforeFirst, beforeSecond := readUniqueTicket(t, ctx, backend, firstID), readUniqueTicket(t, ctx, backend, secondID)
	executor := migrations.Executor{Backend: backend}
	target := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0015_ticket_external_payload"}))
	beforeState, err := executor.Migrate(ctx, loaded, target)
	if err != nil {
		t.Fatal(err)
	}
	reference := uuid.UUID{0: 0x63, 15: 0x95}
	for _, row := range []models.Ticket{beforeFirst, beforeSecond} {
		if _, err := models.TicketObjects.Update(ctx, backend, row, models.TicketPatch{}.WithExternalReference(reference)); err != nil {
			t.Fatal("historical plain UUID disallowed duplicates", err)
		}
	}
	duplicateFirst, duplicateSecond := readUniqueTicket(t, ctx, backend, firstID), readUniqueTicket(t, ctx, backend, secondID)
	failedState, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err == nil || !failedState.Equal(beforeState) || !reflect.DeepEqual(duplicateFirst, readUniqueTicket(t, ctx, backend, firstID)) || !reflect.DeepEqual(duplicateSecond, readUniqueTicket(t, ctx, backend, secondID)) {
		t.Fatal("unique growth altered duplicate data or published state", err)
	}
	if _, err := models.TicketObjects.Update(ctx, backend, duplicateSecond, models.TicketPatch{}.WithExternalReferenceNull()); err != nil {
		t.Fatal(err)
	}
	state, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatal("explicit data correction did not permit retry", err)
	}
	model, found := state.Model("helpdesk", "ticket")
	unique := false
	for _, field := range model.Fields {
		if field.Name == "external_reference" {
			unique = field.Unique
		}
	}
	if !found || !unique {
		t.Fatal("current state omitted uniqueness")
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored := readUniqueTicket(t, ctx, reopened, firstID)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if stored.ExternalReference == nil || *stored.ExternalReference != reference {
		t.Fatal("unique migration lost existing reference")
	}
	_, err = models.TicketObjects.Update(ctx, backend, duplicateSecond, models.TicketPatch{}.WithExternalReference(reference))
	if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
		t.Fatal("historical unique migration did not enforce its declaration", err)
	}
	for _, row := range []*models.Ticket{&beforeFirst, &beforeSecond} {
		if err := models.TicketObjects.Save(ctx, backend, row); err != nil {
			t.Fatal(err)
		}
	}
}

type uniqueHelpdeskBackend struct {
	helpdesk.Backend
	mode                                   string
	transactions, checks, inserts, updates int
}

func (b *uniqueHelpdeskBackend) Atomic(ctx context.Context, fn func(db.Session) error) error {
	b.transactions++
	err := b.Backend.Atomic(ctx, func(session db.Session) error { return fn(uniqueHelpdeskSession{Session: session, owner: b}) })
	if b.mode == "rollback_unknown" && err != nil {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown, Detail: "injected rollback uncertainty"})
	}
	return err
}

type uniqueHelpdeskSession struct {
	db.Session
	owner *uniqueHelpdeskBackend
}

func (s uniqueHelpdeskSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if plan.ResultShape().Kind() == query.ResultProjection {
		for _, condition := range plan.Conditions() {
			if condition.Field().Name() == "external_reference" {
				s.owner.checks++
				if s.owner.mode == "query_error" {
					return nil, errors.New("injected unique lookup failure")
				}
				if s.owner.mode == "stale_read" || s.owner.mode == "rollback_unknown" {
					return uniqueEmptyRows{}, nil
				}
			}
		}
	}
	return s.Session.Query(ctx, plan)
}
func (s uniqueHelpdeskSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	s.owner.inserts++
	return s.Session.Insert(ctx, plan)
}
func (s uniqueHelpdeskSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	s.owner.updates++
	return s.Session.Update(ctx, plan)
}

type uniqueEmptyRows struct{}

func (uniqueEmptyRows) Next() bool        { return false }
func (uniqueEmptyRows) Err() error        { return nil }
func (uniqueEmptyRows) Close() error      { return nil }
func (uniqueEmptyRows) Scan(...any) error { return errors.New("empty rows cannot scan") }

func verifyHelpdeskUnique(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id, outsideID int64) {
	t.Helper()
	baseline := readUniqueTicket(t, ctx, runtime, id)
	outside := readUniqueTicket(t, ctx, runtime, outsideID)
	reference := uuid.UUID{0: 0xad, 15: 0x95}
	owner, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("private uniqueness owner", outside.CategoryID).WithExternalReference(reference))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := models.TicketObjects.Delete(ctx, runtime, &owner); err != nil {
			t.Error(err)
		}
		if err := models.TicketObjects.Save(ctx, runtime, &baseline); err != nil {
			t.Error(err)
		}
	}()
	backend := &uniqueHelpdeskBackend{Backend: runtime}
	application, err := helpdesk.New(backend, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	path := fmt.Sprintf("/api/tickets/%d/", id)
	changePath := fmt.Sprintf("/admin/tickets/change/?id=%d", id)
	if response := client.request("GET", changePath, "", false); response.Code != http.StatusOK {
		t.Fatal("initialize unique consumer CSRF", response.Code)
	}
	duplicate := `{"subject":"must not change","external_reference":"{` + strings.ToUpper(reference.String()) + `}"}`
	checkError := func(status int, body []byte, field string) {
		t.Helper()
		var reply struct {
			Code   string `json:"code"`
			Errors []struct {
				Field, Code string
				Params      []any
			} `json:"errors"`
		}
		if status != http.StatusBadRequest || json.Unmarshal(body, &reply) != nil || reply.Code != "validation_error" || len(reply.Errors) != 1 || reply.Errors[0].Field != field || reply.Errors[0].Code != "unique" || len(reply.Errors[0].Params) != 0 || strings.Contains(string(body), reference.String()) || strings.Contains(string(body), owner.Subject) {
			t.Fatalf("unexpected unique response: %d %s", status, body)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH"} {
		target := path
		route := "/api/tickets/{id}/"
		if method == "POST" {
			target, route = "/api/tickets/", "/api/tickets/"
		}
		writes := backend.inserts + backend.updates
		response := client.request(method, target, duplicate, true)
		checkError(response.Code, response.Body.Bytes(), "external_reference")
		assertHelpdeskResponseDocumented(t, client.document, method, route, response)
		if backend.inserts+backend.updates != writes || !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, id)) {
			t.Fatal("advisory rejection wrote fields")
		}
	}
	form := url.Values{"subject": {`<script>retain my input</script>`}, "external_reference": {"{" + strings.ToUpper(reference.String()) + "}"}, "csrfmiddlewaretoken": {client.csrf}}
	for _, target := range []string{"/admin/tickets/add/", changePath} {
		response := client.request("POST", target, form.Encode(), false)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="external_reference"`) || !strings.Contains(response.Body.String(), `data-error-code="unique"`) || !strings.Contains(response.Body.String(), html.EscapeString(form.Get("subject"))) || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), form.Get("external_reference")) || !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, id)) {
			t.Fatal("Admin uniqueness did not preserve safe raw input", response.Code, response.Body)
		}
	}
	// Suppress only the advisory read to represent a competing write after a
	// successful check. The actual DB constraint must reject insert and update.
	backend.mode = "stale_read"
	for _, method := range []string{"POST", "PATCH"} {
		target := path
		if method == "POST" {
			target = "/api/tickets/"
		}
		response := client.request(method, target, duplicate, true)
		checkError(response.Code, response.Body.Bytes(), "__all__")
		if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, id)) {
			t.Fatal("native conflict persisted partial changes")
		}
	}
	response := client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="__all__"`) || !strings.Contains(response.Body.String(), `data-error-code="unique"`) {
		t.Fatal("native Admin conflict was not an input-level error", response.Code, response.Body)
	}
	for _, mode := range []string{"query_error", "rollback_unknown"} {
		backend.mode = mode
		for _, target := range []string{path, changePath} {
			method, body, jsonInput := "PATCH", duplicate, true
			if target == changePath {
				method, body, jsonInput = "POST", form.Encode(), false
			}
			response := client.request(method, target, body, jsonInput)
			if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), `data-error-code="unique"`) || !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, id)) {
				t.Fatal("execution failure was reduced to a uniqueness rejection", mode, response.Code, response.Body)
			}
		}
	}
	backend.mode = ""
	before := backend.checks
	response = client.request("PATCH", fmt.Sprintf("/api/tickets/%d/", outsideID), duplicate, true)
	if response.Code != http.StatusNotFound || backend.checks != before {
		t.Fatal("uniqueness read preceded category authorization")
	}
	csrf := client.csrf
	client.csrf = "invalid"
	transactions := backend.transactions
	response = client.request("POST", "/api/tickets/", duplicate, true)
	client.csrf = csrf
	if response.Code != http.StatusForbidden || backend.transactions != transactions || backend.checks != before || !strings.Contains(response.Body.String(), `"code":"csrf_rejected"`) {
		t.Fatal("uniqueness read bypassed CSRF")
	}
	denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: helpdesk.ChangeTicket})
	denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
	if response := denied.request("GET", "/admin/tickets/", "", false); response.Code != http.StatusOK {
		t.Fatal("initialize denied consumer CSRF", response.Code)
	}
	response = denied.request("PATCH", path, duplicate, true)
	if response.Code != http.StatusForbidden || backend.transactions != transactions || backend.checks != before || !strings.Contains(response.Body.String(), `"code":"permission_denied"`) {
		t.Fatal("uniqueness read bypassed permission")
	}
	for _, body := range []string{`{"external_reference":0}`, `{"external_reference":"00000000000000000000000000000000","subject":"own zero reference"}`, `{}`, `{"external_reference":null}`} {
		response = client.request("PATCH", path, body, true)
		if response.Code != http.StatusOK {
			t.Fatal("self/zero/null uniqueness flow failed", response.Code, response.Body)
		}
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored := readUniqueTicket(t, ctx, reopened, id)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if stored.ExternalReference != nil || stored.Subject != "own zero reference" {
		t.Fatal("successful unique edit was not durable")
	}
}
