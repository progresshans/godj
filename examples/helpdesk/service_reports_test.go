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
)

func verifyHistoricalServiceReportLifecycle(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, ticketID int64) {
	t.Helper()
	baseline := readUniqueTicket(t, ctx, backend, ticketID)
	report, err := models.ServiceReportObjects.Create(ctx, backend, models.NewServiceReportCreate(ticketID, "Historical report").WithCompleted(true))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, readErr := models.ServiceReportObjects.Using(reopened).Filter(models.ServiceReportFields.ID.Exact(report.ID)).OrderBy(models.ServiceReportFields.ID.Asc()).First(ctx)
	closeErr := reopened.Close()
	if readErr != nil || closeErr != nil || !found || !reflect.DeepEqual(stored, report) {
		t.Fatal("report not durable after reopen", readErr, closeErr)
	}
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0016_alter_ticket_external_reference"})))
	if err != nil {
		t.Fatal("reverse report migration", err)
	}
	if _, found := state.Model("helpdesk", "service_report"); found {
		t.Fatal("reverse retained report model")
	}
	if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, backend, ticketID)) {
		t.Fatal("report reversal changed parent")
	}
	state, err = executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatal("report reapply", err)
	}
	if _, found := state.Model("helpdesk", "service_report"); !found {
		t.Fatal("report model missing after reapply")
	}
	if n, err := models.ServiceReportObjects.Using(backend).Count(ctx); err != nil || n != 0 {
		t.Fatal("reapplied report table unexpected rows", n, err)
	}
	if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, backend, ticketID)) {
		t.Fatal("report reapply changed parent")
	}
}

type reportConsumerValue struct {
	ID        int64  `json:"id"`
	Ticket    int64  `json:"ticket"`
	Summary   string `json:"summary"`
	Completed bool   `json:"completed"`
}

func reportInput(ticket int64, summary string) string {
	encoded, _ := json.Marshal(map[string]any{"ticket": ticket, "summary": summary})
	return string(encoded)
}

type reportFaultBackend struct {
	helpdesk.Backend
	mode                                             string
	stageID, moveCategory                            int64
	queries, transactions, checks, writes, relations int
}

func (b *reportFaultBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b.queries++
	if b.mode == "choice_error" {
		return nil, errors.New("choice query failed")
	}
	return b.Backend.Query(ctx, plan)
}
func (b *reportFaultBackend) Atomic(ctx context.Context, fn func(db.Session) error) error {
	b.transactions++
	err := b.Backend.Atomic(ctx, func(session db.Session) error {
		if b.stageID != 0 {
			row, found, err := models.TicketObjects.Using(session).Filter(models.TicketFields.ID.Exact(b.stageID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("staged parent missing")
			}
			patch := models.TicketPatch{}.WithSubject("must roll back")
			if b.moveCategory != 0 {
				patch = patch.WithCategoryID(b.moveCategory)
			}
			if _, err = models.TicketObjects.Update(ctx, session, row, patch); err != nil {
				return err
			}
		}
		return fn(reportFaultSession{Session: session, owner: b})
	})
	if err != nil && (b.mode == "rollback_unknown" || b.mode == "notfound_unknown") {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	return err
}
func (b *reportFaultBackend) AtomicRelation(ctx context.Context, fn func(db.RelationSession) error) error {
	b.relations++
	err := b.Backend.AtomicRelation(ctx, fn)
	if err != nil && b.mode == "protected_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	return err
}

type reportFaultSession struct {
	db.Session
	owner *reportFaultBackend
}

func (s reportFaultSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	s.owner.queries++
	if plan.ResultShape().Kind() == query.ResultProjection {
		for _, condition := range plan.Conditions() {
			if condition.Field().Name() == "ticket" {
				s.owner.checks++
				switch s.owner.mode {
				case "query_error":
					return nil, errors.New("report uniqueness query failed")
				case "cancel":
					return nil, context.Canceled
				case "native", "rollback_unknown":
					return uniqueEmptyRows{}, nil
				}
			}
		}
	}
	return s.Session.Query(ctx, plan)
}
func (s reportFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "driver_error" {
		return 0, errors.New("write driver failed")
	}
	return s.Session.Insert(ctx, plan)
}
func (s reportFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "driver_error" {
		return 0, errors.New("write driver failed")
	}
	return s.Session.Update(ctx, plan)
}

func verifyHelpdeskReports(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, outsideID int64) {
	t.Helper()
	first, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("<script>report ticket & label</script>", categoryID))
	if err != nil {
		t.Fatal(err)
	}
	second, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Report replacement", categoryID))
	if err != nil {
		t.Fatal(err)
	}
	owned := map[int64]bool{first.ID: true, second.ID: true, outsideID: true}
	defer func() {
		reports, err := models.ServiceReportObjects.Using(runtime).OrderBy(models.ServiceReportFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		for _, row := range reports {
			if owned[row.TicketID] {
				if _, err := models.ServiceReportObjects.Delete(ctx, runtime, &row); err != nil {
					t.Error(err)
				}
			}
		}
		for _, id := range []int64{first.ID, second.ID} {
			row, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
			if err != nil {
				t.Error(err)
			} else if found {
				if _, err := deleteHelpdeskTicket(ctx, runtime, &row); err != nil {
					t.Error(err)
				}
			}
		}
	}()
	outside, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(outsideID, "private other category report"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &reportFaultBackend{Backend: runtime}
	application, err := helpdesk.New(backend, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	route := "/api/service-reports/"
	reverse := fmt.Sprintf("/api/tickets/%d/service-report/", first.ID)
	for _, entry := range []struct {
		path   string
		status int
		body   string
	}{{reverse, 200, "null"}, {fmt.Sprintf("/api/tickets/%d/service-report/", outsideID), 404, ""}, {"/api/tickets/9223372036854775807/service-report/", 404, ""}} {
		response := client.request("GET", entry.path, "", false)
		if response.Code != entry.status || (entry.body != "" && strings.TrimSpace(response.Body.String()) != entry.body) {
			t.Fatal("reverse absence/scope", response.Code, response.Body)
		}
		assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/{id}/service-report/", response)
	}
	response := client.request("GET", "/admin/service-reports/add/", "", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `<select name="ticket" required>`) || !strings.Contains(response.Body.String(), html.EscapeString(first.Subject)) || strings.Contains(response.Body.String(), "Other category ticket") || strings.Contains(response.Body.String(), "<script>") {
		t.Fatal("chooser scope/escaping", response.Code, response.Body)
	}
	for _, permission := range []auth.Permission{helpdesk.AddServiceReport, helpdesk.ViewTicket} {
		denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: permission})
		denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
		before := backend.queries
		response := denied.request("GET", "/admin/service-reports/add/", "", false)
		if response.Code != http.StatusForbidden || backend.queries != before {
			t.Fatal("choice permission queried product data", permission, response.Code)
		}
	}
	// API accepts a scoped key without enumerating ticket labels.
	noLabels := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: helpdesk.ViewTicket})
	noLabels.cookies, noLabels.csrf = maps.Clone(client.cookies), client.csrf
	if response := noLabels.request("GET", "/admin/", "", false); response.Code != 200 {
		t.Fatal("initialize per-runtime CSRF", response.Code)
	}
	response = noLabels.request("POST", route, reportInput(first.ID, "  First report\nSecond line  "), true)
	var report reportConsumerValue
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &report) != nil || report.ID <= 0 || report.Ticket != first.ID || report.Summary != "First report\nSecond line" || report.Completed {
		t.Fatal("report create", response.Code, response.Body)
	}
	assertHelpdeskResponseDocumented(t, client.document, "POST", route, response)
	path := fmt.Sprintf("/api/service-reports/%d/", report.ID)
	checkFailure := func(responseCode int, body []byte, field, code string) {
		t.Helper()
		var result struct {
			Code   string
			Errors []struct {
				Field, Code string
				Params      []any
			}
		}
		if responseCode != 400 || json.Unmarshal(body, &result) != nil || result.Code != "validation_error" || len(result.Errors) != 1 || result.Errors[0].Field != field || result.Errors[0].Code != code || len(result.Errors[0].Params) != 0 {
			t.Fatal("report validation", responseCode, string(body))
		}
	}
	for _, id := range []int64{0, -1, outsideID, 9223372036854775807} {
		response = client.request("POST", route, reportInput(id, "invalid relation"), true)
		checkFailure(response.Code, response.Body.Bytes(), "ticket", "invalid_choice")
	}
	response = client.request("POST", route, reportInput(first.ID, "duplicate"), true)
	checkFailure(response.Code, response.Body.Bytes(), "ticket", "unique")
	if response = client.request("PATCH", path, reportInput(first.ID, "Self update"), true); response.Code != 200 {
		t.Fatal("self uniqueness", response.Code, response.Body)
	}
	if response = client.request("PATCH", path, `{}`, true); response.Code != 200 {
		t.Fatal("no-op report update", response.Code, response.Body)
	}
	for _, body := range []string{`{"id":5}`, `{"summary":null}`, `{"ticket":null}`, `{"ticket":1,"ticket":2}`, `{"unknown":true}`, `{"summary":"ok"} {}`, `[]`} {
		if response = client.request("PATCH", path, body, true); response.Code != 400 {
			t.Fatal("invalid report input accepted", body, response.Code, response.Body)
		}
	}
	if response = client.request("PUT", path, `{"summary":"missing ticket"}`, true); response.Code != 400 {
		t.Fatal("full update allowed omission")
	}
	if response = client.request("POST", route, reportInput(second.ID, strings.Repeat("x", 4096)), true); response.Code != 413 {
		t.Fatal("report body limit", response.Code)
	}
	if response = client.request("GET", route+"?category=anything", "", false); response.Code != 400 {
		t.Fatal("undeclared query parameter accepted")
	}
	for _, method := range []string{"GET", "PUT", "PATCH", "DELETE"} {
		response = client.request(method, fmt.Sprintf("/api/service-reports/%d/", outside.ID), reportInput(first.ID, "forbidden"), method != "GET")
		if response.Code != 404 {
			t.Fatal("foreign report exposed", method, response.Code, response.Body)
		}
	}
	response = client.request("GET", route, "", false)
	var list []reportConsumerValue
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].ID != report.ID {
		t.Fatal("report list scope", response.Code, response.Body)
	}
	response = client.request("GET", reverse, "", false)
	var reverseValue reportConsumerValue
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &reverseValue) != nil || reverseValue.ID != report.ID {
		t.Fatal("reverse report", response.Code, response.Body)
	}
	change := fmt.Sprintf("/admin/service-reports/change/?id=%d", report.ID)
	if response = client.request("GET", change, "", false); response.Code != 200 || !strings.Contains(response.Body.String(), fmt.Sprintf(`<option value="%d" selected>`, first.ID)) {
		t.Fatal("report initial choice", response.Code, response.Body)
	}
	invalidForm := url.Values{"ticket": {fmt.Sprint(first.ID)}, "summary": {"<script>raw report</script>"}, "csrfmiddlewaretoken": {client.csrf}}
	response = client.request("POST", "/admin/service-reports/add/", invalidForm.Encode(), false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `data-error-code="unique"`) || !strings.Contains(response.Body.String(), html.EscapeString(invalidForm.Get("summary"))) || strings.Contains(response.Body.String(), "<script>") {
		t.Fatal("Admin duplicate lost safe raw input", response.Code, response.Body)
	}
	deleteTicket := fmt.Sprintf("/admin/tickets/delete/?id=%d", first.ID)
	confirmation := url.Values{"confirm": {"yes"}, "csrfmiddlewaretoken": {client.csrf}}.Encode()
	response = client.request("POST", deleteTicket, confirmation, false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "protected related objects") || strings.Contains(response.Body.String(), `name="confirm"`) || backend.relations != 1 {
		t.Fatal("report did not protect ticket through coordinated relation transaction", response.Code, response.Body, backend.relations)
	}
	backend.mode = "protected_unknown"
	response = client.request("POST", deleteTicket, confirmation, false)
	if response.Code != 500 {
		t.Fatal("uncertain rollback reduced to normal PROTECT", response.Code)
	}
	backend.mode = ""
	baseline := readUniqueTicket(t, ctx, runtime, second.ID)
	backend.stageID = second.ID
	backend.moveCategory = readUniqueTicket(t, ctx, runtime, outsideID).CategoryID
	response = client.request("POST", route, reportInput(second.ID, "scope changed during write"), true)
	checkFailure(response.Code, response.Body.Bytes(), "ticket", "invalid_choice")
	changedScopeForm := url.Values{"ticket": {fmt.Sprint(second.ID)}, "summary": {"scope changed during write"}, "csrfmiddlewaretoken": {client.csrf}}
	response = client.request("POST", "/admin/service-reports/add/", changedScopeForm.Encode(), false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `data-error-code="invalid_choice"`) || !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, second.ID)) {
		t.Fatal("Admin trusted a stale displayed choice or scope failure committed", response.Code, response.Body)
	}
	backend.moveCategory = 0
	for _, mode := range []string{"native", "query_error", "cancel", "driver_error", "rollback_unknown"} {
		backend.mode = mode
		targetID := first.ID
		if mode == "driver_error" {
			targetID = second.ID
		}
		response = client.request("POST", route, reportInput(targetID, "must fail"), true)
		if mode == "native" {
			checkFailure(response.Code, response.Body.Bytes(), "__all__", "unique")
		} else if response.Code != 500 {
			t.Fatal("execution failure became validation", mode, response.Code, response.Body)
		}
		if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, second.ID)) {
			t.Fatal("failed write committed earlier parent mutation", mode)
		}
	}
	if backend.checks == 0 || backend.writes == 0 {
		t.Fatal("failure probes missed actual advisory/native boundary")
	}
	backend.stageID = 0
	backend.mode = ""
	response = client.request("POST", route, reportInput(second.ID, "Second report"), true)
	var secondReport reportConsumerValue
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &secondReport) != nil {
		t.Fatal("second report create", response.Code, response.Body)
	}
	secondPath := fmt.Sprintf("/api/service-reports/%d/", secondReport.ID)
	for _, mode := range []string{"", "native"} {
		backend.mode = mode
		response = client.request("PATCH", secondPath, reportInput(first.ID, "must not persist"), true)
		field := "ticket"
		if mode == "native" {
			field = "__all__"
		}
		checkFailure(response.Code, response.Body.Bytes(), field, "unique")
		stored, found, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.Exact(secondReport.ID)).OrderBy(models.ServiceReportFields.ID.Asc()).First(ctx)
		if err != nil || !found || stored.TicketID != second.ID || stored.Summary != "Second report" {
			t.Fatal("duplicate reassignment persisted other fields", stored, err)
		}
	}
	backend.mode = ""
	if response = client.request("DELETE", secondPath, "", true); response.Code != 204 {
		t.Fatal("delete secondary report", response.Code)
	}
	if response = client.request("PUT", path, reportInput(first.ID, "Full report replacement"), true); response.Code != 200 {
		t.Fatal("valid full report update", response.Code, response.Body)
	}
	backend.mode = "notfound_unknown"
	if response = client.request("DELETE", "/api/service-reports/9223372036854775807/", "", true); response.Code != 500 {
		t.Fatal("rollback failure hidden behind missing object", response.Code)
	}
	backend.mode = "choice_error"
	if response = client.request("GET", change, "", false); response.Code != 500 {
		t.Fatal("choice execution failure became empty choices", response.Code)
	}
	backend.mode = ""
	// The authentication/CSRF boundary precedes every report data transaction.
	for _, entry := range []struct {
		method, path string
		permission   auth.Permission
	}{{"GET", route, helpdesk.ViewServiceReport}, {"POST", route, helpdesk.AddServiceReport}, {"PATCH", path, helpdesk.ChangeServiceReport}, {"DELETE", path, helpdesk.DeleteServiceReport}, {"GET", reverse, helpdesk.ViewServiceReport}} {
		denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: entry.permission})
		denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
		if response := denied.request("GET", "/admin/", "", false); response.Code != 200 {
			t.Fatal("initialize denied runtime CSRF", response.Code)
		}
		beforeQueries, beforeTx := backend.queries, backend.transactions
		response = denied.request(entry.method, entry.path, reportInput(first.ID, "denied"), entry.method != "GET")
		if response.Code != 403 || backend.queries != beforeQueries || backend.transactions != beforeTx {
			t.Fatal("report permission performed IO", entry, response.Code)
		}
	}
	csrf := client.csrf
	client.csrf = "invalid"
	beforeTx := backend.transactions
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		target := path
		if method == "POST" {
			target = route
		}
		response = client.request(method, target, reportInput(second.ID, "csrf denied"), true)
		if response.Code != 403 || backend.transactions != beforeTx {
			t.Fatal("CSRF allowed report transaction", method, response.Code)
		}
	}
	client.csrf = csrf
	form := url.Values{"ticket": {fmt.Sprint(second.ID)}, "summary": {"Admin reassigned report"}, "completed": {"on"}, "csrfmiddlewaretoken": {client.csrf}}
	response = client.request("POST", change, form.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin reassign", response.Code, response.Body)
	}
	if response = client.request("GET", reverse, "", false); response.Code != 200 || strings.TrimSpace(response.Body.String()) != "null" {
		t.Fatal("reassigned relation retained old child", response.Code, response.Body)
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	persisted, found, err := models.ServiceReportObjects.Using(reopened).Filter(models.ServiceReportFields.ID.Exact(report.ID)).OrderBy(models.ServiceReportFields.ID.Asc()).First(ctx)
	closeErr := reopened.Close()
	if err != nil || closeErr != nil || !found || persisted.TicketID != second.ID || persisted.Summary != "Admin reassigned report" || !persisted.Completed {
		t.Fatal("report edit/reassignment not durable", err, closeErr, persisted)
	}
	response = client.request("DELETE", path, "", true)
	if response.Code != 204 {
		t.Fatal("report delete", response.Code, response.Body)
	}
	assertHelpdeskResponseDocumented(t, client.document, "DELETE", "/api/service-reports/{id}/", response)
	if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, second.ID)) {
		t.Fatal("report deletion mutated its parent")
	}
	response = client.request("POST", deleteTicket, confirmation, false)
	if response.Code != 302 {
		t.Fatal("ticket remained protected after report reassignment/deletion", response.Code, response.Body)
	}
	// The Admin has its own complete creation and deletion flow.
	form.Set("ticket", fmt.Sprint(second.ID))
	form.Set("summary", "Admin created report")
	response = client.request("POST", "/admin/service-reports/add/", form.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin create", response.Code, response.Body)
	}
	reportRows, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.Summary.Exact("Admin created report")).OrderBy(models.ServiceReportFields.ID.Asc()).All(ctx)
	if err != nil || len(reportRows) != 1 {
		t.Fatal("Admin creation missing", err)
	}
	response = client.request("POST", fmt.Sprintf("/admin/service-reports/delete/?id=%d", reportRows[0].ID), confirmation, false)
	if response.Code != 302 {
		t.Fatal("Admin report delete", response.Code, response.Body)
	}
	if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, second.ID)) {
		t.Fatal("Admin report deletion changed parent")
	}
}
