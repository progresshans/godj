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
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

type reportStatusItem struct {
	ID, Ticket int64
	Summary    string
	Completed  bool
	Closed     bool `json:"ticket_closed"`
	Matched    bool
}
type reportStatusEnvelope struct {
	Category struct {
		ID   int64
		Name string
	}
	Page       int64
	PageSize   int64 `json:"page_size"`
	Comparison string
	Total      int64 `json:"total_reports"`
	Results    []reportStatusItem
}

func TestServiceReportStatusRequiresUsableAuthentication(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, authentication := range []*sessionauth.Runtime{nil, {}} {
		if _, err := application.ServiceReportStatus(authentication); err == nil {
			t.Fatal("status page accepted unusable authentication")
		}
	}
}

func verifyHelpdeskServiceReportStatus(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Reports <script>& category"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Private outside reports"))
	if err != nil {
		t.Fatal(err)
	}
	outTicket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private outside ticket", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(outTicket.ID, "Private outside report")); err != nil {
		t.Fatal(err)
	}
	var tickets []models.Ticket
	var reports []models.ServiceReport
	var expected []reportStatusItem
	payload, err := jsonvalue.Parse([]byte(`{"retain":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 45; index++ {
		closed, completed := index%2 == 0, index%3 == 0
		ticket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Retained report ticket", category.ID).WithClosed(closed).WithExternalPayload(payload))
		if err != nil {
			t.Fatal(err)
		}
		report, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(ticket.ID, fmt.Sprintf("Report <script>& %d", index)).WithCompleted(completed))
		if err != nil {
			t.Fatal(err)
		}
		tickets, reports = append(tickets, ticket), append(reports, report)
		expected = append(expected, reportStatusItem{ID: report.ID, Ticket: ticket.ID, Summary: report.Summary, Completed: completed, Closed: closed, Matched: completed == closed})
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
			return errors.New("status read attempted an audit")
		})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		return client, backend, audits
	}
	decode := func(client *helpdeskClient, suffix string) reportStatusEnvelope {
		response := client.request(http.MethodGet, "/api/service-reports/status/"+suffix, "", false)
		var page reportStatusEnvelope
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("status response", response.Code, response.Body.String())
		}
		return page
	}
	t.Run("rows_filters_and_pages", func(t *testing.T) {
		for _, comparison := range []string{"all", "same", "different"} {
			filtered := make([]reportStatusItem, 0)
			for _, item := range expected {
				if comparison == "all" || (comparison == "same") == item.Matched {
					filtered = append(filtered, item)
				}
			}
			for _, pageNumber := range []int{1, 2, 3, 50001} {
				client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
				page := decode(client, fmt.Sprintf("?comparison=%s&p=%d", comparison, pageNumber))
				start := min(len(filtered), (pageNumber-1)*20)
				end := min(len(filtered), start+20)
				if page.Category.ID != category.ID || page.Category.Name != category.Name || page.Page != int64(pageNumber) || page.PageSize != 20 || page.Comparison != comparison || page.Total != int64(len(filtered)) || page.Results == nil || !slices.Equal(page.Results, filtered[start:end]) {
					t.Fatal("status scope, flags or page differs", comparison, pageNumber, page)
				}
				if backend.snapshots != 1 || backend.queries != 2 || backend.groups != 1 || backend.rowsets != 1 || backend.closed != 1 || *audits != 0 {
					t.Fatal("status page lost snapshot/count ownership", backend)
				}
			}
		}
	})
	t.Run("html_escape_and_filter_links", func(t *testing.T) {
		client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
		response := client.request(http.MethodGet, helpdesk.ServiceReportStatusPath+"?comparison=different", "", false)
		body := response.Body.String()
		for _, required := range []string{"Service report statuses", "Reports &lt;script&gt;&amp; category", "Report &lt;script&gt;&amp;", "value=\"different\" selected", "p=2&amp;comparison=different", "Incomplete", "Closed", "Different"} {
			if !strings.Contains(body, required) {
				t.Fatal("status page missing escaped content or filter link", required, body)
			}
		}
		if response.Code != http.StatusOK || strings.Contains(body, "<script>") || strings.Contains(body, "Private outside") || strings.Contains(body, "csrfmiddlewaretoken") || backend.snapshots != 1 || *audits != 0 {
			t.Fatal("status page escaped read-only category scope", response.Code)
		}
	})
	t.Run("admission_before_query_or_lookup", func(t *testing.T) {
		for _, path := range []string{helpdesk.ServiceReportStatusPath, "/api/service-reports/status/"} {
			for _, permission := range []auth.Permission{helpdesk.ViewServiceReport, helpdesk.ViewTicket} {
				client, backend, _ := newClient(category.ID, "", helpdeskDeniedPermissions{permission})
				if response := client.request(http.MethodGet, path+"?comparison=bad", "", false); response.Code != http.StatusForbidden || backend.snapshots != 0 {
					t.Fatal("missing permission reached parsing or data", permission, path, response.Code)
				}
				if permission == helpdesk.ViewTicket {
					if response := client.request(http.MethodGet, "/api/service-reports/", "", false); response.Code != http.StatusOK {
						t.Fatal("legacy report endpoint acquired ticket-view permission", response.Code)
					}
				}
				client.cookies = map[string]*http.Cookie{}
				response := client.request(http.MethodGet, path+"?comparison=bad", "", false)
				want := http.StatusFound
				if strings.HasPrefix(path, "/api/") {
					want = http.StatusForbidden
				}
				if response.Code != want || backend.snapshots != 0 {
					t.Fatal("anonymous status request reached parsing or data", response.Code)
				}
			}
			client, backend, _ := newClient(category.ID, "", auth.PrincipalAuthorizer{})
			for _, suffix := range []string{"?p=0", "?p=50002", "?p=01", "?p=+1", "?p=", "?p=1&p=2", "?comparison=", "?comparison=Same", "?comparison=unknown", "?comparison=same&comparison=same", "?comparison=%00", "?comparison=%FF", "?comparison=%", "?category=1", "?p=1;comparison=same", "?" + strings.Repeat("x", 129)} {
				if response := client.request(http.MethodGet, path+suffix, "", false); response.Code != http.StatusBadRequest || backend.snapshots != 0 {
					t.Fatal("invalid status query reached database", path, suffix, response.Code)
				}
			}
		}
	})
	t.Run("empty_and_missing_category", func(t *testing.T) {
		empty, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Empty report category"))
		if err != nil {
			t.Fatal(err)
		}
		client, _, _ := newClient(empty.ID, "", auth.PrincipalAuthorizer{})
		page := decode(client, "")
		if page.Total != 0 || len(page.Results) != 0 || page.Results == nil || page.Comparison != "all" {
			t.Fatal("empty status page lost defaults or empty results", page)
		}
		for _, path := range []string{helpdesk.ServiceReportStatusPath, "/api/service-reports/status/"} {
			client, backend, _ := newClient(math.MaxInt64, "", auth.PrincipalAuthorizer{})
			if response := client.request(http.MethodGet, path, "", false); response.Code != http.StatusNotFound || backend.groups != 0 {
				t.Fatal("missing status category queried reports", response.Code)
			}
		}
	})
	for _, mode := range []string{"missing_callback", "nil_reader", "typed_nil_reader", "double_callback", "concurrent_callback", "swallowed_error", "cleanup_failure", "post_cleanup_cancel", "query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{helpdesk.ServiceReportStatusPath, "/api/service-reports/status/"} {
				client, backend, audits := newClient(category.ID, mode, auth.PrincipalAuthorizer{})
				work, cancel := context.WithCancel(ctx)
				defer cancel()
				backend.cancel = cancel
				response := client.requestContext(work, http.MethodGet, path, "", false)
				if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private summary") || strings.Contains(response.Body.String(), "ticket_closed") || strings.Contains(response.Body.String(), "Report &lt;script&gt;") || *audits != 0 {
					t.Fatal("failed status read published output", mode, response.Code, response.Body.String())
				}
				if slices.Contains([]string{"scan_failure", "iteration_failure", "close_failure"}, mode) && backend.closed != 1 {
					t.Fatal("status failure leaked rows", mode, backend.closed)
				}
			}
		})
	}
	actualTickets, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.CategoryID.Exact(category.ID)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
	if err != nil || !reflect.DeepEqual(actualTickets, tickets) {
		t.Fatal("status read changed tickets or repaired payload digests", err)
	}
	keys := make([]int64, len(reports))
	for index, row := range reports {
		keys[index] = row.ID
	}
	actualReports, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.In(keys...)).OrderBy(models.ServiceReportFields.ID.Asc()).All(ctx)
	if err != nil || !reflect.DeepEqual(actualReports, reports) {
		t.Fatal("status read changed reports", err)
	}
	for _, row := range reports {
		history, err := runtime.AuditHistory(ctx, "helpdesk.service_report", row.ID, 10)
		if err != nil || len(history) != 0 {
			t.Fatal("status read appended report audit", err)
		}
	}
	for _, row := range tickets {
		history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", row.ID, 10)
		if err != nil || len(history) != 0 {
			t.Fatal("status read appended ticket audit", err)
		}
	}
}
