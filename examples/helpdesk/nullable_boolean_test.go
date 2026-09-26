package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type helpdeskMutationCounter struct {
	helpdesk.Backend
	transactions, updates int
	rollback              bool
}

type helpdeskCountedSession struct {
	db.Session
	owner *helpdeskMutationCounter
}

func (s helpdeskCountedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	s.owner.updates++
	return s.Session.Update(ctx, plan)
}
func (b *helpdeskMutationCounter) AtomicRelation(ctx context.Context, fn func(db.RelationSession) error) error {
	b.transactions++
	return b.Backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		if err := fn(decoratedHelpdeskRelation{Session: helpdeskCountedSession{session, b}, relation: session}); err != nil {
			return err
		}
		if b.rollback {
			return errors.New("injected failure after real mutation")
		}
		return nil
	})
}

func verifyHelpdeskNullableBoolean(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, id, outsideID int64) {
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
			t.Fatalf("nullable Boolean row: %v", err)
		}
		return value
	}
	initial := read()
	response := client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<select name="reviewed">`) || !strings.Contains(response.Body.String(), `<option value="unknown" selected>Unknown</option>`) || strings.Contains(response.Body.String(), `<input type="checkbox" name="reviewed"`) {
		t.Fatal("nullable model did not render the unknown select state")
	}
	form := url.Values{"subject": {""}, "reviewed": {"false"}, "csrfmiddlewaretoken": {client.csrf}}
	before := backend.transactions
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<option value="false" selected>No</option>`) || backend.transactions != before || !reflect.DeepEqual(initial, read()) {
		t.Fatal("invalid form lost false or mutated data")
	}
	form.Set("subject", initial.Subject)
	form["reviewed"] = []string{"true", "false"}
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusOK || backend.transactions != before || !reflect.DeepEqual(initial, read()) {
		t.Fatal("repeated scalar reached mutation")
	}
	form.Set("reviewed", "false")
	response = client.request("POST", changePath, form.Encode(), false)
	if value := read(); response.Code != http.StatusFound || value.Reviewed == nil || *value.Reviewed {
		t.Fatal("Admin false did not persist as a nonnull Boolean")
	}
	response = client.request("GET", changePath, "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<option value="false" selected>No</option>`) {
		t.Fatal("Admin stored false rendered unknown")
	}
	write := func(method, body, reviewed string, closed bool) {
		t.Helper()
		response := client.request(method, path, body, true)
		assertHelpdeskResponseDocumented(t, client.document, method, "/api/tickets/{id}/", response)
		var encoded map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &encoded); err != nil || response.Code != http.StatusOK || string(encoded["reviewed"]) != reviewed || string(encoded["closed"]) != fmt.Sprint(closed) {
			t.Fatalf("%s nullable update: %d %s", method, response.Code, response.Body)
		}
	}
	write("PATCH", `{"reviewed":true,"closed":true,"details":"keep"}`, "true", true)
	before = backend.updates
	write("PATCH", `{}`, "true", true)
	write("PATCH", `{"reviewed":true}`, "true", true)
	if backend.updates != before {
		t.Fatal("empty or unchanged PATCH issued an UPDATE")
	}
	write("PUT", `{"subject":"Nullable review"}`, "true", false)
	if value := read(); value.Details == nil || *value.Details != "keep" {
		t.Fatal("PUT erased an omitted nullable field")
	}
	write("PATCH", `{"reviewed":false}`, "false", false)
	write("PATCH", `{"reviewed":null}`, "null", false)
	write("PUT", `{"subject":"Nullable review","reviewed":false}`, "false", false)
	baseline := read()
	for _, test := range []struct{ method, body string }{
		{"PUT", `{"reviewed":true}`}, {"PATCH", `{"reviewed":0}`}, {"PATCH", `{"reviewed":"false"}`},
		{"PATCH", `{"reviewed":[]}`}, {"PATCH", `{"reviewed":false,"reviewed":true}`},
		{"PATCH", `{"closed":null}`}, {"PATCH", `{"category":1}`}, {"PATCH", `{"id":1}`},
	} {
		before := backend.transactions
		response := client.request(test.method, path, test.body, true)
		if response.Code != http.StatusBadRequest || backend.transactions != before || !reflect.DeepEqual(baseline, read()) {
			t.Fatalf("invalid update performed I/O: %d %s", response.Code, response.Body)
		}
		assertHelpdeskResponseDocumented(t, client.document, test.method, "/api/tickets/{id}/", response)
	}
	for _, target := range []int64{0, -1, outsideID, id + 100000} {
		response := client.request("PATCH", fmt.Sprintf("/api/tickets/%d/", target), `{"reviewed":true}`, true)
		if response.Code != http.StatusNotFound {
			t.Fatalf("update bypassed category or identity: %d", response.Code)
		}
	}
	denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{helpdesk.ChangeTicket})
	denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
	for _, method := range []string{"PUT", "PATCH"} {
		before := backend.transactions
		if response := denied.request(method, path, "malformed", true); response.Code != http.StatusForbidden || backend.transactions != before {
			t.Fatal("change permission did not precede parsing and data access")
		}
	}
	token := client.csrf
	client.csrf = "invalid"
	before = backend.transactions
	response = client.request("PATCH", path, `{"reviewed":true}`, true)
	client.csrf = token
	if response.Code != http.StatusForbidden || backend.transactions != before {
		t.Fatal("CSRF failure reached data access")
	}
	backend.rollback = true
	before = backend.updates
	response = client.request("PATCH", path, `{"reviewed":true}`, true)
	backend.rollback = false
	if response.Code != http.StatusInternalServerError || backend.updates != before+1 || !reflect.DeepEqual(baseline, read()) {
		t.Fatal("failed transaction published or persisted the nullable update")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	persisted, found, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || persisted.Reviewed == nil || *persisted.Reviewed {
		t.Fatal("false/null distinction was lost across a fresh database connection")
	}
	// End with an explicit unknown state through the real Admin form.
	form.Set("reviewed", "unknown")
	form.Set("csrfmiddlewaretoken", client.csrf)
	response = client.request("POST", changePath, form.Encode(), false)
	if response.Code != http.StatusFound || read().Reviewed != nil {
		t.Fatal("Admin unknown did not clear the Boolean")
	}
}
