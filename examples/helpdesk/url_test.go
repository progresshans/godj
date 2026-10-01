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
)

func verifyHistoricalURLGrowth(t *testing.T, ctx context.Context, backend helpdeskBackend, open func(context.Context) (helpdeskBackend, error), loaded migrations.LoadedDefinitionSet, id int64) {
	t.Helper()
	read := func(reader helpdeskBackend) models.Ticket {
		t.Helper()
		value, found, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("historical URL ticket missing", err)
		}
		return value
	}
	before := read(backend)
	if before.ExternalURL != nil {
		t.Fatal("nullable URL addition invented a value")
	}
	const legacy = "  legacy://UNCHANGED  "
	if _, err := models.TicketObjects.Update(ctx, backend, before, models.TicketPatch{}.WithExternalURL(legacy)); err != nil {
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
	if stored.ExternalURL == nil || *stored.ExternalURL != legacy {
		t.Fatal("ordinary URL storage applied input grammar or normalization")
	}
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "helpdesk", Name: "0021_auto_72857bab23b6"})))
	if err != nil {
		t.Fatal(err)
	}
	model, found := state.Model("helpdesk", "ticket")
	if !found {
		t.Fatal("URL reverse removed the ticket")
	}
	for _, field := range model.Fields {
		if field.Name == "external_url" {
			t.Fatal("URL reverse retained field state")
		}
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	if after := read(backend); after.ExternalURL != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("URL reverse/reapply changed other ticket fields")
	}
}

func verifyHelpdeskURL(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id, outsideID int64) {
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
		t.Helper()
		row, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal("URL row missing", err)
		}
		return row
	}
	baseline := read()
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `type="url" name="external_url" value=""`) || !strings.Contains(response.Body.String(), ` novalidate>`) {
		t.Fatal("URL Admin widget is missing")
	}
	createdResponse := client.request("POST", "/api/tickets/", `{"subject":"URL create probe","external_url":"  HTTPS://Example.COM/Path  "}`, true)
	assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", createdResponse)
	var created struct {
		ID, Category int64
		URL          *string `json:"external_url"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil || createdResponse.Code != http.StatusCreated || created.ID <= 0 || created.Category != categoryID || created.URL == nil || *created.URL != "HTTPS://Example.COM/Path" {
		t.Fatalf("URL create: %d %s", createdResponse.Code, createdResponse.Body)
	}
	row, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || row.ExternalURL == nil || *row.ExternalURL != *created.URL {
		t.Fatal("URL create response differs from DB", err)
	}
	if _, err := deleteHelpdeskTicket(ctx, runtime, &row); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"subject": {baseline.Subject}, "external_url": {"javascript://example.com"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="external_url"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("invalid URL Admin input reached storage")
	}
	const markup = `"><script>alert(1)</script>`
	form.Set("external_url", markup)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), html.EscapeString(markup)) || backend.transactions != before {
		t.Fatal("invalid URL input lost escaping")
	}
	form.Set("external_url", "  //Example.com/Path  ")
	response = client.request("POST", changePath, form.Encode(), false)
	if row := read(); response.Code != http.StatusFound || row.ExternalURL == nil || *row.ExternalURL != "https://Example.com/Path" {
		t.Fatal("URL Admin did not infer scheme")
	}
	before = backend.updates
	form.Set("external_url", "Example.com/Path")
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || backend.updates != before {
		t.Fatal("equivalent URL form caused an update")
	}
	write := func(method, body string, want *string) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var value map[string]json.RawMessage
		if json.Unmarshal(response.Body.Bytes(), &value) != nil || response.Code != http.StatusOK {
			t.Fatalf("URL %s: %d %s", method, response.Code, response.Body)
		}
		raw, present := value["external_url"]
		if !present {
			t.Fatal("URL response omitted a nullable field")
		}
		row := read()
		if want == nil {
			if string(raw) != "null" || row.ExternalURL != nil {
				t.Fatal("URL null did not persist")
			}
		} else {
			var text string
			if json.Unmarshal(raw, &text) != nil || text != *want || row.ExternalURL == nil || *row.ExternalURL != *want {
				t.Fatal("URL response differs from stored input")
			}
		}
	}
	write("PATCH", `{"external_url":"  HTTPS://例え.テスト/Path?x=%zz  "}`, new("HTTPS://例え.テスト/Path?x=%zz"))
	before = backend.updates
	write("PATCH", `{}`, new("HTTPS://例え.テスト/Path?x=%zz"))
	if backend.updates != before {
		t.Fatal("URL omission caused an update")
	}
	write("PUT", `{"subject":"URL visit"}`, new("HTTPS://例え.テスト/Path?x=%zz"))
	write("PATCH", `{"external_url":"  "}`, new(""))
	write("PATCH", `{"external_url":null}`, nil)
	write("PATCH", `{"external_url":"ftps://user:pass@Example.com:99999/a"}`, new("ftps://user:pass@Example.com:99999/a"))
	baseline = read()
	for _, value := range []string{`"example.com"`, `"//example.com"`, `"javascript://example.com"`, `"https://a.c"`, `"https://example.com/` + strings.Repeat("a", 201) + `"`, `false`, `0`, `{}`, `[]`, `"https://example.com/\u0000"`} {
		before := backend.transactions
		response := client.request("PATCH", path, `{"external_url":`+value+`}`, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid URL persisted: %d %s", response.Code, response.Body)
		}
	}
	csrf := client.csrf
	client.csrf = "invalid"
	before = backend.transactions
	response = client.request("PATCH", path, `{"external_url":null}`, true)
	client.csrf = csrf
	if response.Code != http.StatusForbidden || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("URL write bypassed CSRF")
	}
	denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermissions{helpdesk.ChangeTicket})
	denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
	response = denied.request("PATCH", path, `{"external_url":null}`, true)
	if response.Code != http.StatusForbidden || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("URL write bypassed current authorization")
	}
	before = backend.updates
	response = client.request("PATCH", fmt.Sprintf("/api/tickets/%d/", outsideID), `{"external_url":null}`, true)
	if response.Code != http.StatusNotFound || backend.updates != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("URL escaped the category scope")
	}
	backend.rollback = true
	response = client.request("PATCH", path, `{"external_url":null}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("URL transaction failure published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	closeErr := second.Close()
	if err != nil || closeErr != nil || !found || !reflect.DeepEqual(baseline, stored) {
		t.Fatal("URL changed after reconnect", err, closeErr)
	}
	// Existing ORM values remain readable. Rendering them does not grant href authority.
	const legacy = `javascript:alert("legacy")`
	if _, err := models.TicketObjects.Update(ctx, runtime, stored, models.TicketPatch{}.WithExternalURL(legacy)); err != nil {
		t.Fatal(err)
	}
	response = client.request("GET", path, "", true)
	assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/{id}/", response)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `javascript:alert`) {
		t.Fatal("legacy URL was rejected during output")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), html.EscapeString(legacy)) || strings.Contains(response.Body.String(), `href="javascript:`) {
		t.Fatal("stored URL became active unsafe markup")
	}
	form.Set("subject", read().Subject)
	form.Set("external_url", "")
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().ExternalURL != nil {
		t.Fatal("blank nullable URL form did not clear the value")
	}
}
