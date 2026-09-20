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
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func helpdeskJSON(t *testing.T, text string) jsonvalue.Value {
	t.Helper()
	value, err := jsonvalue.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func verifyHistoricalJSONGrowth(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, id int64) {
	t.Helper()
	read := func(backend helpdeskBackend) models.Ticket {
		value, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("historical JSON row missing", err)
		}
		return value
	}
	before := read(backend)
	if before.ExternalPayload != nil {
		t.Fatal("nullable JSON migration invented a value")
	}
	sample := helpdeskJSON(t, `{"": [340282366920938463463374607431768211455,null,false]}`)
	if _, err := models.TicketObjects.Update(ctx, backend, before, models.TicketPatch{}.WithExternalPayload(sample)); err != nil {
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
	if stored.ExternalPayload == nil || *stored.ExternalPayload != sample {
		t.Fatal("historical JSON lost exact content on reopen")
	}
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0014_ticket_external_reference"})))
	if err != nil {
		t.Fatal(err)
	}
	model, found := state.Model("helpdesk", "ticket")
	if !found {
		t.Fatal("JSON reverse removed model")
	}
	for _, field := range model.Fields {
		if field.Name == "external_payload" {
			t.Fatal("JSON reverse retained field state")
		}
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	if after := read(backend); !reflect.DeepEqual(before, after) {
		t.Fatal("JSON reverse/reapply changed an existing row")
	}
}

func verifyHelpdeskJSON(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id, outsideID int64) {
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
			t.Fatal("JSON ticket missing", err)
		}
		return row
	}
	baseline := read()
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<textarea name=\"external_payload\">\nnull</textarea>") || baseline.ExternalPayload != nil {
		t.Fatal("Admin JSON null textarea missing")
	}
	createdResponse := client.request("POST", "/api/tickets/", `{"subject":"JSON create probe","external_payload":{"":340282366920938463463374607431768211455}}`, true)
	assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", createdResponse)
	var created struct {
		ID, Category int64
		Payload      json.RawMessage `json:"external_payload"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil || createdResponse.Code != http.StatusCreated || created.ID <= 0 || created.Category != categoryID || string(created.Payload) != `{"":340282366920938463463374607431768211455}` {
		t.Fatalf("JSON create: %d %s", createdResponse.Code, createdResponse.Body)
	}
	createdRow, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || createdRow.ExternalPayload == nil || createdRow.ExternalPayload.Text != string(created.Payload) {
		t.Fatal("JSON create did not persist exact content", err)
	}
	if _, err := models.TicketObjects.Delete(ctx, runtime, &createdRow); err != nil {
		t.Fatal(err)
	}
	// Each accepted payload fits a ticket response; the complete page must
	// also fit the response's aggregate node budget.
	largePayload := "[" + strings.Repeat("0,", 1023) + "0]"
	largeRows := make([]models.Ticket, 0, 4)
	for index := 0; index < 4; index++ {
		response := client.request("POST", "/api/tickets/", `{"subject":"JSON page probe","external_payload":`+largePayload+`}`, true)
		var row struct{ ID int64 }
		if json.Unmarshal(response.Body.Bytes(), &row) != nil || response.Code != http.StatusCreated || row.ID <= 0 {
			t.Fatal("accepted page payload did not create", response.Code)
		}
		stored, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(row.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("page probe row missing", err)
		}
		largeRows = append(largeRows, stored)
	}
	page := client.request("GET", "/api/tickets/", "", false)
	for index := range largeRows {
		removed := largeRows[index]
		if _, err := models.TicketObjects.Delete(ctx, runtime, &removed); err != nil {
			t.Fatal(err)
		}
	}
	if page.Code != http.StatusOK {
		t.Fatal("individually accepted JSON rows cannot render as a page", page.Code)
	}
	var pageRows []struct {
		ID      int64
		Payload json.RawMessage `json:"external_payload"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &pageRows); err != nil {
		t.Fatal(err)
	}
	seen := make(map[int64]bool)
	for _, row := range pageRows {
		for _, created := range largeRows {
			if row.ID == created.ID {
				if string(row.Payload) != largePayload || seen[row.ID] {
					t.Fatal("page JSON changed or repeated")
				}
				seen[row.ID] = true
			}
		}
	}
	if len(seen) != len(largeRows) {
		t.Fatal("JSON page dropped accepted rows")
	}
	form := url.Values{"subject": {baseline.Subject}, "csrfmiddlewaretoken": {client.csrf}}
	for _, raw := range []string{`{"broken":`, `{"a":1,"a":2}`, `NaN`, `"\ud800"`, `"\u0000"`, `{"nested":"\u0000"}`, `{"\u0000":1}`, strings.Repeat("[", 14) + "0" + strings.Repeat("]", 14), `</textarea><script>bad</script>`} {
		form.Set("external_payload", raw)
		before := backend.transactions
		response := client.request("POST", changePath, form.Encode(), false)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="external_payload"`) || !strings.Contains(response.Body.String(), html.EscapeString(raw)) || strings.Contains(response.Body.String(), "<script>") || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid JSON Admin reached persistence or lost raw text: %q, %d", raw, response.Code)
		}
	}
	form.Set("external_payload", `{"b":2,"a":1.0}`)
	if response := client.request("POST", changePath, form.Encode(), false); response.Code != http.StatusFound {
		t.Fatal("valid JSON Admin did not save", response.Code, response.Body)
	}
	baseline = read()
	form.Set("external_payload", ` { "a": 1e0, "b": 2 } `)
	before := backend.updates
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || backend.updates != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("equivalent Form JSON caused an update")
	}
	form.Set("subject", "JSON sibling edit")
	response = client.request("POST", changePath, form.Encode(), false)
	if row := read(); response.Code != http.StatusFound || backend.updates != before+1 || row.Subject != "JSON sibling edit" || *row.ExternalPayload != *baseline.ExternalPayload {
		t.Fatal("unrelated Form change rewrote JSON numeric spelling")
	}
	// SQL NULL and stored JSON null have the same form display, but the
	// existing model tag must survive an unchanged form and sibling update.
	if _, err := models.TicketObjects.Update(ctx, runtime, read(), models.TicketPatch{}.WithExternalPayload(jsonvalue.Null())); err != nil {
		t.Fatal(err)
	}
	form.Set("external_payload", "null")
	before = backend.updates
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || backend.updates != before || read().ExternalPayload == nil {
		t.Fatal("unchanged Form collapsed stored JSON null into SQL NULL")
	}
	form.Set("subject", "JSON null sibling")
	response = client.request("POST", changePath, form.Encode(), false)
	if row := read(); response.Code != http.StatusFound || row.Subject != "JSON null sibling" || row.ExternalPayload == nil || *row.ExternalPayload != jsonvalue.Null() {
		t.Fatal("Form sibling edit collapsed stored JSON null")
	}
	write := func(method, body, want string) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil || response.Code != http.StatusOK {
			t.Fatalf("JSON %s: %d %s", method, response.Code, response.Body)
		}
		if string(fields["external_payload"]) != want {
			t.Fatalf("JSON response lost type or exact tokens: %s want %s", fields["external_payload"], want)
		}
		row := read()
		if want == "null" {
			if row.ExternalPayload != nil {
				t.Fatal("explicit API null did not clear SQL value")
			}
		} else if row.ExternalPayload == nil || *row.ExternalPayload != helpdeskJSON(t, want) {
			t.Fatal("JSON response differs from stored value")
		}
	}
	write("PATCH", `{"external_payload":null}`, "null")
	for _, raw := range []string{`false`, `0`, `[]`, `{}`, `""`, `"{\"a\":1}"`, `340282366920938463463374607431768211455`, `{"":{"__proto__":{"constructor":false}},"a":[null,9007199254740993]}`, strings.Repeat("[", 13) + "0" + strings.Repeat("]", 13)} {
		want := helpdeskJSON(t, raw).Text
		write("PATCH", `{"external_payload":`+raw+`}`, want)
		before = backend.updates
		write("PATCH", `{}`, want)
		if backend.updates != before {
			t.Fatal("JSON PATCH omission wrote data")
		}
		write("PUT", `{"subject":"JSON API visit"}`, want)
		if response := client.request("GET", "/api/tickets/", "", false); response.Code != http.StatusOK {
			t.Fatal("accepted JSON cannot render inside list", response.Code)
		}
	}
	sample := `{"":340282366920938463463374607431768211455,"a":"</textarea><script>data</script>"}`
	write("PATCH", `{"external_payload":`+sample+`}`, sample)
	baseline = read()
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), html.EscapeString(helpdeskJSON(t, sample).Text)) {
		t.Fatal("JSON Admin initial content was not escaped")
	}
	for _, body := range []string{`{"external_payload":{"a":1,"a":2}}`, `{"external_payload":"\u0000"}`, `{"external_payload":{"\u0000":1}}`, `{"external_payload":"\ud800"}`, `{"external_payload":NaN}`, `{"external_payload":{},"external_payload":null}`, `{"":1}`, `{"unknown":{"":1}}`, `{"external_payload":` + strings.Repeat("[", 14) + "0" + strings.Repeat("]", 14) + `}`} {
		before := backend.transactions
		response := client.request("PATCH", path, body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid JSON API persisted: %d %s", response.Code, response.Body)
		}
	}
	before = backend.transactions
	response = client.request("PATCH", path, `{"external_payload":"`+strings.Repeat("a", 4096)+`"}`, true)
	if response.Code != http.StatusRequestEntityTooLarge || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("oversized JSON bypassed body budget")
	}
	csrf := client.csrf
	client.csrf = "invalid"
	response = client.request("PATCH", path, `{"external_payload":0}`, true)
	client.csrf = csrf
	if response.Code != http.StatusForbidden || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("JSON write bypassed CSRF")
	}
	denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{helpdesk.ChangeTicket})
	denied.cookies = maps.Clone(client.cookies)
	safe := denied.request("GET", "/api/tickets/", "", false)
	denied.csrf = safe.Header().Get(websessionauth.DefaultCSRFHeader)
	if safe.Code != http.StatusOK || denied.csrf == "" {
		t.Fatal("JSON permission probe could not obtain its CSRF token")
	}
	response = denied.request("PATCH", path, `{"external_payload":0}`, true)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"permission_denied"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("JSON write bypassed change permission")
	}
	before = backend.updates
	response = client.request("PATCH", fmt.Sprintf("/api/tickets/%d/", outsideID), `{"external_payload":0}`, true)
	if response.Code != http.StatusNotFound || backend.updates != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("JSON write escaped category scope")
	}
	backend.rollback = true
	response = client.request("PATCH", path, `{"external_payload":0}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed JSON transaction published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	_, nativeJSON := second.(*postgres.Backend)
	closeErr := second.Close()
	if err != nil || closeErr != nil || !found || !reflect.DeepEqual(stored, baseline) {
		t.Fatal("JSON changed across fresh connection", err, closeErr)
	}
	if nativeJSON {
		for _, raw := range []string{`1e2000`, `1e5000`} {
			response := client.request("PATCH", path, `{"external_payload":`+raw+`}`, true)
			if response.Code != http.StatusInternalServerError || !reflect.DeepEqual(baseline, read()) {
				t.Fatal("native JSON expansion or renderer failure escaped transaction")
			}
			count, err := models.TicketObjects.Using(runtime).Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			response = client.request("POST", "/api/tickets/", `{"subject":"native failure","external_payload":`+raw+`}`, true)
			after, err := models.TicketObjects.Using(runtime).Count(ctx)
			if err != nil || response.Code != http.StatusInternalServerError || count != after {
				t.Fatal("failed native JSON create left a row", err)
			}
		}
	}
	form.Set("subject", baseline.Subject)
	form.Set("external_payload", "")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().ExternalPayload != nil {
		t.Fatal("blank Form did not clear non-null JSON")
	}
}
