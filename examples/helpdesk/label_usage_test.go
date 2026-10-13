package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

type labelUsageItem struct {
	ID                  int64
	Name                string
	Total, Open, Closed int64
	Cost                *string  `json:"expected_cost_total"`
	Effort              *float64 `json:"effort_average"`
	Elapsed             *string  `json:"elapsed_total"`
}
type labelUsageEnvelope struct {
	Category struct {
		ID   int64
		Name string
	}
	Page     int64
	PageSize int64 `json:"page_size"`
	Usage    string
	Total    int64 `json:"total_labels"`
	Results  []labelUsageItem
}

func TestLabelUsageRequiresUsableAuthentication(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, authentication := range []*sessionauth.Runtime{nil, {}} {
		if _, err := application.LabelUsage(authentication); err == nil {
			t.Fatal("usage accepted unusable authentication")
		}
	}
}

func verifyHelpdeskLabelUsage(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Labels <script>& category"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Private outside labels"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonvalue.Parse([]byte(`{"retain":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	var tickets []models.Ticket
	for _, row := range []struct {
		subject string
		closed  bool
		cost    string
		effort  float64
		micros  int64
		metrics bool
	}{
		{"Retained open", false, "999999999999.99", 1, 1, true}, {"Retained closed", true, "0.02", 3, -1, true},
		{"Retained missing metrics", false, "", 0, 0, false}, {"Retained zero metrics", true, "0", 0, 0, true},
	} {
		input := models.NewTicketCreate(row.subject, category.ID).WithClosed(row.closed).WithExternalPayload(document)
		if row.metrics {
			cost, err := decimal.Parse(row.cost)
			if err != nil {
				t.Fatal(err)
			}
			input = input.WithExpectedCost(cost).WithEffort(row.effort).WithElapsed(duration.FromMicroseconds(row.micros))
		}
		saved, err := models.TicketObjects.Create(ctx, runtime, input)
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, saved)
	}
	cost, err := decimal.Parse("999999999999.99")
	if err != nil {
		t.Fatal(err)
	}
	privateTicket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private outside ticket", outside.ID).WithClosed(false).WithExpectedCost(cost).WithEffort(9000).WithElapsed(duration.FromMicroseconds(999)))
	if err != nil {
		t.Fatal(err)
	}
	tickets = append(tickets, privateTicket)
	var labels []models.Label
	var links []models.TicketLabel
	var expected []labelUsageItem
	link := func(ticket, label int64) {
		saved, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(ticket, label))
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, saved)
	}
	pointer := func(s string) *string { return &s }
	effort := func(n float64) *float64 { return &n }
	for index := 0; index < 45; index++ {
		label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(fmt.Sprintf("Label <script>& %02d", index), category.ID))
		if err != nil {
			t.Fatal(err)
		}
		labels = append(labels, label)
		row := labelUsageItem{ID: label.ID, Name: label.Name}
		switch index % 3 {
		case 0:
			for _, ticket := range tickets[:3] {
				link(ticket.ID, label.ID)
			}
			row.Total, row.Open, row.Closed = 3, 2, 1
			row.Cost, row.Effort, row.Elapsed = pointer("1000000000000.01"), effort(2), pointer("00:00:00")
		case 2:
			link(tickets[3].ID, label.ID)
			row.Total, row.Closed = 1, 1
			row.Cost, row.Effort, row.Elapsed = pointer("0"), effort(0), pointer("00:00:00")
		}
		expected = append(expected, row)
	}
	privateLabel, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Private outside label", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	labels = append(labels, privateLabel)
	// Deliberately bypass scoped HTTP admission through model-level storage.
	// Both an empty label and an already-used label retain private links.
	link(privateTicket.ID, labels[0].ID)
	link(privateTicket.ID, labels[1].ID)
	link(tickets[0].ID, privateLabel.ID)
	report, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(tickets[0].ID, "Retained label report"))
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
			return errors.New("label usage attempted audit")
		})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		return client, backend, audits
	}
	decode := func(client *helpdeskClient, suffix string) labelUsageEnvelope {
		response := client.request(http.MethodGet, "/api/labels/usage/"+suffix, "", false)
		var page labelUsageEnvelope
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("label usage response", response.Code, response.Body.String())
		}
		var raw struct{ Results []map[string]json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		for _, row := range raw.Results {
			for _, field := range []string{"id", "name", "total", "open", "closed", "expected_cost_total", "effort_average", "elapsed_total"} {
				if _, exists := row[field]; !exists {
					t.Fatal("label usage omitted required nullable output", field)
				}
			}
		}
		return page
	}
	t.Run("rows_filters_pages_and_category_isolation", func(t *testing.T) {
		for _, usage := range []string{"all", "used", "unused"} {
			filtered := make([]labelUsageItem, 0)
			for _, row := range expected {
				if usage == "all" || usage == "used" && row.Total > 0 || usage == "unused" && row.Total == 0 {
					filtered = append(filtered, row)
				}
			}
			for _, number := range []int{1, 2, 3, 50001} {
				client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
				page := decode(client, fmt.Sprintf("?usage=%s&p=%d", usage, number))
				start := min(len(filtered), (number-1)*20)
				end := min(len(filtered), start+20)
				if page.Category.ID != category.ID || page.Category.Name != category.Name || page.Page != int64(number) || page.PageSize != 20 || page.Usage != usage || page.Total != int64(len(filtered)) || page.Results == nil || !reflect.DeepEqual(page.Results, filtered[start:end]) {
					t.Fatal("label usage differs", usage, number, page, filtered[start:end])
				}
				if backend.snapshots != 1 || backend.queries != 2 || backend.groups != 1 || backend.rowsets != 1 || backend.closed != 1 || *audits != 0 {
					t.Fatal("label usage snapshot/count ownership", backend)
				}
			}
		}
	})
	t.Run("html_escape_and_filter_links", func(t *testing.T) {
		client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
		response := client.request(http.MethodGet, helpdesk.LabelUsagePath+"?usage=used", "", false)
		body := response.Body.String()
		for _, required := range []string{"Label usage", "Labels &lt;script&gt;&amp; category", "Label &lt;script&gt;&amp; 00", "value=\"used\" selected", "p=2&amp;usage=used", "1000000000000.01", "00:00:00"} {
			if !strings.Contains(body, required) {
				t.Fatal("label usage missing content", required, body)
			}
		}
		if response.Code != http.StatusOK || strings.Contains(body, "<script>") || strings.Contains(body, "Private outside") || strings.Contains(body, "csrfmiddlewaretoken") || backend.snapshots != 1 || *audits != 0 {
			t.Fatal("label usage HTML escaped scope or wrote", response.Code)
		}
		unused := client.request(http.MethodGet, helpdesk.LabelUsagePath+"?usage=unused", "", false)
		if unused.Code != http.StatusOK || !strings.Contains(unused.Body.String(), "Not set") || strings.Contains(unused.Body.String(), "Next page") {
			t.Fatal("empty relation display or page link", unused.Body.String())
		}
	})
	t.Run("admission_before_query_or_lookup", func(t *testing.T) {
		for _, path := range []string{helpdesk.LabelUsagePath, "/api/labels/usage/"} {
			for _, permission := range []auth.Permission{helpdesk.ViewLabel, helpdesk.ViewTicket} {
				client, backend, _ := newClient(category.ID, "", helpdeskDeniedPermissions{permission})
				if response := client.request(http.MethodGet, path+"?usage=bad", "", false); response.Code != http.StatusForbidden || backend.snapshots != 0 {
					t.Fatal("label usage missing permission reached parsing or data", permission, path, response.Code)
				}
				if permission == helpdesk.ViewTicket {
					if response := client.request(http.MethodGet, "/api/labels/", "", false); response.Code != http.StatusOK {
						t.Fatal("label listing acquired ticket-view permission", response.Code)
					}
				}
				client.cookies = map[string]*http.Cookie{}
				want := http.StatusFound
				if strings.HasPrefix(path, "/api/") {
					want = http.StatusForbidden
				}
				if response := client.request(http.MethodGet, path+"?usage=bad", "", false); response.Code != want || backend.snapshots != 0 {
					t.Fatal("anonymous label usage reached data", response.Code)
				}
			}
			client, backend, _ := newClient(category.ID, "", auth.PrincipalAuthorizer{})
			for _, suffix := range []string{"?p=0", "?p=50002", "?p=01", "?p=+1", "?p=", "?p=1&p=2", "?usage=", "?usage=Used", "?usage=unknown", "?usage=used&usage=used", "?usage=%00", "?usage=%FF", "?usage=%", "?category=1", "?p=1;usage=used", "?" + strings.Repeat("x", 129)} {
				if response := client.request(http.MethodGet, path+suffix, "", false); response.Code != http.StatusBadRequest || backend.snapshots != 0 {
					t.Fatal("invalid label usage reached data", path, suffix, response.Code)
				}
			}
		}
	})
	t.Run("empty_and_missing_category", func(t *testing.T) {
		empty, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Empty label usage"))
		if err != nil {
			t.Fatal(err)
		}
		client, _, _ := newClient(empty.ID, "", auth.PrincipalAuthorizer{})
		page := decode(client, "")
		if page.Total != 0 || len(page.Results) != 0 || page.Results == nil || page.Usage != "all" {
			t.Fatal("empty label usage defaults", page)
		}
		for _, path := range []string{helpdesk.LabelUsagePath, "/api/labels/usage/"} {
			client, backend, _ := newClient(math.MaxInt64, "", auth.PrincipalAuthorizer{})
			if response := client.request(http.MethodGet, path, "", false); response.Code != http.StatusNotFound || backend.groups != 0 {
				t.Fatal("missing label category queried labels", response.Code)
			}
		}
	})
	for _, mode := range []string{"missing_callback", "nil_reader", "typed_nil_reader", "double_callback", "concurrent_callback", "swallowed_error", "cleanup_failure", "post_cleanup_cancel", "query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure", "late_callback", "unfinished_callback"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{helpdesk.LabelUsagePath, "/api/labels/usage/"} {
				client, backend, audits := newClient(category.ID, mode, auth.PrincipalAuthorizer{})
				work, cancel := context.WithCancel(ctx)
				defer cancel()
				backend.cancel = cancel
				backend.started, backend.release, backend.finished = make(chan struct{}), make(chan struct{}), make(chan error, 1)
				response := client.requestContext(work, http.MethodGet, path, "", false)
				if mode == "late_callback" {
					calls := backend.queries
					if backend.late == nil || backend.late() == nil || backend.queries != calls {
						t.Fatal("late snapshot callback reached data")
					}
				}
				if mode == "unfinished_callback" {
					close(backend.release)
					if err := <-backend.finished; err == nil {
						t.Fatal("unfinished snapshot callback escaped cancellation")
					}
				}
				if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private summary") || strings.Contains(response.Body.String(), "total_labels") || strings.Contains(response.Body.String(), "Label &lt;script&gt;") || *audits != 0 {
					t.Fatal("failed label usage published output", mode, response.Code, response.Body.String())
				}
				if slices.Contains([]string{"scan_failure", "iteration_failure", "close_failure"}, mode) && backend.closed != 1 {
					t.Fatal("label usage cursor leaked", mode, backend.closed)
				}
			}
		})
	}
	t.Run("nonfinite_numeric_output_failure", func(t *testing.T) {
		outside, err := models.TicketObjects.Patch(ctx, runtime, tickets[0], models.TicketPatch{}.WithEffort(math.Inf(1)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, err := models.TicketObjects.Patch(ctx, runtime, outside, models.TicketPatch{}.WithEffort(*tickets[0].Effort))
			if err != nil {
				t.Fatal(err)
			}
		}()
		for _, path := range []string{helpdesk.LabelUsagePath, "/api/labels/usage/"} {
			client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
			response := client.request(http.MethodGet, path, "", false)
			if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "total_labels") || strings.Contains(response.Body.String(), "Label &lt;script&gt;") || backend.closed != 1 || *audits != 0 {
				t.Fatal("nonfinite label usage published output", path, response.Code)
			}
			stored, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(outside.ID)).Get(ctx)
			if err != nil || !reflect.DeepEqual(stored, outside) {
				t.Fatal("failed label usage changed the nonfinite stored value", err)
			}
		}
	})
	ticketKeys := make([]int64, len(tickets))
	for i, row := range tickets {
		ticketKeys[i] = row.ID
	}
	actualTickets, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.In(ticketKeys...)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
	if err != nil || !reflect.DeepEqual(actualTickets, tickets) {
		t.Fatal("label usage changed tickets or repaired payload digest", err)
	}
	labelKeys := make([]int64, len(labels))
	for i, row := range labels {
		labelKeys[i] = row.ID
	}
	actualLabels, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.In(labelKeys...)).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil || !reflect.DeepEqual(actualLabels, labels) {
		t.Fatal("label usage changed labels", err)
	}
	actualLinks, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.LabelID.In(labelKeys...)).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx)
	if err != nil || !reflect.DeepEqual(actualLinks, links) {
		t.Fatal("label usage changed links", err)
	}
	actualReport, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.Exact(report.ID)).Get(ctx)
	if err != nil || !reflect.DeepEqual(actualReport, report) {
		t.Fatal("label usage changed reports", err)
	}
	for _, check := range []struct {
		model string
		keys  []int64
	}{{"helpdesk.ticket", ticketKeys}, {"helpdesk.label", labelKeys}, {"helpdesk.service_report", []int64{report.ID}}} {
		for _, key := range check.keys {
			history, err := runtime.AuditHistory(ctx, check.model, key, 10)
			if err != nil || len(history) != 0 {
				t.Fatal("label usage appended audit", check.model, err)
			}
		}
	}
}
