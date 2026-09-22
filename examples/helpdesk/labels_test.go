package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

func assertLabelContracts(t *testing.T, document helpdeskDocument) {
	t.Helper()
	label := document.Components.Schemas["Label"]
	if !slices.Equal(label.Required, []string{"id", "name", "category"}) || len(label.Properties) != 3 || label.Properties["name"].MaxLength != 64 || !label.Properties["category"].ReadOnly {
		t.Fatal("Label response lost its typed fields or server-assigned category")
	}
	for _, name := range []string{"LabelCreate", "LabelUpdate", "LabelPatch"} {
		input := document.Components.Schemas[name]
		if len(input.Properties) != 1 || input.AdditionalProperties || !strings.Contains(string(input.Properties["name"].Normalization), `"maxLengthAfterTrim":64`) || (name != "LabelPatch" && !slices.Equal(input.Required, []string{"name"})) || (name == "LabelPatch" && len(input.Required) != 0) {
			t.Fatal("Label input exposed ownership or lost name presence/normalization", name)
		}
	}
	page := document.Components.Schemas["LabelList"]
	if !slices.Equal(page.Required, []string{"items", "count", "limit", "offset"}) || page.Properties["items"].Items == nil || page.Properties["items"].Items.Ref != "#/components/schemas/Label" {
		t.Fatal("Label list lost pagination or model shape")
	}
	parameters := document.Paths["/api/labels/"]["get"].Parameters
	if len(parameters) != 3 || parameters[0].Name != "limit" || string(parameters[0].Schema.Minimum) != "1" || string(parameters[0].Schema.Maximum) != "100" || parameters[1].Name != "offset" || string(parameters[1].Schema.Minimum) != "0" || string(parameters[1].Schema.Maximum) != "2147483647" || parameters[2].Name != "search" {
		t.Fatal("Label pagination bounds do not match its public query schema")
	}
	for _, entry := range []struct{ path, method, schema, status string }{{"/api/labels/", "post", "LabelCreate", "201"}, {"/api/labels/{id}/", "put", "LabelUpdate", "200"}, {"/api/labels/{id}/", "patch", "LabelPatch", "200"}} {
		operation := document.Paths[entry.path][entry.method]
		if operation.RequestBody == nil || !operation.RequestBody.Required || operation.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+entry.schema || operation.Responses[entry.status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/Label" {
			t.Fatal("Label operation does not own its request/response schema", entry)
		}
	}
}

func verifyHistoricalLabelLifecycle(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, ticketID, categoryID int64) {
	t.Helper()
	baseline := readUniqueTicket(t, ctx, backend, ticketID)
	created, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("Historical label", categoryID))
	if err != nil {
		t.Fatal(err)
	}
	// Isolate Label's PROTECT edge from the Category's existing Ticket edge.
	parent, err := models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("Label-only parent"))
	if err != nil {
		t.Fatal(err)
	}
	child, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("Protected child", parent.ID))
	if err != nil {
		t.Fatal(err)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	originalParent := parent
	if count, err := deleters.ModelsCategory.Delete(ctx, backend, &parent); count != 0 || !errors.Is(err, &query.Error{Code: query.CodeProtectedForeignKey}) || parent != originalParent {
		t.Fatal("Label failed to protect its category", count, err)
	}
	if _, err := models.LabelObjects.Delete(ctx, backend, &child); err != nil {
		t.Fatal(err)
	}
	if count, err := deleters.ModelsCategory.Delete(ctx, backend, &parent); err != nil || count != 1 {
		t.Fatal("removed Label still protected category", count, err)
	}
	if _, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate(created.Name, categoryID)); !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
		t.Fatal("actual generated migration omitted tuple uniqueness", err)
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, readErr := models.LabelObjects.Using(reopened).Filter(models.LabelFields.ID.Exact(created.ID)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
	closeErr := reopened.Close()
	if readErr != nil || closeErr != nil || !found || !reflect.DeepEqual(stored, created) {
		t.Fatal("Label was not durable", stored, readErr, closeErr)
	}
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0017_service_report"})))
	if err != nil {
		t.Fatal(err)
	}
	if _, found := state.Model("helpdesk", "label"); found {
		t.Fatal("reverse retained Label model")
	}
	if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, backend, ticketID)) {
		t.Fatal("Label reversal changed existing ticket")
	}
	state, err = executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatal(err)
	}
	model, found := state.Model("helpdesk", "label")
	if !found || len(model.UniqueConstraints) != 1 || !slices.Equal(model.UniqueConstraints[0].Fields, []string{"category", "name"}) {
		t.Fatal("reapplied history lost ordered constraint")
	}
	if count, err := models.LabelObjects.Using(backend).Count(ctx); err != nil || count != 0 {
		t.Fatal("reapplied Label rows", count, err)
	}
	if !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, backend, ticketID)) {
		t.Fatal("Label reapply changed existing ticket")
	}
}

type labelConsumerValue struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Category int64  `json:"category"`
}

func labelInput(name string) string {
	value, _ := json.Marshal(map[string]string{"name": name})
	return string(value)
}

type labelFaultBackend struct {
	helpdesk.Backend
	mode                                  string
	stageID, moveID, moveCategory         int64
	queries, transactions, checks, writes int
}

func (b *labelFaultBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b.queries++
	return b.Backend.Query(ctx, plan)
}
func (b *labelFaultBackend) Atomic(ctx context.Context, fn func(db.Session) error) error {
	b.transactions++
	err := b.Backend.Atomic(ctx, func(session db.Session) error {
		if b.stageID != 0 {
			row, found, err := models.TicketObjects.Using(session).Filter(models.TicketFields.ID.Exact(b.stageID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("staged ticket missing")
			}
			if _, err := models.TicketObjects.Update(ctx, session, row, models.TicketPatch{}.WithSubject("label write must roll back")); err != nil {
				return err
			}
		}
		if b.moveID != 0 {
			row, found, err := models.LabelObjects.Using(session).Filter(models.LabelFields.ID.Exact(b.moveID)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("staged label missing")
			}
			if _, err := models.LabelObjects.Update(ctx, session, row, models.LabelPatch{}.WithCategoryID(b.moveCategory)); err != nil {
				return err
			}
		}
		return fn(&labelFaultSession{Session: session, owner: b})
	})
	if err != nil && (b.mode == "rollback_unknown" || b.mode == "notfound_unknown") {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	if err == nil && b.mode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	return err
}

type labelFaultSession struct {
	db.Session
	owner   *labelFaultBackend
	written bool
}

func (s *labelFaultSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	s.owner.queries++
	if plan.Table() == "helpdesk_label" {
		if s.written && s.owner.mode == "reload_error" {
			return nil, errors.New("label reload failed")
		}
		if plan.ResultShape().Kind() == query.ResultProjection {
			s.owner.checks++
			switch s.owner.mode {
			case "query_error":
				return nil, errors.New("label uniqueness read failed")
			case "cancel":
				return nil, context.Canceled
			case "native", "rollback_unknown":
				return uniqueEmptyRows{}, nil
			}
		}
	}
	return s.Session.Query(ctx, plan)
}
func (s *labelFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "driver_error" {
		return 0, errors.New("label driver failed")
	}
	key, err := s.Session.Insert(ctx, plan)
	s.written = err == nil
	return key, err
}
func (s *labelFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "driver_error" {
		return 0, errors.New("label driver failed")
	}
	count, err := s.Session.Update(ctx, plan)
	s.written = err == nil
	return count, err
}

func verifyHelpdeskLabels(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, otherCategoryID, ticketID int64) {
	t.Helper()
	foreign, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Shared label", otherCategoryID))
	if err != nil {
		t.Fatal(err)
	}
	backend := &labelFaultBackend{Backend: runtime}
	application, err := helpdesk.New(backend, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	if response := client.request("GET", "/admin/labels/add/", "", false); response.Code != 200 || strings.Contains(response.Body.String(), `name="category"`) || !strings.Contains(response.Body.String(), `name="name"`) {
		t.Fatal("Label form exposes category or omits name", response.Code)
	}
	read := func(id int64) models.Label {
		t.Helper()
		value, found, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(id)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("label missing", id, err)
		}
		return value
	}
	count := func() int64 {
		t.Helper()
		n, err := models.LabelObjects.Using(runtime).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	defer func() {
		rows, err := models.LabelObjects.Using(runtime).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		for _, value := range rows {
			if _, err := models.LabelObjects.Delete(ctx, runtime, &value); err != nil {
				t.Error(err)
			}
		}
	}()
	decode := func(responseCode int, data []byte) labelConsumerValue {
		t.Helper()
		var value labelConsumerValue
		if responseCode != 200 && responseCode != 201 || json.Unmarshal(data, &value) != nil || value.ID <= 0 || value.Category != categoryID {
			t.Fatal("Label response scope", responseCode, string(data))
		}
		return value
	}
	conflict := func(code int, data []byte, expected string) {
		t.Helper()
		var response struct {
			Code   string
			Errors []struct {
				Field, Code string
				Params      []any
			}
		}
		if code != 400 || json.Unmarshal(data, &response) != nil || response.Code != "validation_error" || len(response.Errors) != 1 || response.Errors[0].Field != "__all__" || response.Errors[0].Code != expected || len(response.Errors[0].Params) != 0 || strings.Contains(string(data), "Shared label") {
			t.Fatal("Label duplicate leaked values or blamed hidden category", code, string(data))
		}
	}
	response := client.request("POST", "/api/labels/", labelInput("  Shared label  "), true)
	first := decode(response.Code, response.Body.Bytes())
	if first.Name != foreign.Name || read(first.ID).CategoryID != categoryID {
		t.Fatal("different category name was not allowed/trimmed")
	}
	response = client.request("POST", "/api/labels/", labelInput("Second label"), true)
	second := decode(response.Code, response.Body.Bytes())
	path := fmt.Sprintf("/api/labels/%d/", second.ID)
	for _, method := range []string{"POST", "PUT", "PATCH"} {
		target := path
		if method == "POST" {
			target = "/api/labels/"
		}
		writes := backend.writes
		response = client.request(method, target, labelInput(first.Name), true)
		conflict(response.Code, response.Body.Bytes(), "unique_together")
		if backend.writes != writes || read(second.ID).Name != second.Name {
			t.Fatal("confirmed duplicate reached a write")
		}
	}
	for _, body := range []string{`{}`, labelInput(second.Name)} {
		writes := backend.writes
		response = client.request("PATCH", path, body, true)
		if value := decode(response.Code, response.Body.Bytes()); value != second || backend.writes != writes {
			t.Fatal("self/no-op patch changed label")
		}
	}
	for _, body := range []string{`{}`, `{"name":null}`, `{"name":" "}`, `{"category":1,"name":"bad"}`, `{"id":1,"name":"bad"}`, `{"name":"a","name":"b"}`, `{"name":"ok"} {}`, `[]`, labelInput(strings.Repeat("라", 65))} {
		beforeQueries, beforeTx := backend.queries, backend.transactions
		response = client.request("POST", "/api/labels/", body, true)
		if response.Code != 400 || backend.queries != beforeQueries || backend.transactions != beforeTx {
			t.Fatal("invalid Label input reached storage", body, response.Code)
		}
	}
	response = client.request("POST", "/api/labels/", labelInput(strings.Repeat("라", 64)), true)
	long := decode(response.Code, response.Body.Bytes())
	if response = client.request("DELETE", fmt.Sprintf("/api/labels/%d/", long.ID), "", true); response.Code != 204 {
		t.Fatal("Unicode label delete", response.Code)
	}
	response = client.request("GET", "/api/labels/?search=label&limit=1&offset=1", "", false)
	var page struct {
		Items                []labelConsumerValue
		Count, Limit, Offset int64
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Count != 2 || page.Limit != 1 || page.Offset != 1 || len(page.Items) != 1 || page.Items[0] != second {
		t.Fatal("scoped search/pagination", response.Code, response.Body)
	}
	response = client.request("GET", "/api/labels/?limit=100&offset=2147483647", "", false)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Count != 2 || page.Limit != 100 || page.Offset != 2147483647 || len(page.Items) != 0 {
		t.Fatal("published pagination boundary failed", response.Code, response.Body)
	}
	for _, raw := range []string{"limit=0", "limit=101", "limit=-1", "limit=1&limit=2", "offset=-1", "offset=2147483648", "offset=", "limit=1.0", "category=1", "search=%00", "search=%ff", "search=%zz", "search=" + strings.Repeat("a", 65), strings.Repeat("x", 2049)} {
		before := backend.queries
		response = client.request("GET", "/api/labels/?"+raw, "", false)
		if response.Code != 400 || backend.queries != before {
			t.Fatal("invalid list query reached data", raw, response.Code)
		}
	}
	for _, method := range []string{"GET", "PUT", "PATCH", "DELETE"} {
		response = client.request(method, fmt.Sprintf("/api/labels/%d/", foreign.ID), labelInput("hidden"), method != "GET")
		if response.Code != 404 || read(foreign.ID).Name != foreign.Name {
			t.Fatal("cross-category Label access", method, response.Code)
		}
	}
	change := fmt.Sprintf("/admin/labels/change/?id=%d", second.ID)
	form := url.Values{"name": {" <script>shared</script> "}, "csrfmiddlewaretoken": {client.csrf}}
	response = client.request("PUT", fmt.Sprintf("/api/labels/%d/", first.ID), labelInput("<script>shared</script>"), true)
	first = decode(response.Code, response.Body.Bytes())
	for _, target := range []string{"/admin/labels/add/", change} {
		response = client.request("POST", target, form.Encode(), false)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `data-error-field="__all__"`) || !strings.Contains(response.Body.String(), `data-error-code="unique_together"`) || !strings.Contains(response.Body.String(), html.EscapeString(form.Get("name"))) || strings.Contains(response.Body.String(), "<script>") {
			t.Fatal("Admin tuple diagnostic/raw-input escaping", response.Code, response.Body)
		}
	}
	baseline := readUniqueTicket(t, ctx, runtime, ticketID)
	backend.stageID = ticketID
	for _, mode := range []string{"native", "query_error", "cancel", "driver_error", "reload_error", "rollback_unknown"} {
		backend.mode = mode
		name := first.Name
		if mode == "driver_error" || mode == "reload_error" {
			name = "failed " + mode
		}
		before := count()
		response = client.request("POST", "/api/labels/", labelInput(name), true)
		if mode == "native" {
			conflict(response.Code, response.Body.Bytes(), "unique")
		} else if response.Code != 500 {
			t.Fatal("execution failure became a diagnostic", mode, response.Code, response.Body)
		}
		if count() != before || !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, ticketID)) {
			t.Fatal("Label failure committed a prior write", mode)
		}
	}
	backend.stageID, backend.mode = 0, ""
	backend.moveID, backend.moveCategory = second.ID, otherCategoryID
	response = client.request("POST", change, url.Values{"name": {"moved during write"}, "csrfmiddlewaretoken": {client.csrf}}.Encode(), false)
	if response.Code != 404 || read(second.ID).CategoryID != categoryID || read(second.ID).Name != second.Name {
		t.Fatal("Admin trusted scope before its transaction", response.Code)
	}
	backend.moveID = 0
	backend.mode = "notfound_unknown"
	if response = client.request("DELETE", "/api/labels/9223372036854775807/", "", true); response.Code != 500 {
		t.Fatal("uncertain rollback hidden as 404", response.Code)
	}
	backend.mode = "commit_unknown"
	before, transactions, writes := count(), backend.transactions, backend.writes
	response = client.request("POST", "/api/labels/", labelInput("uncertain commit"), true)
	if response.Code != 500 || count() != before+1 || backend.transactions != transactions+1 || backend.writes != writes+1 {
		t.Fatal("unknown commit was retried, called rollback or returned success", response.Code)
	}
	backend.mode = ""
	for _, entry := range []struct {
		method, path string
		permission   auth.Permission
	}{{"GET", "/api/labels/?limit=bad", helpdesk.ViewLabel}, {"POST", "/api/labels/", helpdesk.AddLabel}, {"PUT", path, helpdesk.ChangeLabel}, {"PATCH", path, helpdesk.ChangeLabel}, {"DELETE", path, helpdesk.DeleteLabel}, {"GET", "/admin/labels/", helpdesk.ViewLabel}, {"GET", "/admin/labels/add/", helpdesk.AddLabel}, {"GET", change, helpdesk.ChangeLabel}, {"GET", fmt.Sprintf("/admin/labels/delete/?id=%d", second.ID), helpdesk.DeleteLabel}} {
		denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: entry.permission})
		denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
		if response := denied.request("GET", "/admin/", "", false); response.Code != 200 {
			t.Fatal("initialize denied Label session", response.Code)
		}
		beforeQueries, beforeTx := backend.queries, backend.transactions
		response = denied.request(entry.method, entry.path, "invalid", entry.method != "GET")
		if response.Code != 403 || backend.queries != beforeQueries || backend.transactions != beforeTx {
			t.Fatal("Label permission ran data I/O", entry, response.Code)
		}
	}
	csrf := client.csrf
	client.csrf = "invalid"
	beforeTx := backend.transactions
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		target := path
		if method == "POST" {
			target = "/api/labels/"
		}
		response = client.request(method, target, labelInput("denied"), true)
		if response.Code != 403 || backend.transactions != beforeTx {
			t.Fatal("Label CSRF reached transaction", method, response.Code)
		}
	}
	client.csrf = csrf
	anonymous := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	beforeQueries, beforeTx := backend.queries, backend.transactions
	for _, method := range []string{"GET", "POST"} {
		response = anonymous.request(method, "/api/labels/", "invalid", method == "POST")
		if response.Code != 403 || backend.queries != beforeQueries || backend.transactions != beforeTx {
			t.Fatal("anonymous Label request reached storage", method, response.Code)
		}
	}
	missingApplication, err := helpdesk.New(backend, 9223372036854775807)
	if err != nil {
		t.Fatal(err)
	}
	missing := helpdeskHTTP(t, missingApplication, runtime, auth.PrincipalAuthorizer{})
	missing.cookies, missing.csrf = maps.Clone(client.cookies), client.csrf
	if response = missing.request("GET", "/admin/", "", false); response.Code != 200 {
		t.Fatal("initialize missing-scope session")
	}
	checks, missingWrites := backend.checks, backend.writes
	response = missing.request("POST", "/api/labels/", labelInput("missing category"), true)
	if response.Code != 404 || backend.checks != checks || backend.writes != missingWrites {
		t.Fatal("missing Category reached label uniqueness or insert", response.Code)
	}
	form = url.Values{"name": {"Admin created label"}, "category": {fmt.Sprint(otherCategoryID)}, "csrfmiddlewaretoken": {client.csrf}}
	beforeTx = backend.transactions
	response = client.request("POST", "/admin/labels/add/", form.Encode(), false)
	if response.Code != 400 || backend.transactions != beforeTx {
		t.Fatal("Admin accepted undeclared ownership field", response.Code)
	}
	form.Del("category")
	response = client.request("POST", "/admin/labels/add/", form.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin Label create", response.Code, response.Body)
	}
	rows, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.Name.Exact("Admin created label")).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil || len(rows) != 1 || rows[0].CategoryID != categoryID {
		t.Fatal("Admin accepted client category", rows, err)
	}
	form.Set("name", "Admin renamed label")
	response = client.request("POST", fmt.Sprintf("/admin/labels/change/?id=%d", rows[0].ID), form.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin Label update", response.Code, response.Body)
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	durable, found, readErr := models.LabelObjects.Using(reopened).Filter(models.LabelFields.ID.Exact(rows[0].ID)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
	closeErr := reopened.Close()
	if readErr != nil || closeErr != nil || !found || durable.Name != "Admin renamed label" || durable.CategoryID != categoryID {
		t.Fatal("Label edit not durable", durable, readErr, closeErr)
	}
	response = client.request("POST", fmt.Sprintf("/admin/labels/delete/?id=%d", rows[0].ID), url.Values{"confirm": {"yes"}, "csrfmiddlewaretoken": {client.csrf}}.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin Label delete", response.Code)
	}
	response = client.request("DELETE", path, "", true)
	if response.Code != 204 {
		t.Fatal("API Label delete", response.Code)
	}
	assertHelpdeskResponseDocumented(t, client.document, "DELETE", "/api/labels/{id}/", response)
	if response = client.request("GET", path, "", false); response.Code != 404 {
		t.Fatal("deleted Label still visible")
	}
	if backend.checks == 0 || backend.writes == 0 || !reflect.DeepEqual(baseline, readUniqueTicket(t, ctx, runtime, ticketID)) || read(foreign.ID).Name != foreign.Name {
		t.Fatal("Label operations missed native paths or changed related data")
	}
}
