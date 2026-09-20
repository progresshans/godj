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
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

func verifyHelpdeskDecimal(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id, outsideID int64) {
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
			t.Fatal("decimal row missing", err)
		}
		return row
	}
	baseline := read()
	if baseline.ExpectedCost != nil {
		t.Fatal("new nullable Decimal did not start NULL")
	}
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `type="number" step="0.01" name="expected_cost"`) {
		t.Fatal("Decimal Admin widget missing")
	}
	createdResponse := client.request("POST", "/api/tickets/", `{"subject":"Decimal create probe","expected_cost":0.1}`, true)
	assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", createdResponse)
	var created struct {
		ID, Category int64
		Cost         *string `json:"expected_cost"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil || createdResponse.Code != http.StatusCreated || created.ID <= 0 || created.Category != categoryID || created.Cost == nil || *created.Cost != "0.10" {
		t.Fatalf("Decimal create: %d %s", createdResponse.Code, createdResponse.Body)
	}
	createdRow, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || createdRow.ExpectedCost == nil || createdRow.ExpectedCost.String() != "0.1" {
		t.Fatal("created Decimal missing from DB")
	}
	if _, err := models.TicketObjects.Delete(ctx, runtime, &createdRow); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"subject": {baseline.Subject}, "expected_cost": {"1.230"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="expected_cost"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("excess-scale Admin input reached persistence")
	}
	markup := `"><script>alert(1)</script>`
	form.Set("expected_cost", markup)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), html.EscapeString(markup)) || backend.transactions != before {
		t.Fatal("Decimal invalid form lost escaping")
	}
	form.Set("expected_cost", "123.45")
	response = client.request("POST", changePath, form.Encode(), false)
	if row := read(); response.Code != http.StatusFound || row.ExpectedCost == nil || row.ExpectedCost.String() != "123.45" {
		t.Fatal("Admin Decimal did not persist")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `type="number" step="0.01" name="expected_cost" value="123.45"`) {
		t.Fatal("Admin Decimal fixed scale changed")
	}
	write := func(method, body string, want *string) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var values map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &values); err != nil || response.Code != http.StatusOK {
			t.Fatalf("Decimal %s: %d %s", method, response.Code, response.Body)
		}
		raw, present := values["expected_cost"]
		if !present {
			t.Fatal("required nullable Decimal response missing")
		}
		stored := read()
		if want == nil {
			if string(raw) != "null" || stored.ExpectedCost != nil {
				t.Fatal("Decimal null was not stored")
			}
			return
		}
		var value string
		expected, err := decimal.Parse(*want)
		if json.Unmarshal(raw, &value) != nil || value != *want || err != nil || stored.ExpectedCost == nil || !stored.ExpectedCost.Equal(expected) {
			t.Fatalf("Decimal response/DB precision changed: %s want %s", raw, *want)
		}
	}
	write("PATCH", `{"expected_cost":0}`, new("0.00"))
	before = backend.updates
	write("PATCH", `{}`, new("0.00"))
	write("PATCH", `{"expected_cost":-0.0}`, new("0.00"))
	write("PATCH", `{"expected_cost":"0.00"}`, new("0.00"))
	if backend.updates != before {
		t.Fatal("Decimal equivalent zero or omission caused update")
	}
	write("PUT", `{"subject":"Decimal visit"}`, new("0.00"))
	for _, value := range []string{"-9999999999.99", "9999999999.99", "-0.01", "0.01", "0.10", "1.50"} {
		body, _ := json.Marshal(map[string]string{"expected_cost": value})
		write("PATCH", string(body), &value)
	}
	write("PATCH", `{"expected_cost":null}`, nil)
	write("PUT", `{"subject":"Decimal visit","expected_cost":1.5}`, new("1.50"))
	baseline = read()
	for _, body := range []string{`{"expected_cost":"1.230"}`, `{"expected_cost":1.230}`, `{"expected_cost":10000000000}`, `{"expected_cost":123456789012345678.12}`, `{"expected_cost":"NaN"}`, `{"expected_cost":"Infinity"}`, `{"expected_cost":1e309}`, `{"expected_cost":1e-9999}`, `{"expected_cost":true}`, `{"expected_cost":[]}`, `{"expected_cost":{}}`, `{"expected_cost":"1","expected_cost":null}`, `{"expected_cost":"` + strings.Repeat("1", 1001) + `"}`} {
		before := backend.transactions
		response := client.request("PATCH", path, body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid Decimal changed storage: %d %s", response.Code, response.Body)
		}
	}
	csrf := client.csrf
	client.csrf = "invalid"
	before = backend.transactions
	response = client.request("PATCH", path, `{"expected_cost":"2.50"}`, true)
	client.csrf = csrf
	if response.Code != http.StatusForbidden || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("Decimal write bypassed CSRF")
	}
	before = backend.updates
	response = client.request("PATCH", fmt.Sprintf("/api/tickets/%d/", outsideID), `{"expected_cost":"2.50"}`, true)
	if response.Code != http.StatusNotFound || backend.updates != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("Decimal escaped category isolation")
	}
	backend.rollback = true
	before = backend.updates
	response = client.request("PATCH", path, `{"expected_cost":"2.50"}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed Decimal transaction published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.ExpectedCost == nil || stored.ExpectedCost.String() != "1.5" {
		t.Fatal("Decimal changed after reopen")
	}
	form.Set("expected_cost", "")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().ExpectedCost != nil {
		t.Fatal("Admin blank did not clear Decimal")
	}
}
