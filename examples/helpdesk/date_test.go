package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func verifyHelpdeskCalendarDate(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id int64) {
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
			t.Fatalf("calendar date row: %v", err)
		}
		return value
	}
	baseline := read()
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="service_on" value="" placeholder="YYYY-MM-DD"`) {
		t.Fatal("Admin date did not render blank")
	}
	form := url.Values{"subject": {baseline.Subject}, "service_on": {"1900-02-29"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="service_on"`) || !strings.Contains(response.Body.String(), `value="1900-02-29"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("invalid Admin date mutated data or lost submitted value")
	}
	form.Set("service_on", "February 29, 2000")
	response = client.request("POST", changePath, form.Encode(), false)
	if value := read(); response.Code != http.StatusFound || value.ServiceOn == nil || *value.ServiceOn != (calendar.Date{Year: 2000, Month: 2, Day: 29}) {
		t.Fatal("Admin date did not persist")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="service_on" value="2000-02-29"`) {
		t.Fatal("Admin canonical date initial failed")
	}
	write := func(method, body, want string) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var value map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || response.Code != http.StatusOK || string(value["service_on"]) != want {
			t.Fatalf("date %s=%d %s", method, response.Code, response.Body)
		}
	}
	write("PATCH", `{"service_on":"0001-01-01"}`, `"0001-01-01"`)
	before = backend.updates
	write("PATCH", `{}`, `"0001-01-01"`)
	write("PATCH", `{"service_on":"00010101"}`, `"0001-01-01"`)
	if backend.updates != before {
		t.Fatal("omitted/equivalent date PATCH issued update")
	}
	write("PUT", `{"subject":"Calendar visit"}`, `"0001-01-01"`)
	write("PATCH", `{"service_on":null}`, `null`)
	write("PUT", `{"subject":"Calendar visit","service_on":"9999-12-31"}`, `"9999-12-31"`)
	baseline = read()
	for _, body := range []string{`{"service_on":false}`, `{"service_on":0}`, `{"service_on":""}`, `{"service_on":"1900-02-29"}`, `{"service_on":"10000-01-01"}`, `{"service_on":"2026-09-20T00:00:00Z"}`, `{"service_on":"2026-09-20","service_on":null}`, `{"service_on":[]}`} {
		before := backend.transactions
		response := client.request("PATCH", path, body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid date update changed storage: %d %s", response.Code, response.Body)
		}
	}
	backend.rollback = true
	before = backend.updates
	response = client.request("PATCH", path, `{"service_on":"2000-02-29"}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed date transaction published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.ServiceOn == nil || *stored.ServiceOn != (calendar.Date{Year: 9999, Month: 12, Day: 31}) {
		t.Fatal("date changed after database reopen")
	}
	form.Set("service_on", "")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().ServiceOn != nil {
		t.Fatal("Admin blank did not clear date")
	}
}
