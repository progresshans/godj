package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

func verifyHelpdeskFloat(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id int64) {
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
			t.Fatalf("Float row: %v", err)
		}
		return row
	}
	baseline := read()
	// This HTTP runtime owns a fresh CSRF signing key. Obtain its form token
	// before exercising authenticated JSON mutations with the durable session.
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `type="number" step="any" name="effort" value=""`) {
		t.Fatal("Admin Float input missing")
	}
	createdResponse := client.request("POST", "/api/tickets/", `{"subject":"Float create probe","effort":0.1}`, true)
	assertHelpdeskResponseDocumented(t, client.document, "POST", "/api/tickets/", createdResponse)
	var created struct {
		ID       int64
		Category int64
		Effort   *float64
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil || createdResponse.Code != http.StatusCreated || created.ID <= 0 || created.Category != categoryID || created.Effort == nil || *created.Effort != 0.1 {
		t.Fatalf("non-null Float create: status=%d decode=%v body=%s", createdResponse.Code, err, createdResponse.Body)
	}
	createdRow, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(created.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || createdRow.Effort == nil || *createdRow.Effort != 0.1 {
		t.Fatal("created Float missing from DB")
	}
	if _, err := deleteHelpdeskTicket(ctx, runtime, &createdRow); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"subject": {baseline.Subject}, "effort": {"NaN"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-field="effort"`) || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("nonfinite Admin input reached persistence")
	}
	markup := `"><script>alert(1)</script>`
	form.Set("effort", markup)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), html.EscapeString(markup)) || backend.transactions != before {
		t.Fatal("Float validation lost output escaping")
	}
	form.Set("effort", "1.5")
	response = client.request("POST", changePath, form.Encode(), false)
	if row := read(); response.Code != http.StatusFound || row.Effort == nil || *row.Effort != 1.5 {
		t.Fatal("Admin Float did not persist")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `type="number" step="any" name="effort" value="1.5"`) {
		t.Fatal("Admin Float initial value changed")
	}
	write := func(method, body string, want *float64) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var value map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || response.Code != http.StatusOK {
			t.Fatalf("Float %s: %d %s", method, response.Code, response.Body)
		}
		raw, present := value["effort"]
		if !present {
			t.Fatal("Float response omitted required nullable member")
		}
		if want == nil {
			if string(raw) != "null" {
				t.Fatal("Float NULL lost")
			}
			return
		}
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil || math.Float64bits(number) != math.Float64bits(*want) {
			t.Fatalf("Float response rounded: %s", raw)
		}
	}
	write("PATCH", `{"effort":0}`, new(0.0))
	before = backend.updates
	write("PATCH", `{}`, new(0.0))
	write("PATCH", `{"effort":-0.0}`, new(0.0))
	write("PATCH", `{"effort":"0e0"}`, new(0.0))
	if backend.updates != before {
		t.Fatal("numeric zero equivalence or omission caused write")
	}
	write("PUT", `{"subject":"Float visit"}`, new(0.0))
	write("PATCH", `{"effort":false}`, new(0.0))
	write("PATCH", `{"effort":9007199254740993}`, new(9007199254740992.0))
	for _, number := range []float64{math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 0.1} {
		wire, err := json.Marshal(map[string]float64{"effort": number})
		if err != nil {
			t.Fatal(err)
		}
		write("PATCH", string(wire), &number)
	}
	write("PATCH", `{"effort":null}`, nil)
	write("PUT", `{"subject":"Float visit","effort":1.5}`, new(1.5))
	baseline = read()
	// ORM storage admits infinity on both backends, while this application's
	// JSON and Admin projections must fail explicitly instead of emitting NULL
	// or invalid JSON. A failed read must leave the stored value intact.
	for _, number := range []float64{math.Inf(1), math.Inf(-1)} {
		outside, err := models.TicketObjects.Update(ctx, runtime, baseline, models.TicketPatch{}.WithEffort(number))
		if err != nil {
			t.Fatal(err)
		}
		before := backend.transactions
		for _, target := range []string{path, "/api/tickets/", changePath} {
			response := client.request("GET", target, "", false)
			if stored := read(); response.Code != http.StatusInternalServerError || backend.transactions != before || stored.Effort == nil || *stored.Effort != number {
				t.Fatalf("nonfinite model output did not fail without mutation: %s status=%d", target, response.Code)
			}
		}
		if _, err := models.TicketObjects.Update(ctx, runtime, outside, models.TicketPatch{}.WithEffort(*baseline.Effort)); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{`{"effort":"NaN"}`, `{"effort":"Infinity"}`, `{"effort":1e309}`, `{"effort":"bad"}`, `{"effort":"0x1p0"}`, `{"effort":1,"effort":null}`, `{"effort":[]}`, `{"effort":` + strings.Repeat("1", 400) + `}`} {
		before := backend.transactions
		response := client.request("PATCH", path, body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid Float changed storage: %d %s", response.Code, response.Body)
		}
	}
	backend.rollback = true
	before = backend.updates
	response = client.request("PATCH", path, `{"effort":2.5}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed Float transaction published state")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stored, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Effort == nil || *stored.Effort != 1.5 {
		t.Fatal("Float changed after reopen")
	}
	form.Set("effort", "")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().Effort != nil {
		t.Fatal("Admin blank did not clear Float")
	}
}
