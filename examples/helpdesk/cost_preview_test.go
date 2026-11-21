package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

type costPreviewItem struct {
	ID       int64
	Subject  string
	Original *string `json:"expected_cost"`
	Adjusted *string `json:"adjusted_cost"`
}
type costPreviewEnvelope struct {
	Category struct {
		ID   int64
		Name string
	}
	Page          int64
	PageSize      int64 `json:"page_size"`
	Factor, Delta string
	Minimum       *string `json:"min_cost"`
	Total         int64   `json:"total_tickets"`
	Results       []costPreviewItem
}

func TestCostPreviewRequiresUsableAuthentication(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, authentication := range []*sessionauth.Runtime{nil, {}} {
		if _, err := application.CostPreview(authentication); err == nil {
			t.Fatal("cost preview accepted unusable authentication")
		}
	}
}

func verifyHelpdeskCostPreview(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Cost <script>& category"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Private outside costs"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := jsonvalue.Parse([]byte(`{"retain":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	var tickets []models.Ticket
	for index := range 45 {
		input := models.NewTicketCreate(fmt.Sprintf("Cost <script>& %02d", index), category.ID).WithClosed(index%2 == 0).WithDetails("Unchanged details").WithExternalPayload(payload)
		if index%5 != 0 {
			text := []string{"0", "0.1", "-7.25", "999999999999.99"}[(index-1)%4]
			value, err := decimal.Parse(text)
			if err != nil {
				t.Fatal(err)
			}
			input = input.WithExpectedCost(value)
		}
		row, err := models.TicketObjects.Create(ctx, runtime, input)
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, row)
	}
	private, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private outside ticket", outside.ID).WithExpectedCost(decimal.Decimal{Coefficient: "1", Exponent: 11}))
	if err != nil {
		t.Fatal(err)
	}
	label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Retained cost label", category.ID))
	if err != nil {
		t.Fatal(err)
	}
	link, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(tickets[0].ID, label.ID))
	if err != nil {
		t.Fatal(err)
	}
	report, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(tickets[0].ID, "Retained cost report"))
	if err != nil {
		t.Fatal(err)
	}
	newClient := func(categoryID int64, mode string, authorizer auth.Authorizer) (*helpdeskClient, *summaryBackend, *int) {
		backend := &summaryBackend{Backend: runtime, mode: mode}
		application, err := helpdesk.New(backend, categoryID)
		if err != nil {
			t.Fatal(err)
		}
		audits := new(int)
		client := helpdeskHTTP(t, application, runtime, authorizer, func(context.Context, db.Session, admin.PreparedEvent) error {
			*audits++
			return errors.New("cost preview attempted audit")
		})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		return client, backend, audits
	}
	decode := func(client *helpdeskClient, suffix string) costPreviewEnvelope {
		response := client.request(http.MethodGet, "/api/tickets/cost-preview/"+suffix, "", false)
		var page costPreviewEnvelope
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cost preview response", response.Code, response.Body.String())
		}
		var raw struct{ Results []map[string]json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		for _, row := range raw.Results {
			for _, name := range []string{"id", "subject", "expected_cost", "adjusted_cost"} {
				if _, present := row[name]; !present {
					t.Fatal("cost preview omitted required nullable output", name)
				}
			}
		}
		return page
	}
	// Independently compute expected values as rationals, never using the query
	// expression or database implementation under test. The admitted operands
	// make six fractional places sufficient without rounding.
	expected := func(factor, delta string, minimum *string) []costPreviewItem {
		factorValue, ok := new(big.Rat).SetString(factor)
		if !ok {
			t.Fatal(factor)
		}
		deltaValue, ok := new(big.Rat).SetString(delta)
		if !ok {
			t.Fatal(delta)
		}
		var threshold *big.Rat
		if minimum != nil {
			threshold, ok = new(big.Rat).SetString(*minimum)
			if !ok {
				t.Fatal(*minimum)
			}
		}
		values := map[int64]*big.Rat{}
		rows := make([]costPreviewItem, 0, len(tickets))
		for _, ticket := range tickets {
			row := costPreviewItem{ID: ticket.ID, Subject: ticket.Subject}
			if ticket.ExpectedCost != nil {
				original := ticket.ExpectedCost.String()
				value, ok := new(big.Rat).SetString(original)
				if !ok {
					t.Fatal(original)
				}
				value.Mul(value, factorValue)
				value.Add(value, deltaValue)
				if threshold != nil && value.Cmp(threshold) < 0 {
					continue
				}
				text := strings.TrimRight(strings.TrimRight(value.FloatString(6), "0"), ".")
				if text == "-0" {
					text = "0"
				}
				row.Original, row.Adjusted = &original, &text
				values[row.ID] = value
			} else if threshold != nil {
				continue
			}
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool {
			left, right := values[rows[i].ID], values[rows[j].ID]
			if left == nil || right == nil {
				if left != right {
					return left != nil
				}
			} else if compare := left.Cmp(right); compare != 0 {
				return compare > 0
			}
			return rows[i].ID < rows[j].ID
		})
		return rows
	}
	t.Run("exact_rows_filters_pages_and_category", func(t *testing.T) {
		zero, empty := "0", "999999999999999.999999"
		for _, input := range []struct {
			factor, delta string
			minimum       *string
		}{{"1", "0", nil}, {"1.25", "0.1", &zero}, {"-0.5", "-0.01", nil}, {"0", "0", nil}, {"1", "0", &empty}} {
			want := expected(input.factor, input.delta, input.minimum)
			for _, number := range []int{1, 2, 3, 50001} {
				client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
				query := url.Values{"p": {strconv.Itoa(number)}, "factor": {input.factor}, "delta": {input.delta}}
				if input.minimum != nil {
					query.Set("min_cost", *input.minimum)
				}
				page := decode(client, "?"+query.Encode())
				start := min(len(want), (number-1)*20)
				end := min(len(want), start+20)
				if page.Category.ID != category.ID || page.Category.Name != category.Name || page.Page != int64(number) || page.PageSize != 20 || page.Factor != input.factor || page.Delta != input.delta || !reflect.DeepEqual(page.Minimum, input.minimum) || page.Total != int64(len(want)) || page.Results == nil || !reflect.DeepEqual(page.Results, want[start:end]) {
					t.Fatal("exact cost preview differs", input, number, page, want[start:end])
				}
				if backend.snapshots != 1 || backend.queries != 2 || backend.groups != 1 || backend.rowsets != 1 || backend.closed != 1 || *audits != 0 {
					t.Fatal("cost preview snapshot/page ownership", backend)
				}
			}
		}
	})
	t.Run("html_escape_and_preserved_filter_links", func(t *testing.T) {
		client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
		response := client.request(http.MethodGet, helpdesk.CostPreviewPath+"?factor=1.25&delta=0.1&min_cost=0", "", false)
		body := response.Body.String()
		for _, required := range []string{"Expected cost preview", "Cost &lt;script&gt;&amp; category", "Cost &lt;script&gt;&amp;", "1250000000000.0875", "delta=0.1&amp;factor=1.25&amp;min_cost=0&amp;p=2", "name=\"factor\" value=\"1.25\"", "Clear minimum"} {
			if !strings.Contains(body, required) {
				t.Fatal("cost preview HTML missing", required, body)
			}
		}
		if response.Code != http.StatusOK || strings.Contains(body, "<script>") || strings.Contains(body, "Private outside") || strings.Contains(body, "csrfmiddlewaretoken") || backend.snapshots != 1 || *audits != 0 {
			t.Fatal("cost preview HTML leaked or wrote", response.Code)
		}
		page := client.request(http.MethodGet, helpdesk.CostPreviewPath+"?p=3", "", false)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Not set") || strings.Contains(page.Body.String(), "Next page") {
			t.Fatal("null costs or final page link", page.Body.String())
		}
	})
	t.Run("admission_and_input_before_lookup", func(t *testing.T) {
		for _, path := range []string{helpdesk.CostPreviewPath, "/api/tickets/cost-preview/"} {
			client, backend, _ := newClient(category.ID, "", helpdeskDeniedPermissions{helpdesk.ViewTicket})
			if response := client.request(http.MethodGet, path+"?factor=bad", "", false); response.Code != http.StatusForbidden || backend.snapshots != 0 {
				t.Fatal("missing cost permission reached parsing", response.Code)
			}
			client.cookies = map[string]*http.Cookie{}
			want := http.StatusFound
			if strings.HasPrefix(path, "/api/") {
				want = http.StatusForbidden
			}
			if response := client.request(http.MethodGet, path+"?factor=bad", "", false); response.Code != want || backend.snapshots != 0 {
				t.Fatal("anonymous cost preview reached data", response.Code)
			}
			client, backend, _ = newClient(category.ID, "", auth.PrincipalAuthorizer{})
			for _, suffix := range []string{"?p=0", "?p=50002", "?p=01", "?p=", "?p=1&p=2", "?factor=", "?factor=1.0", "?factor=1e0", "?factor=%2B1", "?factor=100", "?factor=0.00001", "?factor=%FF", "?factor=%", "?factor=1&factor=1", "?delta=1000000000000", "?delta=0.001", "?min_cost=", "?min_cost=1.0000001", "?min_cost=1000000000000000", "?category=1", "?factor=1;delta=0", "?" + strings.Repeat("x", 257)} {
				if response := client.request(http.MethodGet, path+suffix, "", false); response.Code != http.StatusBadRequest || backend.snapshots != 0 {
					t.Fatal("invalid cost input reached data", path, suffix, response.Code)
				}
			}
		}
	})
	t.Run("empty_and_missing_category", func(t *testing.T) {
		empty, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Empty costs"))
		if err != nil {
			t.Fatal(err)
		}
		client, _, _ := newClient(empty.ID, "", auth.PrincipalAuthorizer{})
		page := decode(client, "")
		if page.Total != 0 || len(page.Results) != 0 || page.Results == nil || page.Factor != "1" || page.Delta != "0" || page.Minimum != nil {
			t.Fatal("empty cost defaults", page)
		}
		for _, path := range []string{helpdesk.CostPreviewPath, "/api/tickets/cost-preview/"} {
			client, backend, _ := newClient(math.MaxInt64, "", auth.PrincipalAuthorizer{})
			if response := client.request(http.MethodGet, path, "", false); response.Code != http.StatusNotFound || backend.groups != 0 {
				t.Fatal("missing cost category queried tickets", response.Code)
			}
		}
	})
	for _, mode := range []string{"missing_callback", "nil_reader", "typed_nil_reader", "double_callback", "concurrent_callback", "swallowed_error", "cleanup_failure", "post_cleanup_cancel", "query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure", "late_callback", "unfinished_callback"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{helpdesk.CostPreviewPath, "/api/tickets/cost-preview/"} {
				client, backend, audits := newClient(category.ID, mode, auth.PrincipalAuthorizer{})
				work, cancel := context.WithCancel(ctx)
				defer cancel()
				backend.cancel = cancel
				backend.started, backend.release, backend.finished = make(chan struct{}), make(chan struct{}), make(chan error, 1)
				response := client.requestContext(work, http.MethodGet, path, "", false)
				if mode == "late_callback" {
					calls := backend.queries
					if backend.late == nil || backend.late() == nil || backend.queries != calls {
						t.Fatal("late cost snapshot reached data")
					}
				}
				if mode == "unfinished_callback" {
					close(backend.release)
					if err := <-backend.finished; err == nil {
						t.Fatal("unfinished cost snapshot escaped cancellation")
					}
				}
				if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private summary") || strings.Contains(response.Body.String(), "total_tickets") || strings.Contains(response.Body.String(), "Cost &lt;script&gt;") || *audits != 0 {
					t.Fatal("failed cost preview published output", mode, response.Code, response.Body.String())
				}
				if slices.Contains([]string{"scan_failure", "iteration_failure", "close_failure"}, mode) && backend.closed != 1 {
					t.Fatal("cost cursor leaked", mode, backend.closed)
				}
			}
		})
	}
	t.Run("stored_models_relations_and_audit_unchanged", func(t *testing.T) {
		all := append(append([]models.Ticket{}, tickets...), private)
		keys := make([]int64, len(all))
		for i, row := range all {
			keys[i] = row.ID
		}
		actual, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.In(keys...)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
		if err != nil || !reflect.DeepEqual(actual, all) {
			t.Fatal("cost preview changed full tickets or payload digest", err)
		}
		storedLabel, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(label.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLabel, label) {
			t.Fatal("cost preview changed label", err)
		}
		storedLink, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(link.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLink, link) {
			t.Fatal("cost preview changed link", err)
		}
		storedReport, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.Exact(report.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedReport, report) {
			t.Fatal("cost preview changed report", err)
		}
		for _, check := range []struct {
			model string
			keys  []int64
		}{{"helpdesk.ticket", keys}, {"helpdesk.label", []int64{label.ID}}, {"helpdesk.service_report", []int64{report.ID}}} {
			for _, id := range check.keys {
				history, err := runtime.AuditHistory(ctx, check.model, id, 10)
				if err != nil || len(history) != 0 {
					t.Fatal("cost preview appended audit", check.model, err)
				}
			}
		}
	})
}

func assertCostPreviewContracts(t *testing.T, document helpdeskDocument) {
	t.Helper()
	operation := document.Paths["/api/tickets/cost-preview/"]["get"]
	schema := document.Components.Schemas["CostPreview"]
	if operation.Responses["200"].Content["application/json"].Schema.Ref != "#/components/schemas/CostPreview" || len(operation.Parameters) != 4 || schema.AdditionalProperties || !slices.Equal(schema.Required, []string{"category", "page", "page_size", "factor", "delta", "min_cost", "total_tickets", "results"}) {
		t.Fatal("cost preview lost its closed page contract")
	}
	for index, name := range []string{"p", "factor", "delta", "min_cost"} {
		if operation.Parameters[index].Name != name || operation.Parameters[index].Required || operation.Parameters[index].AllowEmptyValue {
			t.Fatal("cost preview parameter presence", name)
		}
	}
	for index, precision := range [][2]int{{6, 4}, {14, 2}, {21, 6}} {
		input := operation.Parameters[index+1].Schema
		var policy struct {
			MaxDigits      int
			DecimalPlaces  int
			Representation string
		}
		if input.Type != "string" || input.Pattern == "" || input.MinLength != 1 || input.MaxLength != []int{8, 16, 23}[index] || json.Unmarshal(input.DecimalPolicy, &policy) != nil || policy.MaxDigits != precision[0] || policy.DecimalPlaces != precision[1] || policy.Representation != "canonical-decimal-string" {
			t.Fatal("cost preview lost exact parameter precision", index, input)
		}
	}
	if string(operation.Parameters[0].Schema.Default) != "1" || string(operation.Parameters[1].Schema.Default) != `"1"` || string(operation.Parameters[2].Schema.Default) != `"0"` || len(operation.Parameters[3].Schema.Default) != 0 {
		t.Fatal("cost preview omission/default differs")
	}
	rows := schema.Properties["results"]
	if rows.Type != "array" || rows.MaxItems != 20 || rows.Items == nil || rows.Items.AdditionalProperties || !slices.Equal(rows.Items.Required, []string{"id", "subject", "expected_cost", "adjusted_cost"}) {
		t.Fatal("cost preview required bounded rows")
	}
	for _, name := range []string{"expected_cost", "adjusted_cost"} {
		value := rows.Items.Properties[name]
		if len(value.AnyOf) != 2 || value.AnyOf[0].Type != "string" || value.AnyOf[0].Pattern == "" || !value.allowsType("null") {
			t.Fatal("cost preview exact result/null distinction", name)
		}
	}
	if !schema.Properties["min_cost"].allowsType("null") {
		t.Fatal("omitted minimum was lost")
	}
}
