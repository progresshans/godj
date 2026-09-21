package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type helpdeskSearchCounter struct {
	helpdesk.Backend
	queries int
}

func (b *helpdeskSearchCounter) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b.queries++
	return b.Backend.Query(ctx, plan)
}

func verifyHelpdeskJSONSearch(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient, categoryID, outsideID int64) {
	t.Helper()
	backend := &helpdeskSearchCounter{Backend: runtime}
	app, err := helpdesk.New(backend, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, app, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	var owned []models.Ticket
	defer func() {
		for _, row := range owned {
			if _, err := deleteHelpdeskTicket(ctx, runtime, &row); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, input := range []struct{ subject, payload string }{
		{"json-search vendor subject", ""},
		{"json-search chosen data", `{"source":"VENDOR%_A","note":"only-json"}`},
		{"json-search unrelated data", `{"source":"vendorB","note":"shared"}`},
		{"json-search missing source", `{"other":"vendor"}`},
	} {
		create := models.NewTicketCreate(input.subject, categoryID)
		if input.payload != "" {
			create = create.WithExternalPayload(helpdeskJSON(t, input.payload))
		}
		row, err := models.TicketObjects.Create(ctx, runtime, create)
		if err != nil {
			t.Fatal(err)
		}
		owned = append(owned, row)
	}
	outside, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(outsideID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal("outside search fixture absent", err)
	}
	original := outside.ExternalPayload
	if _, err := models.TicketObjects.Update(ctx, runtime, outside, models.TicketPatch{}.WithExternalPayload(helpdeskJSON(t, `{"source":"VENDOR%_A","note":"only-json"}`))); err != nil {
		t.Fatal(err)
	}
	defer func() {
		patch := models.TicketPatch{}.WithExternalPayloadNull()
		if original != nil {
			patch = patch.WithExternalPayload(*original)
		}
		if _, err := models.TicketObjects.Update(ctx, runtime, outside, patch); err != nil {
			t.Error(err)
		}
	}()
	for _, tc := range []struct {
		search, source string
		want           []int64
	}{
		{"json-search", "", []int64{owned[0].ID, owned[1].ID, owned[2].ID, owned[3].ID}},
		{"only-json", "", []int64{owned[1].ID}},
		{"", "VENDOR", []int64{owned[1].ID, owned[2].ID}},
		{"", "%_", []int64{owned[1].ID}},
		{"json-search chosen", "vendor", []int64{owned[1].ID}},
		{"json-search missing", "vendor", nil},
	} {
		raw := url.Values{"search": {tc.search}, "source": {tc.source}}.Encode()
		response := client.request("GET", "/api/tickets/?"+raw, "", false)
		assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/", response)
		var rows []struct{ ID int64 }
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &rows) != nil {
			t.Fatal("JSON query response", raw, response.Code, response.Body)
		}
		ids := make([]int64, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		if !slices.Equal(ids, tc.want) {
			t.Fatal("JSON query selection/category scope", raw, ids, tc.want)
		}
	}
	// The ordinary Admin search includes whole external JSON and uses the same
	// trusted category restriction; source filtering is an explicit API option.
	response := client.request("GET", "/admin/tickets/?q=only-json", "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "json-search chosen data") || strings.Contains(response.Body.String(), "json-search unrelated data") {
		t.Fatal("Admin JSON search", response.Code, response.Body)
	}
	invalid := []string{"source=a&source=b", "search=a&search=b", "sour%63e=a&source=b", "source=%00", "search=%FF", "source=%ZZ", "source=" + strings.Repeat("a", 65), "source=" + url.QueryEscape(strings.Repeat("한", 22)), "category=" + fmt.Sprint(outsideID), "search=ignored&page=999&unknown=ignored", "search=" + strings.Repeat("a", 2049)}
	for _, raw := range invalid {
		before := backend.queries
		response := client.request("GET", "/api/tickets/?"+raw, "", false)
		assertHelpdeskResponseDocumented(t, client.document, "GET", "/api/tickets/", response)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"validation_error"`) || backend.queries != before {
			t.Fatal("invalid query performed I/O or escaped validation", raw, response.Code, response.Body, backend.queries-before)
		}
	}
	denied := helpdeskHTTP(t, app, runtime, helpdeskDeniedPermission{helpdesk.ViewTicket})
	denied.cookies = maps.Clone(client.cookies)
	for _, candidate := range []*helpdeskClient{denied, helpdeskHTTP(t, app, runtime, auth.PrincipalAuthorizer{})} {
		before := backend.queries
		response := candidate.request("GET", "/api/tickets/?source=%00", "", false)
		if response.Code != http.StatusForbidden || backend.queries != before {
			t.Fatal("query parsing preceded authentication/permission", response.Code, backend.queries-before)
		}
	}
}
