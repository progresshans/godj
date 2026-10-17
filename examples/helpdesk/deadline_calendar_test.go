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

type deadlineCalendarItem struct {
	Start                *string `json:"period_start"`
	Total, Open, Overdue int64
	First                *string `json:"first_due_at"`
	Last                 *string `json:"last_due_at"`
}
type deadlineCalendarEnvelope struct {
	Category struct {
		ID   int64
		Name string
	}
	Page     int64
	PageSize int64  `json:"page_size"`
	AsOf     string `json:"as_of"`
	Period   string
	From     *string `json:"from_date"`
	Until    *string `json:"until_date"`
	Total    int64   `json:"total_groups"`
	Results  []deadlineCalendarItem
}

// Group only the known synthetic daily inputs with the standard library.
// This expected result never calls GoDj's AST, compiler or period helper.
func expectedDeadlinePeriods(t *testing.T, daily []deadlineCalendarItem, period string) []deadlineCalendarItem {
	t.Helper()
	groups := map[string]deadlineCalendarItem{}
	for _, row := range daily {
		key := ""
		if row.Start != nil {
			day, err := time.Parse("2006-01-02", *row.Start)
			if err != nil {
				t.Fatal(err)
			}
			switch period {
			case "week":
				day = day.AddDate(0, 0, -int((day.Weekday()+6)%7))
			case "month":
				day = time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
			case "quarter":
				day = time.Date(day.Year(), (day.Month()-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC)
			case "year":
				day = time.Date(day.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
			default:
				t.Fatal("unknown synthetic period", period)
			}
			key = day.Format("2006-01-02")
		}
		group := groups[key]
		if key != "" {
			group.Start = new(key)
		}
		group.Total += row.Total
		group.Open += row.Open
		group.Overdue += row.Overdue
		if row.First != nil && (group.First == nil || *row.First < *group.First) {
			group.First = row.First
		}
		if row.Last != nil && (group.Last == nil || *row.Last > *group.Last) {
			group.Last = row.Last
		}
		groups[key] = group
	}
	rows := slices.Collect(maps.Values(groups))
	slices.SortFunc(rows, func(left, right deadlineCalendarItem) int {
		if left.Start == nil && right.Start == nil {
			return 0
		}
		if left.Start == nil {
			return 1
		}
		if right.Start == nil {
			return -1
		}
		return strings.Compare(*left.Start, *right.Start)
	})
	return rows
}

func verifyHelpdeskDeadlineCalendar(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient, native deadlineNative) {
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Calendar <script>& category"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Outside calendar"))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Empty calendar"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := jsonvalue.Parse([]byte(`{"retain":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	reference := time.Date(2024, 2, 29, 12, 34, 56, 1000, time.UTC)
	const referenceInput = "2024-02-29T21:34:56.0000019+09:00"
	var tickets []models.Ticket
	expected := make([]deadlineCalendarItem, 0, 67)
	// Three known clock values per day give independent grouped counts. The
	// second open deadline equals as_of on leap day; it must not be overdue.
	for index := range 42 {
		date := time.Date(2024, 2, 1+index-1, 0, 0, 0, 0, time.UTC)
		if index == 0 {
			date = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		if index == 41 {
			date = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
		}
		day := date.Format("2006-01-02")
		first, last := day+"T00:00:00.000000Z", day+"T23:59:59.999999Z"
		row := deadlineCalendarItem{Start: &day, Total: 3, Open: 2, First: &first, Last: &last}
		for position, clock := range []struct{ h, m, s, ns int }{{0, 0, 0, 0}, {12, 34, 56, 1000}, {23, 59, 59, 999999000}} {
			at := time.Date(date.Year(), date.Month(), date.Day(), clock.h, clock.m, clock.s, clock.ns, time.UTC)
			if position < 2 && at.Before(reference) {
				row.Overdue++
			}
			value, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate(fmt.Sprintf("Calendar %02d %d", index, position), category.ID).WithDueAt(at.In(time.FixedZone("input", 9*3600))).WithClosed(position == 2).WithDetails("Retained calendar details").WithElapsed(duration.FromMicroseconds(int64(index+position))).WithExternalPayload(payload))
			if err != nil {
				t.Fatal(err)
			}
			tickets = append(tickets, value)
		}
		expected = append(expected, row)
	}
	// Each coarser period must have more than one nonempty page, including
	// years. These extra instants are independent of the leap-day daily data.
	for year := 2000; year < 2024; year++ {
		at := time.Date(year, time.July, 15, 6, 7, 8, 1000, time.UTC)
		closed := year%3 == 0
		value, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate(fmt.Sprintf("Calendar annual %d", year), category.ID).WithDueAt(at).WithClosed(closed))
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, value)
		day, instant := at.Format("2006-01-02"), at.Format("2006-01-02T15:04:05.000000Z")
		row := deadlineCalendarItem{Start: &day, Total: 1, First: &instant, Last: &instant}
		if !closed {
			row.Open, row.Overdue = 1, 1
		}
		expected = append(expected, row)
	}
	slices.SortFunc(expected, func(left, right deadlineCalendarItem) int { return strings.Compare(*left.Start, *right.Start) })
	for index := range 3 {
		value, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate(fmt.Sprintf("Unset calendar %d", index), category.ID).WithClosed(index == 2))
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, value)
	}
	expected = append(expected, deadlineCalendarItem{Total: 3, Open: 2})
	private, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private calendar ticket", outside.ID).WithDueAt(reference))
	if err != nil {
		t.Fatal(err)
	}
	label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Retained calendar label", category.ID))
	if err != nil {
		t.Fatal(err)
	}
	link, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(tickets[0].ID, label.ID))
	if err != nil {
		t.Fatal(err)
	}
	report, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(tickets[0].ID, "Retained calendar report").WithCompleted(true))
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
			return errors.New("calendar attempted audit")
		})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		return client, backend, audits
	}
	queryText := func(number int, from, until *string) string {
		values := url.Values{"p": {strconv.Itoa(number)}, "as_of": {referenceInput}}
		if from != nil {
			values.Set("from_date", *from)
		}
		if until != nil {
			values.Set("until_date", *until)
		}
		return "?" + values.Encode()
	}
	decode := func(t *testing.T, client *helpdeskClient, suffix string) deadlineCalendarEnvelope {
		t.Helper()
		response := client.request(http.MethodGet, "/api/tickets/deadline-calendar/"+suffix, "", false)
		var page deadlineCalendarEnvelope
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("calendar response", response.Code, response.Body.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"category", "page", "page_size", "as_of", "period", "from_date", "until_date", "total_groups", "results"} {
			if _, ok := raw[name]; !ok {
				t.Fatal("calendar omitted response field", name)
			}
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(raw["results"], &rows); err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			for _, name := range []string{"period_start", "total", "open", "overdue", "first_due_at", "last_due_at"} {
				if _, ok := row[name]; !ok {
					t.Fatal("calendar omitted required nullable output", name)
				}
			}
		}
		return page
	}
	t.Run("exact_groups_ranges_pages_and_category", func(t *testing.T) {
		for _, bounds := range [][2]*string{{nil, nil}, {new("2024-02-15"), new("2024-03-10")}, {new("2024-02-29"), new("2024-02-29")}, {new("0001-01-01"), nil}, {nil, new("2024-02-01")}, {new("2025-01-01"), new("2025-12-31")}} {
			want := make([]deadlineCalendarItem, 0, len(expected))
			for _, row := range expected {
				if bounds[0] != nil && (row.Start == nil || *row.Start < *bounds[0]) || bounds[1] != nil && (row.Start == nil || *row.Start > *bounds[1]) {
					continue
				}
				want = append(want, row)
			}
			for _, number := range []int{1, 2, 3, 4, 50001} {
				client, backend, audits := newClient(t, category.ID, "", helpdeskDeniedPermissions{helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.DeleteTicket, helpdesk.ViewCategory, helpdesk.ViewLabel, helpdesk.ViewServiceReport})
				page := decode(t, client, queryText(number, bounds[0], bounds[1]))
				start := min(len(want), (number-1)*20)
				end := min(len(want), start+20)
				if page.Category.ID != category.ID || page.Category.Name != category.Name || page.Page != int64(number) || page.PageSize != 20 || page.Period != "day" || page.AsOf != "2024-02-29T12:34:56.000001Z" || !reflect.DeepEqual(page.From, bounds[0]) || !reflect.DeepEqual(page.Until, bounds[1]) || page.Total != int64(len(want)) || page.Results == nil || !reflect.DeepEqual(page.Results, want[start:end]) {
					t.Fatal("calendar values/counts/range/order/page differ", number, page, want[start:end])
				}
				if backend.snapshots != 1 || backend.queries != 2 || backend.groups != 1 || backend.rowsets != 1 || backend.closed != 1 || *audits != 0 {
					t.Fatal("calendar snapshot/page lifetime changed", backend)
				}
			}
		}
	})
	t.Run("period_groups_ranges_pages_and_category", func(t *testing.T) {
		for _, period := range []string{"week", "month", "quarter", "year"} {
			groups := expectedDeadlinePeriods(t, expected, period)
			if len(groups) <= 20 {
				t.Fatal("coarse period fixture lost its second nonempty page", period)
			}
			if period == "month" {
				var february *deadlineCalendarItem
				for index := range groups {
					if groups[index].Start != nil && *groups[index].Start == "2024-02-01" {
						february = &groups[index]
					}
				}
				if february == nil || february.Total != 87 || february.Open != 58 || february.Overdue != 57 || february.First == nil || february.Last == nil || *february.First != "2024-02-01T00:00:00.000000Z" || *february.Last != "2024-02-29T23:59:59.999999Z" {
					t.Fatal("independent leap-month expectation changed", february)
				}
			}
			for _, bounds := range [][2]*string{{nil, nil}, {new("2024-01-01"), new("2024-03-10")}, {new("2024-02-01"), new("2024-02-15")}, {new("0001-01-01"), nil}, {nil, new("2024-02-01")}, {new("9999-01-01"), new("9999-12-31")}, {new("2025-01-01"), new("2025-12-31")}} {
				want := make([]deadlineCalendarItem, 0, len(groups))
				for _, row := range groups {
					if bounds[0] != nil && (row.Start == nil || *row.Start < *bounds[0]) || bounds[1] != nil && (row.Start == nil || *row.Start > *bounds[1]) {
						continue
					}
					want = append(want, row)
				}
				for _, number := range []int{1, 2, 50001} {
					client, backend, audits := newClient(t, category.ID, "", helpdeskDeniedPermissions{helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.DeleteTicket, helpdesk.ViewCategory, helpdesk.ViewLabel, helpdesk.ViewServiceReport})
					page := decode(t, client, queryText(number, bounds[0], bounds[1])+"&period="+period)
					start := min(len(want), (number-1)*20)
					end := min(len(want), start+20)
					if page.Category.ID != category.ID || page.Category.Name != category.Name || page.Period != period || page.Page != int64(number) || page.PageSize != 20 || page.AsOf != "2024-02-29T12:34:56.000001Z" || !reflect.DeepEqual(page.From, bounds[0]) || !reflect.DeepEqual(page.Until, bounds[1]) || page.Total != int64(len(want)) || page.Results == nil || !reflect.DeepEqual(page.Results, want[start:end]) {
						t.Fatal("period groups, complete deadlines or start bounds differ", period, number, page, want[start:end])
					}
					if backend.snapshots != 1 || backend.queries != 2 || backend.groups != 1 || backend.rowsets != 1 || backend.closed != 1 || *audits != 0 {
						t.Fatal("period grouping escaped its read snapshot or page lifetime", period, backend)
					}
				}
			}
		}
	})
	t.Run("explicit_reference_and_html_links", func(t *testing.T) {
		client, backend, audits := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
		entry := client.request(http.MethodGet, helpdesk.DeadlineCalendarPath, "", false)
		if entry.Code != 200 || backend.snapshots != 0 || !strings.Contains(entry.Body.String(), "Enter a reference time") || !strings.Contains(entry.Body.String(), `name="as_of" value=""`) {
			t.Fatal("calendar entry selected a clock or read data", entry.Code, entry.Body.String())
		}
		response := client.request(http.MethodGet, helpdesk.DeadlineCalendarPath+queryText(1, new("0001-01-01"), new("9999-12-31")), "", false)
		body := response.Body.String()
		next := url.Values{"p": {"2"}, "as_of": {"2024-02-29T12:34:56.000001Z"}, "from_date": {"0001-01-01"}, "until_date": {"9999-12-31"}, "period": {"day"}}
		for _, text := range []string{"Calendar &lt;script&gt;&amp; category", `name="as_of" value="2024-02-29T12:34:56.000001Z"`, `data-deadline-period="0001-01-01"`, strings.ReplaceAll(next.Encode(), "&", "&amp;"), "Clear date range"} {
			if !strings.Contains(body, text) {
				t.Fatal("calendar HTML lost escaping/reference/range", text, body)
			}
		}
		if response.Code != 200 || strings.Contains(body, "<script>") || strings.Contains(body, `method="post"`) || *audits != 0 {
			t.Fatal("calendar published unsafe/write HTML")
		}
		last := client.request(http.MethodGet, helpdesk.DeadlineCalendarPath+queryText(4, nil, nil), "", false)
		if last.Code != 200 || !strings.Contains(last.Body.String(), `data-deadline-period="unset"`) || !strings.Contains(last.Body.String(), "Not set") || strings.Contains(last.Body.String(), ">Next page<") {
			t.Fatal("calendar final page lost unset group", last.Body.String())
		}
		for _, period := range []string{"week", "month", "quarter", "year"} {
			for _, number := range []int{1, 2} {
				response := client.request(http.MethodGet, helpdesk.DeadlineCalendarPath+queryText(number, new("0001-01-01"), new("9999-12-31"))+"&period="+period, "", false)
				body := response.Body.String()
				pageNumber, label := "2", "Next page"
				if number == 2 {
					pageNumber, label = "1", "Previous page"
				}
				values := url.Values{"p": {pageNumber}, "as_of": {"2024-02-29T12:34:56.000001Z"}, "period": {period}, "from_date": {"0001-01-01"}, "until_date": {"9999-12-31"}}
				pageLink := `href="` + helpdesk.DeadlineCalendarPath + "?" + strings.ReplaceAll(values.Encode(), "&", "&amp;") + `">` + label + `</a>`
				clear := url.Values{"p": {"1"}, "as_of": {"2024-02-29T12:34:56.000001Z"}, "period": {period}}
				clearLink := `href="` + helpdesk.DeadlineCalendarPath + "?" + strings.ReplaceAll(clear.Encode(), "&", "&amp;") + `">Clear date range</a>`
				for _, text := range []string{`value="` + period + `" selected`, `type="hidden" name="period" value="` + period + `"`, `type="hidden" name="from_date" value="0001-01-01"`, `type="hidden" name="until_date" value="9999-12-31"`, pageLink, clearLink} {
					if response.Code != 200 || !strings.Contains(body, text) {
						t.Fatal("period HTML lost selection, range or navigation context", period, number, text, body)
					}
				}
			}
		}
	})
	t.Run("admission_and_input_before_lookup", func(t *testing.T) {
		for _, path := range []string{helpdesk.DeadlineCalendarPath, "/api/tickets/deadline-calendar/"} {
			client, backend, _ := newClient(t, category.ID, "", helpdeskDeniedPermissions{helpdesk.ViewTicket})
			if response := client.request(http.MethodGet, path+"?as_of=bad", "", false); response.Code != 403 || backend.snapshots != 0 {
				t.Fatal("calendar permission followed parsing", response.Code)
			}
			client.cookies = map[string]*http.Cookie{}
			status := http.StatusFound
			if strings.HasPrefix(path, "/api/") {
				status = http.StatusForbidden
			}
			if response := client.request(http.MethodGet, path+"?as_of=bad", "", false); response.Code != status || backend.snapshots != 0 {
				t.Fatal("anonymous calendar read data", response.Code)
			}
			client, backend, _ = newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
			for _, suffix := range []string{"?p=1", "?as_of=", "?as_of=2024-02-29", "?as_of=2024-02-29T00:00:00", "?as_of=%FF", "?as_of=%", "?as_of=2024-02-29T00:00:00Z&as_of=2024-02-29T00:00:00Z", "?category=1", "?" + strings.Repeat("x", 513), queryText(0, nil, nil), queryText(50002, nil, nil), queryText(1, new(""), nil), queryText(1, new("0000-01-01"), nil), queryText(1, new("2023-02-29"), nil), queryText(1, nil, new("2024-2-01")), queryText(1, new("2024-03-01"), new("2024-02-29")), queryText(1, new("2024-02-29T00:00:00Z"), nil), queryText(1, new("2024-02-29"), nil) + "&from_date=2024-02-29"} {
				if response := client.request(http.MethodGet, path+suffix, "", false); response.Code != 400 || backend.snapshots != 0 {
					t.Fatal("invalid calendar input read data", path, suffix, response.Code)
				}
			}
			for _, invalid := range []string{"", "Day", "hour", "week+", "%00", "%FF", "%", "day&period=week"} {
				if response := client.request(http.MethodGet, path+queryText(1, nil, nil)+"&period="+invalid, "", false); response.Code != 400 || backend.snapshots != 0 {
					t.Fatal("invalid period read data", path, invalid, response.Code)
				}
			}
			if strings.HasPrefix(path, "/api/") {
				if response := client.request(http.MethodGet, path, "", false); response.Code != 400 || backend.snapshots != 0 {
					t.Fatal("calendar API chose an implicit reference")
				}
			}
		}
	})
	t.Run("empty_and_missing_category", func(t *testing.T) {
		client, _, _ := newClient(t, empty.ID, "", auth.PrincipalAuthorizer{})
		page := decode(t, client, "?as_of="+url.QueryEscape(referenceInput))
		if page.Total != 0 || page.Results == nil || len(page.Results) != 0 || page.From != nil || page.Until != nil {
			t.Fatal("empty calendar defaults differ", page)
		}
		for _, path := range []string{helpdesk.DeadlineCalendarPath, "/api/tickets/deadline-calendar/"} {
			client, backend, _ := newClient(t, math.MaxInt64, "", auth.PrincipalAuthorizer{})
			if response := client.request(http.MethodGet, path+queryText(1, nil, nil), "", false); response.Code != 404 || backend.groups != 0 {
				t.Fatal("missing calendar category queried tickets", response.Code)
			}
		}
	})
	t.Run("native_corruption_and_source_scope", func(t *testing.T) {
		last := tickets[125]
		restore := func() {
			work, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := native.setDueAt(work, last.ID, *last.DueAt); err != nil {
				t.Error(err)
			}
			if err := native.setDueAt(work, private.ID, *private.DueAt); err != nil {
				t.Error(err)
			}
		}
		defer restore()
		if err := native.corruptDueAt(ctx, private.ID); err != nil {
			t.Fatal(err)
		}
		client, _, _ := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
		page := decode(t, client, queryText(1, nil, nil))
		if page.Total != int64(len(expected)) {
			t.Fatal("outside category corrupted calendar")
		}
		if err := native.corruptDueAt(ctx, last.ID); err != nil {
			t.Fatal(err)
		}
		stored := native.Snapshot(t)
		for _, path := range []string{helpdesk.DeadlineCalendarPath, "/api/tickets/deadline-calendar/"} {
			for _, suffix := range []string{queryText(1, nil, nil), queryText(1, new("0001-01-01"), new("0001-01-01")), queryText(1, nil, nil) + "&period=month", queryText(1, new("0001-01-01"), new("0001-01-01")) + "&period=year"} {
				client, _, audits := newClient(t, category.ID, "", auth.PrincipalAuthorizer{})
				response := client.request(http.MethodGet, path+suffix, "", false)
				if response.Code != 500 || strings.Contains(response.Body.String(), "results") || strings.Contains(response.Body.String(), "Calendar &lt;script&gt;") || strings.Contains(response.Body.String(), "godj_invalid") || *audits != 0 || native.Snapshot(t) != stored {
					t.Fatal("corrupt calendar input was hidden or partially published", response.Code, response.Body.String())
				}
			}
		}
		restore()
	})
	for _, mode := range []string{"missing_callback", "nil_reader", "typed_nil_reader", "double_callback", "concurrent_callback", "swallowed_error", "cleanup_failure", "post_cleanup_cancel", "query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure", "late_callback", "unfinished_callback"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{helpdesk.DeadlineCalendarPath, "/api/tickets/deadline-calendar/"} {
				client, backend, audits := newClient(t, category.ID, mode, auth.PrincipalAuthorizer{})
				work, cancel := context.WithCancel(ctx)
				defer cancel()
				backend.cancel = cancel
				backend.started, backend.release, backend.finished = make(chan struct{}), make(chan struct{}), make(chan error, 1)
				response := client.requestContext(work, http.MethodGet, path+queryText(1, nil, nil)+"&period=month", "", false)
				if mode == "late_callback" {
					calls := backend.queries
					if backend.late == nil || backend.late() == nil || backend.queries != calls {
						t.Fatal("late deadline callback read data")
					}
				}
				if mode == "unfinished_callback" {
					close(backend.release)
					if err := <-backend.finished; err == nil {
						t.Fatal("unfinished calendar snapshot escaped cancellation")
					}
				}
				if response.Code != 500 || strings.Contains(response.Body.String(), "private summary") || strings.Contains(response.Body.String(), "total_groups") || strings.Contains(response.Body.String(), "Calendar &lt;script&gt;") || *audits != 0 {
					t.Fatal("failed calendar snapshot published output", mode, response.Code, response.Body.String())
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
			t.Fatal("calendar reads changed any stored ticket column", err)
		}
		storedLabel, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(label.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLabel, label) {
			t.Fatal("calendar reads changed label", err)
		}
		storedLink, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(link.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedLink, link) {
			t.Fatal("calendar reads changed relation", err)
		}
		storedReport, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.Exact(report.ID)).Get(ctx)
		if err != nil || !reflect.DeepEqual(storedReport, report) {
			t.Fatal("calendar reads changed report", err)
		}
		for _, item := range []struct {
			model string
			keys  []int64
		}{{"helpdesk.ticket", keys}, {"helpdesk.label", []int64{label.ID}}, {"helpdesk.service_report", []int64{report.ID}}} {
			for _, id := range item.keys {
				history, err := runtime.AuditHistory(ctx, item.model, id, 10)
				if err != nil || len(history) != 0 {
					t.Fatal("calendar reads appended audit", item.model, err)
				}
			}
		}
		if native.Snapshot(t) != before {
			t.Fatal("calendar reads changed native storage classes or unselected columns")
		}
	})
}

func TestDeadlineCalendarRequiresUsableAuthentication(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, authentication := range []*sessionauth.Runtime{nil, {}} {
		if _, err := application.DeadlineCalendar(authentication); err == nil {
			t.Fatal("deadline calendar accepted unusable authentication")
		}
	}
}

func assertDeadlineCalendarContracts(t *testing.T, document helpdeskDocument) {
	t.Helper()
	operation := document.Paths["/api/tickets/deadline-calendar/"]["get"]
	schema := document.Components.Schemas["DeadlineCalendar"]
	if operation.Responses["200"].Content["application/json"].Schema.Ref != "#/components/schemas/DeadlineCalendar" || len(operation.Parameters) != 5 || schema.AdditionalProperties || !slices.Equal(schema.Required, []string{"category", "page", "page_size", "as_of", "period", "from_date", "until_date", "total_groups", "results"}) {
		t.Fatal("calendar closed page contract changed")
	}
	for index, name := range []string{"p", "as_of", "period", "from_date", "until_date"} {
		parameter := operation.Parameters[index]
		if parameter.Name != name || parameter.Required != (name == "as_of") || parameter.AllowEmptyValue {
			t.Fatal("calendar parameter presence differs", name)
		}
	}
	reference := operation.Parameters[1].Schema
	if reference.Format != "date-time" || reference.TimeFormat != time.RFC3339Nano || reference.InputOffset != "required" || reference.MaxBytes != 64 || len(reference.Default) != 0 {
		t.Fatal("calendar reference lost explicit offset/microseconds")
	}
	const choices = `["day","week","month","quarter","year"]`
	if string(operation.Parameters[2].Schema.Enum) != choices || string(operation.Parameters[2].Schema.Default) != `"day"` || string(schema.Properties["period"].Enum) != choices || schema.Properties["period"].Type != "string" {
		t.Fatal("period input/output choice domains differ")
	}
	for _, index := range []int{3, 4} {
		day := operation.Parameters[index].Schema
		if day.Type != "string" || day.Format != "date" || day.MinLength != 10 || day.MaxLength != 10 || day.MaxBytes != 10 || day.Pattern == "" || len(day.Default) != 0 {
			t.Fatal("calendar bound lost canonical Date meaning", index)
		}
	}
	if string(operation.Parameters[0].Schema.Default) != "1" {
		t.Fatal("calendar page default changed")
	}
	rows := schema.Properties["results"]
	if rows.Type != "array" || rows.MaxItems != 20 || rows.Items == nil || rows.Items.AdditionalProperties || !slices.Equal(rows.Items.Required, []string{"period_start", "total", "open", "overdue", "first_due_at", "last_due_at"}) {
		t.Fatal("calendar rows lost bounded required fields")
	}
	for _, name := range []string{"period_start", "first_due_at", "last_due_at"} {
		value := rows.Items.Properties[name]
		if len(value.AnyOf) != 2 || value.AnyOf[0].Type != "string" || !value.allowsType("null") {
			t.Fatal("calendar output lost required nullability", name)
		}
		if name == "period_start" && value.AnyOf[0].Format != "date" {
			t.Fatal("calendar Date domain missing")
		}
		if name != "period_start" && value.AnyOf[0].Format != "date-time" {
			t.Fatal("complete deadline lost its DateTime domain", name)
		}
	}
	for _, name := range []string{"from_date", "until_date"} {
		if !schema.Properties[name].allowsType("null") {
			t.Fatal("calendar nullable range context missing", name)
		}
	}
}
