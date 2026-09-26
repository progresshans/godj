package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"net/http/httptest"
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
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/systemstate"
)

func assertTicketLabelContracts(t *testing.T, document helpdeskDocument) {
	t.Helper()
	output := document.Components.Schemas["TicketLabel"]
	if len(output.Properties) != 3 || !slices.Equal(output.Required, []string{"id", "ticket", "label"}) || !output.Properties["ticket"].ReadOnly || !output.Properties["label"].ReadOnly {
		t.Fatal("TicketLabel output lost keys or exposed mutable ownership")
	}
	for _, name := range []string{"TicketLabelCreate", "TicketLabelUpdate", "TicketLabelPatch"} {
		input := document.Components.Schemas[name]
		if len(input.Properties) != 2 || input.AdditionalProperties || input.Properties["ticket"].Type != "integer" || input.Properties["label"].Type != "integer" {
			t.Fatal("link input contract", name)
		}
		expected := []string{"ticket", "label"}
		if name == "TicketLabelPatch" {
			expected = nil
		}
		if !slices.Equal(input.Required, expected) {
			t.Fatal("link presence contract", name)
		}
	}
	for _, entry := range []struct{ path, method, schema string }{{"/api/ticket-labels/", "post", "TicketLabelCreate"}, {"/api/ticket-labels/{id}/", "put", "TicketLabelUpdate"}, {"/api/ticket-labels/{id}/", "patch", "TicketLabelPatch"}} {
		operation := document.Paths[entry.path][entry.method]
		if operation.RequestBody == nil || operation.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+entry.schema || !slices.Equal(operation.AdditionalPermissions, []string{string(helpdesk.ViewTicket), string(helpdesk.ViewLabel)}) {
			t.Fatal("link mutation permission or schema contract", entry)
		}
	}
	page := document.Components.Schemas["TicketLabelList"]
	parameters := document.Paths["/api/ticket-labels/"]["get"].Parameters
	if !slices.Equal(page.Required, []string{"items", "count", "limit", "offset"}) || page.Properties["items"].Items == nil || page.Properties["items"].Items.Ref != "#/components/schemas/TicketLabel" || len(parameters) != 2 || parameters[0].Name != "limit" || string(parameters[0].Schema.Maximum) != "100" || parameters[1].Name != "offset" || string(parameters[1].Schema.Maximum) != "2147483647" {
		t.Fatal("link list pagination contract")
	}
	if _, found := document.Paths["/api/tickets/{id}/"]["delete"].Responses["400"]; !found {
		t.Fatal("confirmed protection undocumented")
	}
}

func verifyHistoricalTicketLabelLifecycle(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, ticketID, categoryID int64) {
	t.Helper()
	executor := migrations.Executor{Backend: backend}
	target := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0018_label"}))
	if _, err := executor.Migrate(ctx, loaded, target); err != nil {
		t.Fatal(err)
	}
	ticket := readUniqueTicket(t, ctx, backend, ticketID)
	label, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("Historical linked label", categoryID))
	if err != nil {
		t.Fatal(err)
	}
	state, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatal(err)
	}
	model, found := state.Model("helpdesk", "ticket_label")
	if !found || len(model.UniqueConstraints) != 1 || !slices.Equal(model.UniqueConstraints[0].Fields, []string{"ticket", "label"}) {
		t.Fatal("migration omitted ordered pair uniqueness")
	}
	for _, field := range model.Fields {
		if field.Name == "ticket" || field.Name == "label" {
			if field.Relation == nil || field.Relation.OnDelete != ir.DeleteCascade {
				t.Fatal("migration omitted cascade", field.Name)
			}
		}
	}
	link, err := models.TicketLabelObjects.Create(ctx, backend, models.NewTicketLabelCreate(ticketID, label.ID))
	if err != nil {
		t.Fatal(err)
	}
	verifyTicketLabelsDeclaration(t, ctx, backend, open, loaded, ticket, label, link)
	if _, err := models.TicketLabelObjects.Create(ctx, backend, models.NewTicketLabelCreate(ticketID, label.ID)); !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
		t.Fatal("native pair uniqueness absent", err)
	}
	for _, input := range []models.TicketLabelCreate{models.NewTicketLabelCreate(9223372036854775807, label.ID), models.NewTicketLabelCreate(ticketID, 9223372036854775807)} {
		if _, err := models.TicketLabelObjects.Create(ctx, backend, input); err == nil {
			t.Fatal("native FK missing")
		}
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, readErr := models.TicketLabelObjects.Using(reopened).Filter(models.TicketLabelFields.ID.Exact(link.ID)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
	closeErr := reopened.Close()
	if readErr != nil || closeErr != nil || !found || stored != link {
		t.Fatal("link durability", readErr, closeErr)
	}
	for _, request := range []migrations.LifecycleRequest{target, migrations.LatestLifecycleRequest()} {
		if _, err := executor.Migrate(ctx, loaded, request); err != nil {
			t.Fatal(err)
		}
		actual, found, err := models.LabelObjects.Using(backend).Filter(models.LabelFields.ID.Exact(label.ID)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
		if err != nil || !found || actual != label || !reflect.DeepEqual(ticket, readUniqueTicket(t, ctx, backend, ticketID)) {
			t.Fatal("link lifecycle changed endpoints", err)
		}
	}
	if count, err := models.TicketLabelObjects.Using(backend).Count(ctx); err != nil || count != 0 {
		t.Fatal("reapply restored removed links", count, err)
	}
	if _, err := models.LabelObjects.Delete(ctx, backend, &label); err != nil {
		t.Fatal(err)
	}
}

// The columnless declaration adopts the existing intermediary. Both directions
// of its own historical migration must retain link identity and key allocation.
func verifyTicketLabelsDeclaration(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, ticket models.Ticket, label models.Label, link models.TicketLabel) {
	t.Helper()
	executor := migrations.Executor{Backend: backend}
	before := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0019_ticket_label"}))
	second, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("Declaration sequence check", label.CategoryID))
	if err != nil {
		t.Fatal(err)
	}
	retired, err := models.TicketLabelObjects.Create(ctx, backend, models.NewTicketLabelCreate(ticket.ID, second.ID))
	if err != nil {
		t.Fatal(err)
	}
	highWater := retired.ID
	if _, err := models.TicketLabelObjects.Delete(ctx, backend, &retired); err != nil {
		t.Fatal(err)
	}
	for index, request := range []migrations.LifecycleRequest{before, migrations.LatestLifecycleRequest(), before, migrations.LatestLifecycleRequest()} {
		state, err := executor.Migrate(ctx, loaded, request)
		if err != nil {
			t.Fatal(err)
		}
		metadata, found := state.Model("helpdesk", "ticket")
		if !found {
			t.Fatal("declaration migration removed owner")
		}
		if len(metadata.ManyToMany) != index%2 || index%2 == 1 && !reflect.DeepEqual(metadata.ManyToMany, (models.TicketDescriptor{}).Metadata().ManyToMany) {
			t.Fatal("historical collection differs from current declaration")
		}
		stored, found, err := models.TicketLabelObjects.Using(backend).Filter(models.TicketLabelFields.ID.Exact(link.ID)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
		if err != nil || !found || stored != link || !reflect.DeepEqual(ticket, readUniqueTicket(t, ctx, backend, ticket.ID)) {
			t.Fatal("declaration migration rewrote existing rows", err)
		}
	}
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		_ = reopened.Close()
		t.Fatal(err)
	}
	forward, err := collections.ModelsTicketLabels.From(reopened, ticket)
	if err != nil {
		_ = reopened.Close()
		t.Fatal(err)
	}
	labels, readErr := forward.All(ctx)
	reverse, err := collections.ModelsLabelTickets.From(reopened, label)
	if err != nil {
		_ = reopened.Close()
		t.Fatal(err)
	}
	tickets, reverseErr := reverse.All(ctx)
	closeErr := reopened.Close()
	if readErr != nil || reverseErr != nil || closeErr != nil || len(labels) != 1 || labels[0] != label || len(tickets) != 1 || !reflect.DeepEqual(tickets[0], ticket) {
		t.Fatal("declared collection did not adopt durable intermediary", readErr, reverseErr, closeErr)
	}
	next, err := models.TicketLabelObjects.Create(ctx, backend, models.NewTicketLabelCreate(ticket.ID, second.ID))
	if err != nil || next.ID <= highWater {
		t.Fatal("declaration reset intermediary identity allocation", err, next.ID, highWater)
	}
	if _, err := models.TicketLabelObjects.Delete(ctx, backend, &next); err != nil {
		t.Fatal(err)
	}
	if _, err := models.LabelObjects.Delete(ctx, backend, &second); err != nil {
		t.Fatal(err)
	}
}

type ticketLabelValue struct{ ID, Ticket, Label int64 }

func ticketLabelInput(ticket, label int64) string {
	return fmt.Sprintf(`{"ticket":%d,"label":%d}`, ticket, label)
}
func ticketLabelPath(id int64) string { return fmt.Sprintf("/api/ticket-labels/%d/", id) }
func assertTicketLabelError(t *testing.T, response *httptest.ResponseRecorder, fields []string, code string) {
	t.Helper()
	var body struct {
		Code   string
		Errors []struct {
			Field, Code string
			Params      []any
		}
	}
	if response.Code != 400 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Code != "validation_error" || len(body.Errors) != len(fields) {
		t.Fatal("link rejection", response.Code, response.Body)
	}
	for i, failure := range body.Errors {
		if failure.Field != fields[i] || failure.Code != code || len(failure.Params) != 0 {
			t.Fatal("link diagnosis leaks identity or partial outcome", response.Body)
		}
	}
}

type ticketLabelFaultBackend struct {
	helpdesk.Backend
	mode                                             string
	stageTicket, moveTicket, moveLabel, moveCategory int64
	queries, transactions, writes, relations         int
}

func (b *ticketLabelFaultBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b.queries++
	return b.Backend.Query(ctx, plan)
}
func (b *ticketLabelFaultBackend) Atomic(ctx context.Context, fn func(db.Session) error) error {
	b.transactions++
	err := b.Backend.Atomic(ctx, func(session db.Session) error {
		if b.stageTicket != 0 || b.moveTicket != 0 {
			id := b.stageTicket
			if id == 0 {
				id = b.moveTicket
			}
			row, found, err := models.TicketObjects.Using(session).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("staged ticket absent")
			}
			patch := models.TicketPatch{}.WithSubject("must roll back link stage")
			if b.moveTicket != 0 {
				patch = patch.WithCategoryID(b.moveCategory)
			}
			if _, err := models.TicketObjects.Update(ctx, session, row, patch); err != nil {
				return err
			}
		}
		if b.moveLabel != 0 {
			row, found, err := models.LabelObjects.Using(session).Filter(models.LabelFields.ID.Exact(b.moveLabel)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("staged label absent")
			}
			if _, err := models.LabelObjects.Update(ctx, session, row, models.LabelPatch{}.WithCategoryID(b.moveCategory)); err != nil {
				return err
			}
		}
		return fn(&ticketLabelFaultSession{Session: session, owner: b})
	})
	return b.outcome(err)
}
func (b *ticketLabelFaultBackend) outcome(err error) error {
	if err != nil && (b.mode == "rollback_unknown" || b.mode == "notfound_unknown" || b.mode == "protected_unknown") {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown, Category: query.CategoryBackend})
	}
	if err == nil && b.mode == "commit_unknown" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Category: query.CategoryBackend}
	}
	return err
}

type ticketLabelFaultSession struct {
	db.Session
	owner   *ticketLabelFaultBackend
	written bool
}

func (s *ticketLabelFaultSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	s.owner.queries++
	if plan.Table() == "helpdesk_label" && s.owner.mode == "label_lookup_error" {
		return nil, errors.New("second endpoint lookup failed")
	}
	if plan.Table() == "helpdesk_ticket_label" {
		if s.written && s.owner.mode == "reload_error" {
			return nil, errors.New("link reload failed")
		}
		if plan.ResultShape().Kind() == query.ResultProjection {
			switch s.owner.mode {
			case "query_error":
				return nil, errors.New("pair uniqueness lookup failed")
			case "cancel":
				return nil, context.Canceled
			case "native", "rollback_unknown":
				return uniqueEmptyRows{}, nil
			}
		}
	}
	return s.Session.Query(ctx, plan)
}
func (s *ticketLabelFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "driver_error" {
		return 0, errors.New("link insert failed")
	}
	id, err := s.Session.Insert(ctx, plan)
	s.written = err == nil
	return id, err
}
func (s *ticketLabelFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "driver_error" {
		return 0, errors.New("link update failed")
	}
	count, err := s.Session.Update(ctx, plan)
	s.written = err == nil
	return count, err
}
func (s *ticketLabelFaultSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	s.owner.writes++
	return s.Session.Delete(ctx, plan)
}
func (b *ticketLabelFaultBackend) AtomicRelation(ctx context.Context, fn func(db.RelationSession) error) error {
	b.relations++
	return b.outcome(b.Backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		return fn(&ticketLabelFaultRelation{RelationSession: session, owner: b})
	}))
}

type ticketLabelFaultRelation struct {
	db.RelationSession
	owner   *ticketLabelFaultBackend
	removed int
}

func (s *ticketLabelFaultRelation) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	s.owner.queries++
	return s.RelationSession.Query(ctx, plan)
}
func (s *ticketLabelFaultRelation) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	s.owner.writes++
	if s.removed > 0 && s.owner.mode == "late_delete" {
		return 0, errors.New("cascade failed after link delete")
	}
	count, err := s.RelationSession.Delete(ctx, plan)
	if err == nil {
		s.removed++
	}
	return count, err
}

func verifyHelpdeskTicketLabels(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, otherCategoryID int64) {
	t.Helper()
	newTicket := func(subject string, category int64) models.Ticket {
		t.Helper()
		value, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate(subject, category))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	newLabel := func(name string, category int64) models.Label {
		t.Helper()
		value, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(name, category))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := newTicket("<script>link ticket</script>", categoryID)
	second := newTicket("Second link ticket", categoryID)
	third := newTicket("Third link ticket", categoryID)
	foreignTicket := newTicket("private outside ticket", otherCategoryID)
	label := newLabel("<script>link label</script>", categoryID)
	other := newLabel("Second link label", categoryID)
	foreignLabel := newLabel("private outside label", otherCategoryID)
	backend := &ticketLabelFaultBackend{Backend: runtime}
	application, err := helpdesk.New(backend, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	decode := func(response *httptest.ResponseRecorder) ticketLabelValue {
		t.Helper()
		var value ticketLabelValue
		if response.Code != 200 && response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.ID <= 0 {
			t.Fatal("link response", response.Code, response.Body)
		}
		return value
	}
	read := func(id int64) (models.TicketLabel, bool) {
		t.Helper()
		value, found, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(id)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return value, found
	}
	count := func() int64 {
		t.Helper()
		value, err := models.TicketLabelObjects.Using(runtime).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	create := func(ticketID, labelID int64) ticketLabelValue {
		t.Helper()
		response := client.request("POST", "/api/ticket-labels/", ticketLabelInput(ticketID, labelID), true)
		assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/ticket-labels/", response)
		return decode(response)
	}
	assertStored := func(value ticketLabelValue) {
		t.Helper()
		stored, found := read(value.ID)
		if !found || stored.TicketID != value.Ticket || stored.LabelID != value.Label {
			t.Fatal("stored link changed", value, stored)
		}
	}
	responseList := client.request("GET", "/admin/ticket-labels/", "", false)
	if responseList.Code != 200 || strings.Contains(responseList.Body.String(), `name="q"`) {
		t.Fatal("link Admin advertises unsupported search", responseList.Code)
	}
	queries := backend.queries
	if response := client.request("GET", "/admin/ticket-labels/?q=ignored", "", false); response.Code != 400 || backend.queries != queries {
		t.Fatal("link Admin silently ignored search", response.Code)
	}

	one := create(first.ID, label.ID)
	two := create(second.ID, label.ID)
	response := client.request("GET", "/admin/ticket-labels/add/", "", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), html.EscapeString(first.Subject)) || !strings.Contains(response.Body.String(), html.EscapeString(label.Name)) || strings.Contains(response.Body.String(), "private outside") || strings.Contains(response.Body.String(), `name="category"`) || strings.Contains(response.Body.String(), "<script>") {
		t.Fatal("both scoped escaped choices", response.Code, response.Body)
	}
	for _, permission := range []auth.Permission{helpdesk.ViewTicket, helpdesk.ViewLabel, helpdesk.AddTicketLabel, helpdesk.ChangeTicketLabel} {
		denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: permission})
		denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
		if response := denied.request("GET", "/admin/", "", false); response.Code != 200 {
			t.Fatal("denied setup", response.Code)
		}
		cases := []struct{ method, path string }{}
		if permission != helpdesk.ChangeTicketLabel {
			cases = append(cases, struct{ method, path string }{"POST", "/api/ticket-labels/"}, struct{ method, path string }{"GET", "/admin/ticket-labels/add/"})
		}
		if permission != helpdesk.AddTicketLabel {
			cases = append(cases, struct{ method, path string }{"PATCH", ticketLabelPath(two.ID)}, struct{ method, path string }{"PUT", ticketLabelPath(two.ID)}, struct{ method, path string }{"GET", fmt.Sprintf("/admin/ticket-labels/change/?id=%d", two.ID)})
		}
		for _, entry := range cases {
			queries, tx := backend.queries, backend.transactions
			response := denied.request(entry.method, entry.path, "invalid", entry.method != "GET")
			if response.Code != 403 || queries != backend.queries || tx != backend.transactions {
				t.Fatal("missing link/endpoint permission reached I/O", permission, entry, response.Code)
			}
		}
	}
	for _, body := range []string{`{}`, ticketLabelInput(second.ID, label.ID)} {
		writes := backend.writes
		value := decode(client.request("PATCH", ticketLabelPath(two.ID), body, true))
		if value != two || backend.writes != writes {
			t.Fatal("no-op/self patch wrote")
		}
	}
	for _, entry := range []struct{ method, path, body string }{{"POST", "/api/ticket-labels/", ticketLabelInput(first.ID, label.ID)}, {"PUT", ticketLabelPath(two.ID), ticketLabelInput(first.ID, label.ID)}, {"PATCH", ticketLabelPath(two.ID), fmt.Sprintf(`{"ticket":%d}`, first.ID)}} {
		before := backend.writes
		response := client.request(entry.method, entry.path, entry.body, true)
		assertTicketLabelError(t, response, []string{"__all__"}, "unique_together")
		if before != backend.writes {
			t.Fatal("duplicate reached write")
		}
		assertStored(two)
	}
	for _, entry := range []struct {
		ticket, label int64
		fields        []string
	}{{0, label.ID, []string{"ticket"}}, {-1, label.ID, []string{"ticket"}}, {foreignTicket.ID, label.ID, []string{"ticket"}}, {first.ID, foreignLabel.ID, []string{"label"}}, {first.ID, 9223372036854775807, []string{"label"}}, {0, 0, []string{"ticket", "label"}}} {
		before := backend.writes
		response := client.request("POST", "/api/ticket-labels/", ticketLabelInput(entry.ticket, entry.label), true)
		assertTicketLabelError(t, response, entry.fields, "invalid_choice")
		if before != backend.writes {
			t.Fatal("invalid endpoint reached write")
		}
	}
	for _, body := range []string{`{}`, `{"ticket":1}`, `{"ticket":null,"label":1}`, `{"ticket":1,"label":null}`, `{"ticket":1,"label":2,"id":4}`, `{"ticket":1,"label":2,"category":1}`, `{"ticket":1,"ticket":2,"label":2}`, `[]`, `{"ticket":1,"label":2} {}`, `{"ticket":1.5,"label":2}`} {
		queries, tx := backend.queries, backend.transactions
		response := client.request("POST", "/api/ticket-labels/", body, true)
		if response.Code != 400 || queries != backend.queries || tx != backend.transactions {
			t.Fatal("malformed link input reached I/O", body, response.Code)
		}
	}
	if response := client.request("POST", "/api/ticket-labels/", strings.Repeat(" ", 4097), true); response.Code != 413 {
		t.Fatal("link body bound", response.Code)
	}
	for _, raw := range []string{"limit=0", "limit=101", "offset=-1", "offset=2147483648", "limit=1&limit=2", "label=1", "ticket=1", "search=", "x=%00", "x=%ff", "x=%zz", strings.Repeat("x", 2049)} {
		queries := backend.queries
		response := client.request("GET", "/api/ticket-labels/?"+raw, "", false)
		if response.Code != 400 || queries != backend.queries {
			t.Fatal("invalid link query reached I/O", raw, response.Code)
		}
	}
	foreign, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(foreignTicket.ID, foreignLabel.ID))
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(first.ID, foreignLabel.ID))
	if err != nil {
		t.Fatal(err)
	}
	mixedOther, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(foreignTicket.ID, label.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{foreign.ID, mixed.ID, mixedOther.ID} {
		for _, method := range []string{"GET", "PUT", "PATCH", "DELETE"} {
			response := client.request(method, ticketLabelPath(id), ticketLabelInput(first.ID, label.ID), method != "GET")
			if response.Code != 404 {
				t.Fatal("link leaked cross-category endpoint", method, id, response.Code)
			}
			if _, found := read(id); !found {
				t.Fatal("inaccessible link mutated")
			}
		}
	}
	var page struct {
		Items                []ticketLabelValue
		Count, Limit, Offset int64
	}
	response = client.request("GET", "/api/ticket-labels/?limit=1&offset=1", "", false)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Count != 2 || page.Limit != 1 || page.Offset != 1 || len(page.Items) != 1 || page.Items[0] != two {
		t.Fatal("link scoped page", response.Code, response.Body)
	}
	response = client.request("GET", "/api/ticket-labels/?limit=100&offset=2147483647", "", false)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Count != 2 || len(page.Items) != 0 {
		t.Fatal("link page boundary")
	}
	// Admin creates through both authorized choices and reports pair duplicates.
	form := url.Values{"ticket": {fmt.Sprint(third.ID)}, "label": {fmt.Sprint(other.ID)}, "csrfmiddlewaretoken": {client.csrf}}
	response = client.request("POST", "/admin/ticket-labels/add/", form.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin link create", response.Code, response.Body)
	}
	response = client.request("POST", "/admin/ticket-labels/add/", form.Encode(), false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "unique_together") {
		t.Fatal("Admin pair conflict", response.Code, response.Body)
	}
	adminLinks, err := models.TicketLabelObjects.Using(runtime).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var adminLink models.TicketLabel
	for _, candidate := range adminLinks {
		if candidate.TicketID == third.ID && candidate.LabelID == other.ID {
			adminLink = candidate
		}
	}
	if adminLink.ID == 0 {
		t.Fatal("Admin link absent")
	}
	form.Set("label", fmt.Sprint(label.ID))
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", fmt.Sprintf("/admin/ticket-labels/change/?id=%d", adminLink.ID), form.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin link update", response.Code, response.Body)
	}
	storedAdmin, foundAdmin := read(adminLink.ID)
	if !foundAdmin || storedAdmin.LabelID != label.ID || storedAdmin.TicketID != third.ID {
		t.Fatal("Admin update did not persist both keys")
	}
	response = client.request("POST", fmt.Sprintf("/admin/ticket-labels/delete/?id=%d", adminLink.ID), url.Values{"confirm": {"yes"}, "csrfmiddlewaretoken": {client.csrf}}.Encode(), false)
	if response.Code != 302 {
		t.Fatal("Admin unlink", response.Code, response.Body)
	}
	if _, found := read(adminLink.ID); found {
		t.Fatal("Admin unlink retained row")
	}
	for _, entry := range []struct {
		method, path string
		permission   auth.Permission
	}{{"GET", "/api/ticket-labels/?limit=bad", helpdesk.ViewTicketLabel}, {"DELETE", ticketLabelPath(two.ID), helpdesk.DeleteTicketLabel}, {"DELETE", fmt.Sprintf("/api/tickets/%d/", second.ID), helpdesk.DeleteTicket}} {
		denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: entry.permission})
		denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
		if response := denied.request("GET", "/admin/", "", false); response.Code != 200 {
			t.Fatal("denied read/delete setup")
		}
		queries, tx, relations := backend.queries, backend.transactions, backend.relations
		response := denied.request(entry.method, entry.path, "", entry.method != "GET")
		if response.Code != 403 || queries != backend.queries || tx != backend.transactions || relations != backend.relations {
			t.Fatal("link read/delete permission reached I/O", entry, response.Code)
		}
	}

	// Reassignment and omission use the complete pair, never just supplied keys.
	response = client.request("PATCH", ticketLabelPath(two.ID), fmt.Sprintf(`{"label":%d}`, other.ID), true)
	two = decode(response)
	if two.Ticket != second.ID || two.Label != other.ID {
		t.Fatal("omitted ticket lost")
	}
	assertStored(two)
	response = client.request("PUT", ticketLabelPath(two.ID), ticketLabelInput(second.ID, label.ID), true)
	two = decode(response)
	originalFirst := readUniqueTicket(t, ctx, runtime, first.ID)
	for _, mode := range []string{"query_error", "cancel", "driver_error", "reload_error", "native", "rollback_unknown", "label_lookup_error"} {
		backend.mode, backend.stageTicket = mode, first.ID
		before := count()
		body := ticketLabelInput(second.ID, other.ID)
		if mode == "native" || mode == "rollback_unknown" {
			body = ticketLabelInput(first.ID, label.ID)
		}
		response := client.request("POST", "/api/ticket-labels/", body, true)
		if mode == "native" {
			assertTicketLabelError(t, response, []string{"__all__"}, "unique")
		} else if response.Code != 500 {
			t.Fatal("infrastructure failure became success/validation", mode, response.Code, response.Body)
		}
		if count() != before || !reflect.DeepEqual(originalFirst, readUniqueTicket(t, ctx, runtime, first.ID)) {
			t.Fatal("failed link write retained partial effects", mode)
		}
	}
	backend.mode, backend.stageTicket = "", 0
	for _, endpoint := range []string{"ticket", "label"} {
		backend.moveCategory = otherCategoryID
		if endpoint == "ticket" {
			backend.moveTicket = second.ID
		} else {
			backend.moveLabel = label.ID
		}
		response := client.request("POST", "/api/ticket-labels/", ticketLabelInput(second.ID, label.ID), true)
		assertTicketLabelError(t, response, []string{endpoint}, "invalid_choice")
		if response := client.request("PATCH", ticketLabelPath(two.ID), `{}`, true); response.Code != 404 {
			t.Fatal("omitted endpoint trusted pre-transaction scope", endpoint, response.Code)
		}
		backend.moveTicket, backend.moveLabel = 0, 0
		assertStored(two)
	}
	backend.mode = "label_lookup_error"
	if response := client.request("POST", "/api/ticket-labels/", ticketLabelInput(0, label.ID), true); response.Code != 500 {
		t.Fatal("partial first-endpoint validation escaped second lookup failure", response.Code)
	}
	backend.mode = "notfound_unknown"
	if response := client.request("DELETE", "/api/ticket-labels/9223372036854775807/", "", true); response.Code != 500 {
		t.Fatal("unknown rollback published 404", response.Code)
	}
	backend.mode = "commit_unknown"
	before, tx, writes := count(), backend.transactions, backend.writes
	response = client.request("POST", "/api/ticket-labels/", ticketLabelInput(second.ID, other.ID), true)
	if response.Code != 500 || count() != before+1 || backend.transactions != tx+1 || backend.writes != writes+1 {
		t.Fatal("unknown commit retried or published success", response.Code)
	}
	backend.mode = ""
	// Protection is decided before deleting any link, and uncertain protection is
	// never exposed as a confirmed validation response.
	report, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(first.ID, "protected ticket"))
	if err != nil {
		t.Fatal(err)
	}
	ticketPath := fmt.Sprintf("/api/tickets/%d/", first.ID)
	writes = backend.writes
	response = client.request("DELETE", ticketPath, "", true)
	assertTicketLabelError(t, response, []string{"__all__"}, "protected")
	assertStored(one)
	if writes != backend.writes {
		t.Fatal("protection happened after deletion")
	}
	backend.mode = "protected_unknown"
	if response := client.request("DELETE", ticketPath, "", true); response.Code != 500 {
		t.Fatal("unknown protected rollback hidden", response.Code)
	}
	backend.mode = ""
	if _, err := models.ServiceReportObjects.Delete(ctx, runtime, &report); err != nil {
		t.Fatal(err)
	}
	backend.mode = "late_delete"
	if response := client.request("DELETE", ticketPath, "", true); response.Code != 500 {
		t.Fatal("late delete fault did not run", response.Code)
	}
	assertStored(one)
	if !reflect.DeepEqual(originalFirst, readUniqueTicket(t, ctx, runtime, first.ID)) {
		t.Fatal("late cascade failure lost root")
	}
	backend.mode = ""
	if response := client.request("DELETE", ticketPath, "", true); response.Code != 204 {
		t.Fatal("ticket CASCADE", response.Code, response.Body)
	}
	for _, id := range []int64{one.ID, mixed.ID} {
		if _, found := read(id); found {
			t.Fatal("ticket link survived cascade", id)
		}
	}
	assertStored(two)
	if response := client.request("GET", fmt.Sprintf("/api/labels/%d/", label.ID), "", false); response.Code != 200 {
		t.Fatal("ticket cascade deleted other endpoint")
	}
	labelPath := fmt.Sprintf("/api/labels/%d/", label.ID)
	backend.mode = "late_delete"
	if response := client.request("DELETE", labelPath, "", true); response.Code != 500 {
		t.Fatal("late label cascade fault", response.Code)
	}
	assertStored(two)
	backend.mode = ""
	if response := client.request("DELETE", labelPath, "", true); response.Code != 204 {
		t.Fatal("label CASCADE", response.Code, response.Body)
	}
	for _, id := range []int64{two.ID, mixedOther.ID} {
		if _, found := read(id); found {
			t.Fatal("label link survived cascade", id)
		}
	}
	if response := client.request("GET", fmt.Sprintf("/api/tickets/%d/", second.ID), "", false); response.Code != 200 {
		t.Fatal("label cascade deleted ticket")
	}
	if _, found := read(foreign.ID); !found {
		t.Fatal("unrelated link deleted")
	}
	// Unlinking retains both endpoints, and durable reads come from a reopened DB.
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := models.TicketLabelObjects.Using(runtime).Filter(relations.ModelsTicketLabel.Ticket.ID.Exact(second.ID)).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal("unrelated second link lost", err)
	}
	retained := rows[0]
	reopened, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, readErr := models.TicketLabelObjects.Using(reopened).Filter(models.TicketLabelFields.ID.Exact(retained.ID)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
	closeErr := reopened.Close()
	if readErr != nil || closeErr != nil || !found || stored != retained {
		t.Fatal("post-cascade link durability", readErr, closeErr)
	}
	csrf := client.csrf
	client.csrf = "invalid"
	for _, entry := range []struct{ method, path string }{{"POST", "/api/ticket-labels/"}, {"PUT", ticketLabelPath(retained.ID)}, {"PATCH", ticketLabelPath(retained.ID)}, {"DELETE", ticketLabelPath(retained.ID)}, {"DELETE", fmt.Sprintf("/api/tickets/%d/", second.ID)}} {
		tx, relations := backend.transactions, backend.relations
		response := client.request(entry.method, entry.path, `{}`, true)
		if response.Code != 403 || tx != backend.transactions || relations != backend.relations {
			t.Fatal("CSRF reached link/root transaction", entry, response.Code)
		}
	}
	client.csrf = csrf
	if response := client.request("DELETE", ticketLabelPath(retained.ID), "", true); response.Code != 204 {
		t.Fatal("unlink failed", response.Code)
	}
	for _, path := range []string{fmt.Sprintf("/api/tickets/%d/", second.ID), fmt.Sprintf("/api/labels/%d/", other.ID)} {
		if response := client.request("GET", path, "", false); response.Code != 200 {
			t.Fatal("unlink lost endpoint", path)
		}
	}
}
