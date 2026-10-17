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
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

type deadlinePreviewItem struct {
	ID        int64
	Subject   string
	Original  *string `json:"due_at"`
	Adjusted  *string `json:"adjusted_due_at"`
	Remaining *string
}

type deadlinePreviewEnvelope struct {
	Category struct {
		ID   int64
		Name string
	}
	Page     int64
	PageSize int64  `json:"page_size"`
	AsOf     string `json:"as_of"`
	Shift    string
	Minimum  *string `json:"min_remaining"`
	Total    int64   `json:"total_tickets"`
	Results  []deadlinePreviewItem
}

// The expected periods use integer day/clock decomposition, not GoDj's
// expression or Duration formatter, and do not pass through time.Duration.
func deadlinePeriod(microseconds int64) string {
	const day = int64(86400000000)
	days, tail := microseconds/day, microseconds%day
	if tail < 0 {
		days--
		tail += day
	}
	value := fmt.Sprintf("%02d:%02d:%02d", tail/3600000000, tail/60000000%60, tail/1000000%60)
	if fraction := tail % 1000000; fraction != 0 {
		value += fmt.Sprintf(".%06d", fraction)
	}
	if days != 0 {
		value = strconv.FormatInt(days, 10) + " " + value
	}
	return value
}

func verifyHelpdeskDeadlinePreview(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient, native deadlineNative) {
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Deadline <script>& category"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Private outside deadlines"))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Empty deadlines"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := jsonvalue.Parse([]byte(`{"retain":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2024, 2, 28, 23, 59, 59, 999999000, time.UTC)
	var tickets []models.Ticket
	for index := range 45 {
		input := models.NewTicketCreate(fmt.Sprintf("Deadline <script>& %02d", index), category.ID).WithClosed(index%2 == 0).WithDetails("Retained details").WithElapsed(duration.FromMicroseconds(int64(index) - 1)).WithExternalPayload(payload)
		if index%5 != 0 {
			at := time.UnixMicro(anchor.UnixMicro() + int64(index%7)*1000000).UTC()
			if index == 1 {
				at = time.Date(1, 1, 2, 0, 0, 0, 1000, time.UTC)
			}
			if index == 2 {
				at = time.Date(9999, 1, 1, 0, 0, 0, 1000, time.UTC)
			}
			input = input.WithDueAt(at)
		}
		row, err := models.TicketObjects.Create(ctx, runtime, input)
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, row)
	}
	private, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private outside ticket", outside.ID).WithDueAt(anchor))
	if err != nil {
		t.Fatal(err)
	}
	label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Retained deadline label", category.ID))
	if err != nil {
		t.Fatal(err)
	}
	link, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(tickets[0].ID, label.ID))
	if err != nil {
		t.Fatal(err)
	}
	report, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(tickets[0].ID, "Retained deadline report").WithCompleted(true))
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
			return errors.New("deadline preview attempted audit")
		})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		return client, backend, audits
	}
	const referenceInput = "2024-02-29T09:00:00.0000019+09:00"
	reference := time.Date(2024, 2, 29, 0, 0, 0, 1000, time.UTC)
	queryText := func(number int, shift string, minimum *string) string {
		values := url.Values{"p": {strconv.Itoa(number)}, "as_of": {referenceInput}, "shift": {shift}}
		if minimum != nil {
			values.Set("min_remaining", *minimum)
		}
		return "?" + values.Encode()
	}
	decode := func(t *testing.T, client *helpdeskClient, suffix string) deadlinePreviewEnvelope {
		t.Helper()
		response := client.request(http.MethodGet, "/api/tickets/deadline-preview/"+suffix, "", false)
		var page deadlinePreviewEnvelope
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("deadline preview response", response.Code, response.Body.String())
		}
		var raw struct{ Results []map[string]json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		for _, row := range raw.Results {
			for _, name := range []string{"id", "subject", "due_at", "adjusted_due_at", "remaining"} {
				if _, ok := row[name]; !ok {
					t.Fatal("deadline preview omitted required nullable output", name)
				}
			}
		}
		return page
	}
	expected := func(shift int64, minimum *int64) []deadlinePreviewItem {
		rows := make([]deadlinePreviewItem, 0, len(tickets))
		values := map[int64]int64{}
		for _, ticket := range tickets {
			row := deadlinePreviewItem{ID: ticket.ID, Subject: ticket.Subject}
			if ticket.DueAt != nil {
				adjusted := time.UnixMicro(ticket.DueAt.UnixMicro() + shift).UTC()
				remaining := adjusted.UnixMicro() - reference.UnixMicro()
				if minimum != nil && remaining < *minimum {
					continue
				}
				originalText, adjustedText, remainingText := ticket.DueAt.UTC().Format("2006-01-02T15:04:05.000000Z"), adjusted.Format("2006-01-02T15:04:05.000000Z"), deadlinePeriod(remaining)
				row.Original, row.Adjusted, row.Remaining = &originalText, &adjustedText, &remainingText
				values[row.ID] = remaining
			} else if minimum != nil {
				continue
			}
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool {
			left, right := rows[i], rows[j]
			if (left.Remaining == nil) != (right.Remaining == nil) {
				return left.Remaining != nil
			}
			if left.Remaining != nil && values[left.ID] != values[right.ID] {
				return values[left.ID] < values[right.ID]
			}
			return left.ID < right.ID
		})
		return rows
	}
	t.Run("exact_rows_filters_pages_and_category", func(t *testing.T) {
		zero, large := int64(0), int64(315537897599999999)
		for _, input := range []struct {
			shift   int64
			minimum *int64
		}{{0, nil}, {1, nil}, {-1, nil}, {86400000000, &zero}, {0, &large}} {
			want := expected(input.shift, input.minimum)
			var minimum *string
			if input.minimum != nil {
				value := deadlinePeriod(*input.minimum)
				minimum = &value
			}
			for _, number := range []int{1, 2, 3, 50001} {
				// A view-only authorization policy also denies every unrelated
				// Category/Label/Report or write permission used by this product.
				client, backend, audits := newClient(t, category.ID, "", helpdeskDeniedPermissions{helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.DeleteTicket, helpdesk.ViewCategory, helpdesk.ViewLabel, helpdesk.ViewServiceReport})
				page := decode(t, client, queryText(number, deadlinePeriod(input.shift), minimum))
				start := min(len(want), (number-1)*20)
				end := min(len(want), start+20)
				if page.Category.ID != category.ID || page.Category.Name != category.Name || page.Page != int64(number) || page.PageSize != 20 || page.AsOf != reference.Format("2006-01-02T15:04:05.000000Z") || page.Shift != deadlinePeriod(input.shift) || !reflect.DeepEqual(page.Minimum, minimum) || page.Total != int64(len(want)) || page.Results == nil || !reflect.DeepEqual(page.Results, want[start:end]) {
					t.Fatal("deadline values/order/page/total differ", input, number, page, want[start:end])
				}
				if backend.snapshots != 1 || backend.queries != 2 || backend.groups != 1 || backend.rowsets != 1 || backend.closed != 1 || *audits != 0 {
					t.Fatal("deadline snapshot/page lifetime changed", backend)
				}
			}
		}
	})
	t.Run("explicit_reference_and_html_links", func(t *testing.T) {
		client, backend, audits := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
		entry := client.request(http.MethodGet, helpdesk.DeadlinePreviewPath, "", false)
		if entry.Code != 200 || backend.snapshots != 0 || !strings.Contains(entry.Body.String(), "Enter a reference time") || !strings.Contains(entry.Body.String(), `name="as_of" value=""`) {
			t.Fatal("entry page implicitly chose a clock or read data", entry.Code, entry.Body.String())
		}
		negative := deadlinePeriod(-315537897599999999)
		response := client.request(http.MethodGet, helpdesk.DeadlinePreviewPath+queryText(1, deadlinePeriod(1), &negative), "", false)
		body := response.Body.String()
		next := url.Values{"as_of": {reference.Format("2006-01-02T15:04:05.000000Z")}, "shift": {deadlinePeriod(1)}, "min_remaining": {negative}, "p": {"2"}}
		for _, text := range []string{"Deadline &lt;script&gt;&amp; category", "Deadline &lt;script&gt;&amp;", `name="as_of" value="2024-02-29T00:00:00.000001Z"`, strings.ReplaceAll(next.Encode(), "&", "&amp;"), "Clear minimum"} {
			if !strings.Contains(body, text) {
				t.Fatal("deadline HTML lost escaping or request reference", text, body)
			}
		}
		if response.Code != 200 || strings.Contains(body, "<script>") || strings.Contains(body, `method="post"`) || *audits != 0 {
			t.Fatal("deadline page published unsafe or write content")
		}
		last := client.request(http.MethodGet, helpdesk.DeadlinePreviewPath+queryText(3, "00:00:00", nil), "", false)
		if last.Code != 200 || !strings.Contains(last.Body.String(), "Not set") || strings.Contains(last.Body.String(), ">Next page<") {
			t.Fatal("deadline final page or missing values differ", last.Body.String())
		}
	})
	t.Run("admission_and_input_before_lookup", func(t *testing.T) {
		for _, path := range []string{helpdesk.DeadlinePreviewPath, "/api/tickets/deadline-preview/"} {
			client, backend, _ := newClient(t, category.ID, "", helpdeskDeniedPermissions{helpdesk.ViewTicket})
			if response := client.request(http.MethodGet, path+"?as_of=bad", "", false); response.Code != 403 || backend.snapshots != 0 {
				t.Fatal("missing deadline permission reached parsing", response.Code)
			}
			client.cookies = map[string]*http.Cookie{}
			want := http.StatusFound
			if strings.HasPrefix(path, "/api/") {
				want = http.StatusForbidden
			}
			if response := client.request(http.MethodGet, path+"?as_of=bad", "", false); response.Code != want || backend.snapshots != 0 {
				t.Fatal("anonymous deadline preview reached data", response.Code)
			}
			client, backend, _ = newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
			for _, suffix := range []string{"?p=1", "?as_of=", "?as_of=2024-02-29", "?as_of=2024-02-29T00:00:00", "?as_of=2024-02-29T09:00:00+09:00", "?as_of=2023-02-29T00:00:00Z", "?as_of=0000-01-01T00:00:00Z", "?as_of=%FF", "?as_of=%", "?as_of=2024-02-29T00:00:00Z&as_of=2024-02-29T00:00:00Z", "?category=1", "?" + strings.Repeat("x", 513), queryText(0, "00:00:00", nil), queryText(50002, "00:00:00", nil), queryText(1, "PT1S", nil), queryText(1, "00:00:00.000000", nil), queryText(1, "1000000000 00:00:00", nil), queryText(1, "00:00:00", new(""))} {
				if response := client.request(http.MethodGet, path+suffix, "", false); response.Code != 400 || backend.snapshots != 0 {
					t.Fatal("invalid deadline input reached data", path, suffix, response.Code)
				}
			}
			if strings.HasPrefix(path, "/api/") {
				if response := client.request(http.MethodGet, path, "", false); response.Code != 400 || backend.snapshots != 0 {
					t.Fatal("API supplied an implicit reference time")
				}
			}
		}
	})
	t.Run("empty_and_missing_category", func(t *testing.T) {
		client, _, _ := newClient(t, empty.ID, "", auth.PrincipalAuthorizer{})
		page := decode(t, client, "?as_of="+url.QueryEscape(referenceInput))
		if page.Total != 0 || page.Results == nil || len(page.Results) != 0 || page.Shift != "00:00:00" || page.Minimum != nil {
			t.Fatal("empty deadline defaults differ", page)
		}
		for _, path := range []string{helpdesk.DeadlinePreviewPath, "/api/tickets/deadline-preview/"} {
			client, backend, _ := newClient(t, math.MaxInt64, "", auth.PrincipalAuthorizer{})
			if response := client.request(http.MethodGet, path+queryText(1, "00:00:00", nil), "", false); response.Code != 404 || backend.groups != 0 {
				t.Fatal("missing deadline category queried tickets", response.Code)
			}
		}
	})
	t.Run("native_range_and_corruption_preserve_storage", func(t *testing.T) {
		last := tickets[len(tickets)-1]
		if last.DueAt == nil {
			t.Fatal("late-row temporal fixture lacks its deadline")
		}
		restore := func() {
			work, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := native.setDueAt(work, last.ID, *last.DueAt); err != nil {
				t.Error(err)
			}
		}
		defer restore()
		for _, corrupt := range []bool{false, true} {
			if corrupt {
				err = native.corruptDueAt(ctx, last.ID)
			} else {
				err = native.setDueAt(ctx, last.ID, time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))
			}
			if err != nil {
				t.Fatal(err)
			}
			stored := native.Snapshot(t)
			for _, path := range []string{helpdesk.DeadlinePreviewPath, "/api/tickets/deadline-preview/"} {
				client, _, audits := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
				response := client.request(http.MethodGet, path+queryText(1, "00:00:00.000001", nil), "", false)
				if response.Code != 500 || strings.Contains(response.Body.String(), "results") || strings.Contains(response.Body.String(), "Deadline &lt;script&gt;") || strings.Contains(response.Body.String(), "godj_invalid") || *audits != 0 || native.Snapshot(t) != stored {
					t.Fatal("failed native deadline calculation published output or changed storage", response.Code, response.Body.String())
				}
			}
		}
		restore()
		stored := native.Snapshot(t)
		for _, path := range []string{helpdesk.DeadlinePreviewPath, "/api/tickets/deadline-preview/"} {
			client, _, _ := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
			response := client.request(http.MethodGet, path+queryText(1, "999999999 23:59:59.999999", nil), "", false)
			if response.Code != 500 || strings.Contains(response.Body.String(), "results") || native.Snapshot(t) != stored {
				t.Fatal("model-valid shift overflow was treated as parser success or a partial result", response.Code, response.Body.String())
			}
		}
	})
	for _, mode := range []string{"missing_callback", "nil_reader", "typed_nil_reader", "double_callback", "concurrent_callback", "swallowed_error", "cleanup_failure", "post_cleanup_cancel", "query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure", "late_callback", "unfinished_callback"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{helpdesk.DeadlinePreviewPath, "/api/tickets/deadline-preview/"} {
				client, backend, audits := newClient(t, category.ID, mode, auth.PrincipalAuthorizer{})
				work, cancel := context.WithCancel(ctx)
				defer cancel()
				backend.cancel = cancel
				backend.started, backend.release, backend.finished = make(chan struct{}), make(chan struct{}), make(chan error, 1)
				response := client.requestContext(work, http.MethodGet, path+queryText(1, "00:00:00", nil), "", false)
				if mode == "late_callback" {
					calls := backend.queries
					if backend.late == nil || backend.late() == nil || backend.queries != calls {
						t.Fatal("late deadline callback read data")
					}
				}
				if mode == "unfinished_callback" {
					close(backend.release)
					if err := <-backend.finished; err == nil {
						t.Fatal("unfinished deadline snapshot escaped cancellation")
					}
				}
				if response.Code != 500 || strings.Contains(response.Body.String(), "private summary") || strings.Contains(response.Body.String(), "total_tickets") || strings.Contains(response.Body.String(), "Deadline &lt;script&gt;") || *audits != 0 {
					t.Fatal("failed deadline snapshot published output", mode, response.Code, response.Body.String())
				}
				if slices.Contains([]string{"scan_failure", "iteration_failure", "close_failure"}, mode) && backend.closed != 1 {
					t.Fatal("deadline cursor leaked", mode, backend.closed)
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
			t.Fatal("deadline reads changed any stored ticket column", err)
		}
		storedLabel, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(label.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLabel, label) {
			t.Fatal("deadline reads changed label", err)
		}
		storedLink, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(link.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLink, link) {
			t.Fatal("deadline reads changed relation", err)
		}
		storedReport, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.Exact(report.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedReport, report) {
			t.Fatal("deadline reads changed report", err)
		}
		for _, item := range []struct {
			model string
			keys  []int64
		}{{"helpdesk.ticket", keys}, {"helpdesk.label", []int64{label.ID}}, {"helpdesk.service_report", []int64{report.ID}}} {
			for _, id := range item.keys {
				history, err := runtime.AuditHistory(ctx, item.model, id, 10)
				if err != nil || len(history) != 0 {
					t.Fatal("deadline reads appended audit", item.model, err)
				}
			}
		}
		if native.Snapshot(t) != before {
			t.Fatal("deadline reads changed native storage classes or unselected columns")
		}
	})
}

func TestDeadlinePreviewRequiresUsableAuthentication(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, authentication := range []*sessionauth.Runtime{nil, {}} {
		if _, err := application.DeadlinePreview(authentication); err == nil {
			t.Fatal("deadline preview accepted unusable authentication")
		}
	}
}

func assertDeadlinePreviewContracts(t *testing.T, document helpdeskDocument) {
	t.Helper()
	operation := document.Paths["/api/tickets/deadline-preview/"]["get"]
	schema := document.Components.Schemas["DeadlinePreview"]
	if operation.Responses["200"].Content["application/json"].Schema.Ref != "#/components/schemas/DeadlinePreview" || len(operation.Parameters) != 4 || schema.AdditionalProperties || !slices.Equal(schema.Required, []string{"category", "page", "page_size", "as_of", "shift", "min_remaining", "total_tickets", "results"}) {
		t.Fatal("deadline preview lost its closed page contract")
	}
	for index, name := range []string{"p", "as_of", "shift", "min_remaining"} {
		parameter := operation.Parameters[index]
		if parameter.Name != name || parameter.Required != (name == "as_of") || parameter.AllowEmptyValue {
			t.Fatal("deadline parameter presence differs", name)
		}
	}
	input := operation.Parameters[1].Schema
	if input.Format != "date-time" || input.TimeFormat != time.RFC3339Nano || input.InputOffset != "required" || input.MinLength != 20 || input.MaxLength != 64 || input.MaxBytes != 64 || input.Pattern == "" || len(input.Default) != 0 {
		t.Fatal("deadline reference time lost its offset, precision or required input")
	}
	for _, index := range []int{2, 3} {
		period := operation.Parameters[index].Schema
		if period.Type != "string" || period.DurationPolicy != "normalized-days-and-microseconds" || period.MinLength != 8 || period.MaxLength != 26 || period.Pattern == "" {
			t.Fatal("deadline Duration input lost canonical model meaning")
		}
	}
	if string(operation.Parameters[0].Schema.Default) != "1" || string(operation.Parameters[2].Schema.Default) != `"00:00:00"` || len(operation.Parameters[3].Schema.Default) != 0 {
		t.Fatal("deadline parameter omission differs")
	}
	rows := schema.Properties["results"]
	if rows.Type != "array" || rows.MaxItems != 20 || rows.Items == nil || rows.Items.AdditionalProperties || !slices.Equal(rows.Items.Required, []string{"id", "subject", "due_at", "adjusted_due_at", "remaining"}) {
		t.Fatal("deadline preview rows lost bounded required fields")
	}
	for _, name := range []string{"due_at", "adjusted_due_at", "remaining"} {
		value := rows.Items.Properties[name]
		if len(value.AnyOf) != 2 || value.AnyOf[0].Type != "string" || !value.allowsType("null") {
			t.Fatal("deadline preview lost nullable result", name)
		}
		if name != "remaining" && (value.AnyOf[0].Format != "date-time" || value.AnyOf[0].TimeFormat != time.RFC3339Nano) {
			t.Fatal("deadline output dropped client microseconds", name)
		}
	}
	if schema.Properties["as_of"].Format != "date-time" || !schema.Properties["min_remaining"].allowsType("null") {
		t.Fatal("deadline request context missing from response")
	}
}
