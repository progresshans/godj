package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
	"html"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func verifyHelpdeskDuration(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id int64) {
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
		value, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatalf("duration row: %v", err)
		}
		return value
	}
	baseline := read()
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="elapsed" value=""`) {
		t.Fatal("Admin duration did not render blank")
	}
	form := url.Values{"subject": {baseline.Subject}, "elapsed": {"P1Y"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="elapsed"`) || !strings.Contains(response.Body.String(), `value="P1Y"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("invalid Admin duration mutated data or lost submitted value")
	}
	badMarkup := `"><script>alert(1)</script>`
	form.Set("elapsed", badMarkup)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), html.EscapeString(badMarkup)) || backend.transactions != before {
		t.Fatal("invalid duration HTML escaped validation or output encoding")
	}
	form.Set("elapsed", "1 02:03:04.123456")
	response = client.request("POST", changePath, form.Encode(), false)
	if value := read(); response.Code != http.StatusFound || value.Elapsed == nil || *value.Elapsed != (duration.Duration{Days: 1, Microseconds: 7384123456}) {
		t.Fatal("Admin duration did not persist")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="elapsed" value="1 02:03:04.123456"`) {
		t.Fatal("Admin canonical duration initial failed")
	}
	write := func(method, body, want string) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var value map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || response.Code != http.StatusOK || string(value["elapsed"]) != want {
			t.Fatalf("time %s=%d %s", method, response.Code, response.Body)
		}
	}
	write("PATCH", `{"elapsed":"00:00:00"}`, `"00:00:00"`)
	before = backend.updates
	write("PATCH", `{}`, `"00:00:00"`)
	write("PATCH", `{"elapsed":"PT0S"}`, `"00:00:00"`)
	if backend.updates != before {
		t.Fatal("omitted/equivalent duration PATCH issued update")
	}
	write("PUT", `{"subject":"Clock visit"}`, `"00:00:00"`)
	write("PATCH", `{"elapsed":0.5}`, `"00:00:00.500000"`)
	write("PATCH", `{"elapsed":null}`, `null`)
	write("PUT", `{"subject":"Clock visit","elapsed":"-1 23:59:59.999999"}`, `"-1 23:59:59.999999"`)
	baseline = read()
	for _, body := range []string{`{"elapsed":false}`, `{"elapsed":"P1Y"}`, `{"elapsed":"1000000000 00:00:00"}`, `{"elapsed":"bad"}`, `{"elapsed":"1 02:03:04.123456T00:00:00Z"}`, `{"elapsed":"1","elapsed":null}`, `{"elapsed":[]}`} {
		before := backend.transactions
		response := client.request("PATCH", path, body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid duration update changed storage: %d %s", response.Code, response.Body)
		}
	}
	backend.rollback = true
	before = backend.updates
	response = client.request("PATCH", path, `{"elapsed":"1 02:03:04.123456"}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed duration transaction published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Elapsed == nil || *stored.Elapsed != (duration.Duration{Days: -1, Microseconds: duration.MicrosecondsPerDay - 1}) {
		t.Fatal("duration changed after database reopen")
	}
	form.Set("elapsed", "")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().Elapsed != nil {
		t.Fatal("Admin blank did not clear duration")
	}
}
