package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

type servicePreviewItem struct {
	ID       int64
	Subject  string
	Day      *string `json:"service_on"`
	Clock    *string `json:"service_at"`
	Elapsed  *string `json:"elapsed"`
	Due      *string `json:"due_at"`
	Start    *string `json:"service_start"`
	Adjusted *string `json:"adjusted_start"`
	Finish   *string `json:"finish_at"`
	Slack    *string `json:"deadline_slack"`
}
type servicePreviewEnvelope struct {
	Category struct {
		ID   int64
		Name string
	}
	Page     int64
	PageSize int64 `json:"page_size"`
	Shift    string
	Minimum  *string `json:"min_start"`
	Total    int64   `json:"total_tickets"`
	Results  []servicePreviewItem
}

// Construct complete UTC instants with the standard library, and add signed
// microseconds as integers. No expected value uses GoDj's expression compiler.
func expectedServicePreview(tickets []models.Ticket, shift int64, minimum *time.Time) []servicePreviewItem {
	rows := make([]servicePreviewItem, 0, len(tickets))
	starts := map[int64]int64{}
	for _, ticket := range tickets {
		row := servicePreviewItem{ID: ticket.ID, Subject: ticket.Subject}
		if ticket.ServiceOn != nil {
			row.Day = new(fmt.Sprintf("%04d-%02d-%02d", ticket.ServiceOn.Year, ticket.ServiceOn.Month, ticket.ServiceOn.Day))
		}
		if ticket.ServiceAt != nil {
			row.Clock = new(strings.TrimSuffix(fmt.Sprintf("%02d:%02d:%02d.%06d", ticket.ServiceAt.Hour, ticket.ServiceAt.Minute, ticket.ServiceAt.Second, ticket.ServiceAt.Microsecond), ".000000"))
		}
		var elapsed int64
		if ticket.Elapsed != nil {
			elapsed = int64(ticket.Elapsed.Days)*86400000000 + ticket.Elapsed.Microseconds
			row.Elapsed = new(deadlinePeriod(elapsed))
		}
		if ticket.DueAt != nil {
			row.Due = new(ticket.DueAt.UTC().Format("2006-01-02T15:04:05.000000Z"))
		}
		if ticket.ServiceOn != nil && ticket.ServiceAt != nil {
			d, w := ticket.ServiceOn, ticket.ServiceAt
			start := time.Date(d.Year, d.Month, d.Day, w.Hour, w.Minute, w.Second, w.Microsecond*1000, time.UTC)
			adjusted := time.UnixMicro(start.UnixMicro() + shift).UTC()
			if minimum != nil && adjusted.Before(*minimum) {
				continue
			}
			starts[row.ID] = adjusted.UnixMicro()
			row.Start, row.Adjusted = new(start.Format("2006-01-02T15:04:05.000000Z")), new(adjusted.Format("2006-01-02T15:04:05.000000Z"))
			if ticket.Elapsed != nil {
				finish := time.UnixMicro(adjusted.UnixMicro() + elapsed).UTC()
				row.Finish = new(finish.Format("2006-01-02T15:04:05.000000Z"))
				if ticket.DueAt != nil {
					row.Slack = new(deadlinePeriod(ticket.DueAt.UnixMicro() - finish.UnixMicro()))
				}
			}
		} else if minimum != nil {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if (left.Adjusted == nil) != (right.Adjusted == nil) {
			return left.Adjusted != nil
		}
		if left.Adjusted != nil && starts[left.ID] != starts[right.ID] {
			return starts[left.ID] < starts[right.ID]
		}
		return left.ID < right.ID
	})
	return rows
}

func verifyHelpdeskServicePreview(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient, native deadlineNative) {
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Service <script>& category"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Outside service schedules"))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Empty service schedules"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := jsonvalue.Parse([]byte(`{"retain":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	var tickets []models.Ticket
	for index := range 45 {
		input := models.NewTicketCreate(fmt.Sprintf("Service <script>& %02d", index), category.ID).WithClosed(index%2 == 0).WithDetails("Keep this ticket").WithExternalPayload(payload)
		if index%5 != 0 {
			day := calendar.Date{Year: 2024, Month: 2, Day: 29}
			if index == 1 {
				day = calendar.Date{Year: 2, Month: 1, Day: 2}
			}
			if index == 2 {
				day = calendar.Date{Year: 9998, Month: 12, Day: 30}
			}
			input = input.WithServiceOn(day)
		}
		if index%7 != 0 {
			microsecond := index
			if index == 6 {
				microsecond = 0
			}
			input = input.WithServiceAt(clock.Time{Microsecond: microsecond})
		}
		if index%11 != 0 {
			input = input.WithElapsed(duration.FromMicroseconds(int64(index) - 6))
		}
		if index%3 != 0 {
			input = input.WithDueAt(time.Date(2024, 3, 1, 0, 0, 0, 1000, time.UTC))
		}
		row, err := models.TicketObjects.Create(ctx, runtime, input)
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, row)
	}
	private, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private outside service", outside.ID).WithServiceOn(calendar.Date{Year: 2000, Month: 1, Day: 1}).WithServiceAt(clock.Time{}).WithElapsed(duration.Duration{}))
	if err != nil {
		t.Fatal(err)
	}
	label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Retained service label", category.ID))
	if err != nil {
		t.Fatal(err)
	}
	link, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(tickets[0].ID, label.ID))
	if err != nil {
		t.Fatal(err)
	}
	report, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(tickets[0].ID, "Retained service report").WithCompleted(true))
	if err != nil {
		t.Fatal(err)
	}
	before := native.Snapshot(t)
	newClient := func(t *testing.T, categoryID int64, mode string, authorizer auth.Authorizer) (*helpdeskClient, *summaryBackend, *int) {
		t.Helper()
		backend := &summaryBackend{Backend: runtime, mode: mode}
		application, err := helpdesk.New(backend, categoryID)
		if err != nil {
			t.Fatal(err)
		}
		audits := new(int)
		client := helpdeskHTTP(t, application, runtime, authorizer, func(context.Context, db.Session, admin.PreparedEvent) error {
			*audits++
			return errors.New("service preview attempted audit")
		})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		return client, backend, audits
	}
	queryText := func(page int, shift string, minimum *time.Time) string {
		values := url.Values{"p": {strconv.Itoa(page)}, "shift": {shift}}
		if minimum != nil {
			values.Set("min_start", minimum.Format(time.RFC3339Nano))
		}
		return "?" + values.Encode()
	}
	decode := func(t *testing.T, client *helpdeskClient, suffix string) servicePreviewEnvelope {
		t.Helper()
		response := client.request(http.MethodGet, "/api/tickets/service-preview/"+suffix, "", false)
		var page servicePreviewEnvelope
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("service preview response", response.Code, response.Body.String())
		}
		var raw struct{ Results []map[string]json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		for _, row := range raw.Results {
			if len(row) != 10 {
				t.Fatal("service preview changed its closed row", row)
			}
			for _, field := range []string{"id", "subject", "service_on", "service_at", "elapsed", "due_at", "service_start", "adjusted_start", "finish_at", "deadline_slack"} {
				if _, ok := row[field]; !ok {
					t.Fatal("service preview omitted a required nullable field", field)
				}
			}
		}
		return page
	}
	t.Run("exact_rows_nulls_filters_pages_and_category", func(t *testing.T) {
		minimum := time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600))
		future := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
		for _, input := range []struct {
			shift   int64
			minimum *time.Time
		}{{0, nil}, {1, nil}, {-1, nil}, {86400000000, &minimum}, {0, &minimum}, {0, &future}} {
			var normalized *time.Time
			var minText *string
			if input.minimum != nil {
				value := input.minimum.UTC().Truncate(time.Microsecond)
				normalized = &value
				minText = new(value.Format("2006-01-02T15:04:05.000000Z"))
			}
			want := expectedServicePreview(tickets, input.shift, normalized)
			for _, pageNumber := range []int{1, 2, 3, 50001} {
				client, backend, audits := newClient(t, category.ID, "", helpdeskDeniedPermissions{helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.DeleteTicket, helpdesk.ViewCategory, helpdesk.ViewLabel, helpdesk.ViewServiceReport})
				page := decode(t, client, queryText(pageNumber, deadlinePeriod(input.shift), input.minimum))
				start := min(len(want), (pageNumber-1)*20)
				end := min(len(want), start+20)
				if page.Category.ID != category.ID || page.Category.Name != category.Name || page.Page != int64(pageNumber) || page.PageSize != 20 || page.Shift != deadlinePeriod(input.shift) || !reflect.DeepEqual(page.Minimum, minText) || page.Total != int64(len(want)) || page.Results == nil || !reflect.DeepEqual(page.Results, want[start:end]) {
					t.Fatal("service arithmetic, nulls, category or page differ", input, page, want[start:end])
				}
				if backend.snapshots != 1 || backend.queries != 2 || backend.groups != 1 || backend.rowsets != 1 || backend.closed != 1 || *audits != 0 {
					t.Fatal("service preview changed snapshot or cursor ownership", backend)
				}
			}
		}
	})
	t.Run("html_defaults_escaping_and_links", func(t *testing.T) {
		client, backend, audits := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
		entry := client.request(http.MethodGet, helpdesk.ServicePreviewPath, "", false)
		if entry.Code != 200 || backend.snapshots != 1 || !strings.Contains(entry.Body.String(), `name="shift" value="00:00:00"`) || !strings.Contains(entry.Body.String(), "45 tickets") {
			t.Fatal("service entry did not use explicit defaults", entry.Code, entry.Body.String())
		}
		minimum := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
		response := client.request(http.MethodGet, helpdesk.ServicePreviewPath+queryText(1, "00:00:00.000001", &minimum), "", false)
		next := url.Values{"p": {"2"}, "shift": {"00:00:00.000001"}, "min_start": {"0001-01-01T00:00:00.000000Z"}}
		for _, text := range []string{"Service &lt;script&gt;&amp; category", "Service &lt;script&gt;&amp;", strings.ReplaceAll(next.Encode(), "&", "&amp;"), "Clear earliest start", "Service time (UTC)"} {
			if !strings.Contains(response.Body.String(), text) {
				t.Fatal("service HTML lost escaping, UTC meaning or query inputs", text, response.Body.String())
			}
		}
		if response.Code != 200 || strings.Contains(response.Body.String(), "<script>") || strings.Contains(response.Body.String(), `method="post"`) || *audits != 0 {
			t.Fatal("unsafe service preview HTML")
		}
		last := client.request(http.MethodGet, helpdesk.ServicePreviewPath+queryText(3, "00:00:00", nil), "", false)
		if last.Code != 200 || !strings.Contains(last.Body.String(), "Not set") || strings.Contains(last.Body.String(), ">Next page<") {
			t.Fatal("service final page lost unset values", last.Body.String())
		}
	})
	t.Run("admission_and_validation_before_lookup", func(t *testing.T) {
		for _, path := range []string{helpdesk.ServicePreviewPath, "/api/tickets/service-preview/"} {
			client, backend, _ := newClient(t, category.ID, "", helpdeskDeniedPermissions{helpdesk.ViewTicket})
			if response := client.request(http.MethodGet, path+"?min_start=bad", "", false); response.Code != 403 || backend.snapshots != 0 {
				t.Fatal("denied service request reached parsing", response.Code)
			}
			client.cookies = map[string]*http.Cookie{}
			want := 302
			if strings.HasPrefix(path, "/api/") {
				want = 403
			}
			if response := client.request(http.MethodGet, path+"?min_start=bad", "", false); response.Code != want || backend.snapshots != 0 {
				t.Fatal("anonymous service request reached data", response.Code)
			}
			client, backend, _ = newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
			for _, suffix := range []string{"?p=0", "?p=01", "?p=50002", "?shift=", "?shift=PT1S", "?shift=00:00:00.000000", "?shift=1000000000%2000:00:00", "?shift=00:00:00&shift=00:00:00", "?min_start=", "?min_start=2024-02-29", "?min_start=2024-02-29T00:00:00", "?min_start=2024-02-29T09:00:00+09:00", "?min_start=2023-02-29T00:00:00Z", "?min_start=0000-01-01T00:00:00Z", "?min_start=%FF", "?min_start=%", "?min_start=2024-02-29T00:00:00Z&min_start=2024-02-29T00:00:00Z", "?category=1", "?as_of=2024-01-01T00:00:00Z", "?" + strings.Repeat("x", 513)} {
				if response := client.request(http.MethodGet, path+suffix, "", false); response.Code != 400 || backend.snapshots != 0 {
					t.Fatal("invalid service input reached data", path, suffix, response.Code)
				}
			}
		}
	})
	t.Run("empty_and_missing_category", func(t *testing.T) {
		client, _, _ := newClient(t, empty.ID, "", auth.PrincipalAuthorizer{})
		page := decode(t, client, "")
		if page.Total != 0 || page.Results == nil || len(page.Results) != 0 || page.Shift != "00:00:00" || page.Minimum != nil {
			t.Fatal("empty service defaults differ", page)
		}
		for _, path := range []string{helpdesk.ServicePreviewPath, "/api/tickets/service-preview/"} {
			client, backend, _ := newClient(t, math.MaxInt64, "", auth.PrincipalAuthorizer{})
			if response := client.request(http.MethodGet, path, "", false); response.Code != 404 || backend.groups != 0 {
				t.Fatal("missing service category queried tickets", response.Code)
			}
		}
	})
	t.Run("native_corruption_ranges_and_null_operands", func(t *testing.T) {
		original := tickets[19] // Present date, clock, elapsed and deadline, within the first page.
		if original.ServiceOn == nil || original.ServiceAt == nil || original.Elapsed == nil || original.DueAt == nil {
			t.Fatal("incomplete native service corruption fixture")
		}
		restore := func() {
			work, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := errors.Join(native.setServiceValues(work, original.ID, original.ServiceOn, original.ServiceAt, original.Elapsed), native.setDueAt(work, original.ID, *original.DueAt)); err != nil {
				t.Error(err)
			}
		}
		defer restore()
		assertFailedPage := func(shift string, pageNumber int) {
			stored := native.Snapshot(t)
			for _, path := range []string{helpdesk.ServicePreviewPath, "/api/tickets/service-preview/"} {
				client, _, audits := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
				response := client.request(http.MethodGet, path+queryText(pageNumber, shift, nil), "", false)
				if response.Code != 500 || strings.Contains(response.Body.String(), "results") || strings.Contains(response.Body.String(), "Service &lt;script&gt;") || strings.Contains(response.Body.String(), "godj_invalid") || *audits != 0 || native.Snapshot(t) != stored {
					t.Fatal("native service failure disclosed a prefix or changed storage", response.Code, response.Body.String())
				}
			}
		}
		assertFailed := func(shift string) { assertFailedPage(shift, 1) }
		for _, field := range []string{"service_on", "service_at", "elapsed"} {
			for _, missing := range []bool{false, true} {
				restore()
				if missing {
					d, w, e := original.ServiceOn, original.ServiceAt, original.Elapsed
					if field == "service_on" {
						w = nil
					} else {
						d = nil
					}
					if err := native.setServiceValues(ctx, original.ID, d, w, e); err != nil {
						t.Fatal(err)
					}
				}
				if err := native.corruptServiceValue(ctx, original.ID, field); err != nil {
					t.Fatal(err)
				}
				// Missing date sorts last. Include that page so the corrupted
				// elapsed operand is actually evaluated rather than unselected.
				if missing && field == "elapsed" {
					stored := native.Snapshot(t)
					for _, path := range []string{helpdesk.ServicePreviewPath, "/api/tickets/service-preview/"} {
						client, _, _ := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
						response := client.request(http.MethodGet, path+queryText(2, "00:00:00", nil), "", false)
						if response.Code != 500 || strings.Contains(response.Body.String(), "results") || native.Snapshot(t) != stored {
							t.Fatal("NULL hid an evaluated corrupt elapsed operand", response.Code, response.Body.String())
						}
					}
				} else {
					assertFailed("00:00:00")
				}
			}
		}
		restore()
		if err := native.corruptDueAt(ctx, original.ID); err != nil {
			t.Fatal(err)
		}
		assertFailed("00:00:00")
		restore()
		if err := native.setServiceValues(ctx, original.ID, new(calendar.Date{Year: 9999, Month: 12, Day: 31}), new(clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}), original.Elapsed); err != nil {
			t.Fatal(err)
		}
		// The finish fails when the future row is selected; adjusted_start
		// participates in ordering for every page.
		assertFailedPage("00:00:00", 2)
		assertFailed("00:00:00.000001")
		restore()
		assertFailed("999999999 23:59:59.999999")
		restore()
		if err := native.corruptServiceValue(ctx, private.ID, "service_at"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := native.setServiceValues(ctx, private.ID, private.ServiceOn, private.ServiceAt, private.Elapsed); err != nil {
				t.Error(err)
			}
		}()
		stored := native.Snapshot(t)
		client, _, _ := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
		if page := decode(t, client, ""); page.Total != 45 || native.Snapshot(t) != stored {
			t.Fatal("foreign Category corruption entered the service preview")
		}
		if err := native.setServiceValues(ctx, private.ID, private.ServiceOn, private.ServiceAt, private.Elapsed); err != nil {
			t.Fatal(err)
		}
	})
	for _, mode := range []string{"missing_callback", "nil_reader", "typed_nil_reader", "double_callback", "concurrent_callback", "swallowed_error", "cleanup_failure", "post_cleanup_cancel", "query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure", "late_callback", "unfinished_callback"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{helpdesk.ServicePreviewPath, "/api/tickets/service-preview/"} {
				client, backend, audits := newClient(t, category.ID, mode, auth.PrincipalAuthorizer{})
				work, cancel := context.WithCancel(ctx)
				defer cancel()
				backend.cancel = cancel
				backend.started, backend.release, backend.finished = make(chan struct{}), make(chan struct{}), make(chan error, 1)
				response := client.requestContext(work, http.MethodGet, path, "", false)
				if mode == "late_callback" {
					calls := backend.queries
					if backend.late == nil || backend.late() == nil || backend.queries != calls {
						t.Fatal("late service callback accessed data")
					}
				}
				if mode == "unfinished_callback" {
					close(backend.release)
					if err := <-backend.finished; err == nil {
						t.Fatal("unfinished service snapshot escaped cancellation")
					}
				}
				if response.Code != 500 || strings.Contains(response.Body.String(), "private summary") || strings.Contains(response.Body.String(), "total_tickets") || strings.Contains(response.Body.String(), "Service &lt;script&gt;") || *audits != 0 {
					t.Fatal("failed service snapshot published output", mode, response.Code, response.Body.String())
				}
				if slices.Contains([]string{"scan_failure", "iteration_failure", "close_failure"}, mode) && backend.closed != 1 {
					t.Fatal("service cursor leaked", mode, backend.closed)
				}
			}
		})
	}
	t.Run("stored_models_relations_and_audit_unchanged", func(t *testing.T) {
		all := append(append([]models.Ticket{}, tickets...), private)
		keys := make([]int64, len(all))
		for index, row := range all {
			keys[index] = row.ID
		}
		actual, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.In(keys...)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
		if err != nil || !reflect.DeepEqual(actual, all) {
			t.Fatal("service preview changed stored tickets", err)
		}
		storedLabel, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(label.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLabel, label) {
			t.Fatal("service preview changed its label", err)
		}
		storedLink, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(link.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLink, link) {
			t.Fatal("service preview changed its relation", err)
		}
		storedReport, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.Exact(report.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedReport, report) {
			t.Fatal("service preview changed its report", err)
		}
		for _, item := range []struct {
			model string
			keys  []int64
		}{{"helpdesk.ticket", keys}, {"helpdesk.label", []int64{label.ID}}, {"helpdesk.service_report", []int64{report.ID}}} {
			for _, id := range item.keys {
				history, err := runtime.AuditHistory(ctx, item.model, id, 10)
				if err != nil || len(history) != 0 {
					t.Fatal("service preview appended audit", item.model, err)
				}
			}
		}
		if native.Snapshot(t) != before {
			t.Fatal("service preview changed storage classes or unselected data")
		}
	})
}

func TestServicePreviewRequiresUsableAuthentication(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, authentication := range []*sessionauth.Runtime{nil, {}} {
		if _, err := application.ServicePreview(authentication); err == nil {
			t.Fatal("service preview accepted unusable authentication")
		}
	}
}

func assertServicePreviewContracts(t *testing.T, document helpdeskDocument) {
	t.Helper()
	operation := document.Paths["/api/tickets/service-preview/"]["get"]
	schema := document.Components.Schemas["ServicePreview"]
	if operation.Responses["200"].Content["application/json"].Schema.Ref != "#/components/schemas/ServicePreview" || len(operation.Parameters) != 3 || schema.AdditionalProperties || !slices.Equal(schema.Required, []string{"category", "page", "page_size", "shift", "min_start", "total_tickets", "results"}) {
		t.Fatal("service preview lost its closed page contract")
	}
	for index, name := range []string{"p", "shift", "min_start"} {
		parameter := operation.Parameters[index]
		if parameter.Name != name || parameter.Required || parameter.AllowEmptyValue {
			t.Fatal("service query parameter presence differs", name)
		}
	}
	minimum := operation.Parameters[2].Schema
	if minimum.Format != "date-time" || minimum.TimeFormat != time.RFC3339Nano || minimum.InputOffset != "required" || minimum.MinLength != 20 || minimum.MaxLength != 64 || minimum.MaxBytes != 64 || minimum.Pattern == "" || len(minimum.Default) != 0 {
		t.Fatal("service minimum lost offset or microsecond contract")
	}
	shift := operation.Parameters[1].Schema
	if shift.Type != "string" || shift.DurationPolicy != "normalized-days-and-microseconds" || shift.Pattern == "" || string(shift.Default) != `"00:00:00"` || string(operation.Parameters[0].Schema.Default) != "1" {
		t.Fatal("service shift default or domain differs")
	}
	rows := schema.Properties["results"]
	fields := []string{"id", "subject", "service_on", "service_at", "elapsed", "due_at", "service_start", "adjusted_start", "finish_at", "deadline_slack"}
	if rows.Type != "array" || rows.MaxItems != 20 || rows.Items == nil || rows.Items.AdditionalProperties || !slices.Equal(rows.Items.Required, fields) {
		t.Fatal("service output lost bounded required fields")
	}
	for _, name := range fields[2:] {
		field := rows.Items.Properties[name]
		if len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || !field.allowsType("null") {
			t.Fatal("service output lost explicit nullable domain", name)
		}
		if slices.Contains([]string{"due_at", "service_start", "adjusted_start", "finish_at"}, name) && (field.AnyOf[0].Format != "date-time" || field.AnyOf[0].TimeFormat != time.RFC3339Nano) {
			t.Fatal("service output lost UTC instant precision", name)
		}
	}
	if rows.Items.Properties["service_on"].AnyOf[0].Format != "date" || rows.Items.Properties["service_at"].AnyOf[0].Pattern == "" || !schema.Properties["min_start"].allowsType("null") {
		t.Fatal("service response conflated calendar domains")
	}
}
