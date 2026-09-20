package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/uuid"
)

func verifyHistoricalExternalReferenceGrowth(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, id int64) {
	t.Helper()
	read := func(reader helpdeskBackend) models.Ticket {
		value, found, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("historical UUID ticket missing", err)
		}
		return value
	}
	before := read(backend)
	if before.ExternalReference != nil {
		t.Fatal("nullable UUID addition invented a value")
	}
	sample := uuid.UUID{0: 0x80, 15: 1}
	if _, err := models.TicketObjects.Update(ctx, backend, before, models.TicketPatch{}.WithExternalReference(sample)); err != nil {
		t.Fatal(err)
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored := read(second)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if stored.ExternalReference == nil || *stored.ExternalReference != sample {
		t.Fatal("UUID add lost value after reopen")
	}
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0013_alter_ticket_expected_cost"})))
	if err != nil {
		t.Fatal(err)
	}
	model, found := state.Model("helpdesk", "ticket")
	if !found {
		t.Fatal("UUID reverse removed model")
	}
	for _, field := range model.Fields {
		if field.Name == "external_reference" {
			t.Fatal("UUID reverse retained field state")
		}
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	after := read(backend)
	if after.ExternalReference != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("UUID reverse/reapply changed existing ticket fields")
	}
}

func verifyHelpdeskUUID(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id, outsideID int64) {
	t.Helper()
	backend := &helpdeskMutationCounter{Backend: runtime}
	application, err := helpdesk.New(backend, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	path := fmt.Sprintf("/api/tickets/%d/", id)
	changePath := fmt.Sprintf("/admin/tickets/change/?id=%d", id)
	read := func() models.Ticket {
		row, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("UUID row missing", err)
		}
		return row
	}
	baseline := read()
	if baseline.ExternalReference != nil {
		t.Fatal("UUID consumer did not start at NULL")
	}
	sample, err := uuid.Parse("12345678-9abc-4def-8123-456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="external_reference" value=""`) {
		t.Fatal("UUID Admin text input missing")
	}
	createdResponse := client.request("POST", "/api/tickets/", `{"subject":"UUID create probe","external_reference":340282366920938463463374607431768211455}`, true)
	assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", createdResponse)
	var created struct {
		ID, Category int64
		Reference    *string `json:"external_reference"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil || createdResponse.Code != http.StatusCreated || created.ID <= 0 || created.Category != categoryID || created.Reference == nil || *created.Reference != "ffffffff-ffff-ffff-ffff-ffffffffffff" {
		t.Fatalf("UUID create: %d %s", createdResponse.Code, createdResponse.Body)
	}
	createdRow, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || createdRow.ExternalReference == nil || createdRow.ExternalReference.String() != *created.Reference {
		t.Fatal("UUID integer create lost stored bits", err)
	}
	if _, err := models.TicketObjects.Delete(ctx, runtime, &createdRow); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"subject": {baseline.Subject}, "external_reference": {"not-a-uuid"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="external_reference"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("invalid UUID Admin reached persistence")
	}
	markup := `"><script>alert(1)</script>`
	form.Set("external_reference", markup)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), html.EscapeString(markup)) || backend.transactions != before {
		t.Fatal("UUID invalid input lost escaping")
	}
	form.Set("external_reference", "  {"+strings.ToUpper(sample.String())+"}  ")
	response = client.request("POST", changePath, form.Encode(), false)
	if row := read(); response.Code != http.StatusFound || row.ExternalReference == nil || *row.ExternalReference != sample {
		t.Fatal("UUID Admin alias did not persist")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="external_reference" value="`+sample.String()+`"`) {
		t.Fatal("UUID Admin initial is not canonical")
	}
	before = backend.updates
	form.Set("external_reference", sample.Hex())
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || backend.updates != before {
		t.Fatal("equivalent UUID Admin spelling caused an update")
	}
	write := func(method, body string, want *string) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var values map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &values); err != nil || response.Code != http.StatusOK {
			t.Fatalf("UUID %s: %d %s", method, response.Code, response.Body)
		}
		raw, present := values["external_reference"]
		if !present {
			t.Fatal("UUID response omitted nullable field")
		}
		stored := read()
		if want == nil {
			if string(raw) != "null" || stored.ExternalReference != nil {
				t.Fatal("UUID null not stored")
			}
			return
		}
		var text string
		if json.Unmarshal(raw, &text) != nil || text != *want || stored.ExternalReference == nil || stored.ExternalReference.String() != *want {
			t.Fatal("UUID wire and stored value differ", string(raw), *want)
		}
	}
	zero := (uuid.UUID{}).String()
	one := (uuid.UUID{15: 1}).String()
	write("PATCH", `{"external_reference":0}`, &zero)
	before = backend.updates
	write("PATCH", `{}`, &zero)
	write("PATCH", `{"external_reference":-0}`, &zero)
	write("PATCH", `{"external_reference":false}`, &zero)
	if backend.updates != before {
		t.Fatal("UUID zero alias or omission caused update")
	}
	write("PUT", `{"subject":"UUID visit"}`, &zero)
	write("PATCH", `{"external_reference":true}`, &one)
	write("PATCH", `{"external_reference":18446744073709551616}`, new("00000000-0000-0001-0000-000000000000"))
	write("PATCH", `{"external_reference":"urn:uuid:`+strings.ToUpper(sample.String())+`"}`, new(sample.String()))
	write("PATCH", `{"external_reference":null}`, nil)
	write("PUT", `{"subject":"UUID visit","external_reference":"`+sample.String()+`"}`, new(sample.String()))
	baseline = read()
	for _, body := range []string{`{"external_reference":-1}`, `{"external_reference":340282366920938463463374607431768211456}`, `{"external_reference":1.0}`, `{"external_reference":1e0}`, `{"external_reference":-0.0}`, `{"external_reference":[]}`, `{"external_reference":{}}`, `{"external_reference":""}`, `{"external_reference":"` + sample.String() + ` "}`, `{"external_reference":"bad"}`, `{"external_reference":0,"external_reference":null}`} {
		before := backend.transactions
		response := client.request("PATCH", path, body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid UUID persisted: %d %s", response.Code, response.Body)
		}
	}
	csrf := client.csrf
	client.csrf = "invalid"
	before = backend.transactions
	response = client.request("PATCH", path, `{"external_reference":0}`, true)
	client.csrf = csrf
	if response.Code != http.StatusForbidden || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("UUID write bypassed CSRF")
	}
	before = backend.updates
	response = client.request("PATCH", fmt.Sprintf("/api/tickets/%d/", outsideID), `{"external_reference":0}`, true)
	if response.Code != http.StatusNotFound || backend.updates != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("UUID escaped category isolation")
	}
	backend.rollback = true
	before = backend.updates
	response = client.request("PATCH", path, `{"external_reference":0}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed UUID transaction published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	closeErr := second.Close()
	if err != nil || closeErr != nil || !found || stored.ExternalReference == nil || *stored.ExternalReference != sample {
		t.Fatal("UUID changed after fresh connection", err, closeErr)
	}
	form.Set("external_reference", "")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().ExternalReference != nil {
		t.Fatal("blank Admin did not clear UUID")
	}
}
