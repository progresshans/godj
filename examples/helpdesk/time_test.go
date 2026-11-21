package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/clock"
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

func verifyHelpdeskClockTime(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id int64) {
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
			t.Fatalf("clock time row: %v", err)
		}
		return value
	}
	baseline := read()
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="service_at" value="" placeholder="HH:MM:SS"`) {
		t.Fatal("Admin time did not render blank")
	}
	form := url.Values{"subject": {baseline.Subject}, "service_at": {"12:34:60"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="service_at"`) || !strings.Contains(response.Body.String(), `value="12:34:60"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("invalid Admin time mutated data or lost submitted value")
	}
	form.Set("service_at", "12:34:56.123456")
	response = client.request("POST", changePath, form.Encode(), false)
	if value := read(); response.Code != http.StatusFound || value.ServiceAt == nil || *value.ServiceAt != (clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 123456}) {
		t.Fatal("Admin time did not persist")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="service_at" value="12:34:56.123456"`) {
		t.Fatal("Admin canonical time initial failed")
	}
	write := func(method, body, want string) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var value map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || response.Code != http.StatusOK || string(value["service_at"]) != want {
			t.Fatalf("time %s=%d %s", method, response.Code, response.Body)
		}
	}
	write("PATCH", `{"service_at":"00:00:00"}`, `"00:00:00"`)
	before = backend.updates
	write("PATCH", `{}`, `"00:00:00"`)
	write("PATCH", `{"service_at":"240000"}`, `"00:00:00"`)
	if backend.updates != before {
		t.Fatal("omitted/equivalent time PATCH issued update")
	}
	write("PUT", `{"subject":"Clock visit"}`, `"00:00:00"`)
	write("PATCH", `{"service_at":null}`, `null`)
	write("PUT", `{"subject":"Clock visit","service_at":"23:59:59.999999"}`, `"23:59:59.999999"`)
	baseline = read()
	for _, body := range []string{`{"service_at":false}`, `{"service_at":0}`, `{"service_at":""}`, `{"service_at":"12:34:60"}`, `{"service_at":"124:00:00"}`, `{"service_at":"12:34:56.123456T00:00:00Z"}`, `{"service_at":"12:34:56.123456","service_at":null}`, `{"service_at":[]}`} {
		before := backend.transactions
		response := client.request("PATCH", path, body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid time update changed storage: %d %s", response.Code, response.Body)
		}
	}
	backend.rollback = true
	before = backend.updates
	response = client.request("PATCH", path, `{"service_at":"12:34:56.123456"}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed time transaction published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.ServiceAt == nil || *stored.ServiceAt != (clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}) {
		t.Fatal("time changed after database reopen")
	}
	form.Set("service_at", "")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().ServiceAt != nil {
		t.Fatal("Admin blank did not clear time")
	}
}
