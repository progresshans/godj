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
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

// Civil dates below are independent literal expectations. Neither the pinned
// provider nor the product AST/compiler computes these expected group keys.
func verifyHelpdeskZonedDeadlineCalendar(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient, native deadlineNative) {
	t.Helper()
	for _, fixture := range []struct {
		zone     string
		instants [][2]string
	}{
		{"America/New_York", [][2]string{
			{"2024-03-10T04:59:59.999999Z", "2024-03-09"}, {"2024-03-10T05:00:00Z", "2024-03-10"},
			{"2024-03-10T07:00:00Z", "2024-03-10"}, {"2024-03-11T03:59:59.999999Z", "2024-03-10"}, {"2024-03-11T04:00:00Z", "2024-03-11"},
			{"2024-11-03T03:59:59.999999Z", "2024-11-02"}, {"2024-11-03T04:00:00Z", "2024-11-03"},
			{"2024-11-03T05:30:00Z", "2024-11-03"}, {"2024-11-03T06:30:00Z", "2024-11-03"},
			{"2024-11-04T04:59:59.999999Z", "2024-11-03"}, {"2024-11-04T05:00:00Z", "2024-11-04"},
		}},
		{"Asia/Seoul", [][2]string{
			{"0001-01-01T00:00:00Z", "0001-01-01"}, {"9999-12-31T00:00:00Z", "9999-12-31"},
			{"1899-12-31T15:32:07.999999Z", "1899-12-31"}, {"1899-12-31T15:32:08Z", "1900-01-01"},
			{"2023-12-31T14:59:59.999999Z", "2023-12-31"}, {"2023-12-31T15:00:00Z", "2024-01-01"},
			{"2024-02-29T14:59:59.999999Z", "2024-02-29"}, {"2024-02-29T15:00:00Z", "2024-03-01"},
		}},
		{"Australia/Lord_Howe", [][2]string{
			{"2024-04-06T12:59:59.999999Z", "2024-04-06"}, {"2024-04-06T13:00:00Z", "2024-04-07"},
			{"2024-04-07T13:29:59.999999Z", "2024-04-07"}, {"2024-04-07T13:30:00Z", "2024-04-08"},
			{"2024-10-05T13:29:59.999999Z", "2024-10-05"}, {"2024-10-05T13:30:00Z", "2024-10-06"},
			{"2024-10-06T12:59:59.999999Z", "2024-10-06"}, {"2024-10-06T13:00:00Z", "2024-10-07"},
		}},
		{"Pacific/Apia", [][2]string{
			{"2011-12-30T09:59:59.999999Z", "2011-12-29"}, {"2011-12-30T10:00:00Z", "2011-12-31"},
			{"2011-12-31T09:59:59.999999Z", "2011-12-31"}, {"2011-12-31T10:00:00Z", "2012-01-01"},
		}},
	} {
		t.Run(strings.ReplaceAll(fixture.zone, "/", "_"), func(t *testing.T) {
			originalStorage := native.Snapshot(t)
			category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Zoned calendar "+fixture.zone))
			if err != nil {
				t.Fatal(err)
			}
			outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Other zoned calendar"))
			if err != nil {
				t.Fatal(err)
			}
			var ownedTickets []int64
			t.Cleanup(func() {
				// Remove only this fixture after its storage assertions. Keeping
				// synthetic rows in the shared suite amplifies every later full
				// storage snapshot, especially under the race detector.
				work, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err := native.removeCalendarFixtures(work, [2]int64{category.ID, outside.ID}, ownedTickets); err != nil {
					t.Error(err)
					return
				}
				after, err := native.snapshot(work)
				if err != nil || after != originalStorage {
					t.Error("calendar fixture cleanup changed preexisting native storage", err)
				}
			})
			reference := time.Date(2024, 3, 10, 7, 0, 0, 0, time.UTC)
			inputs := slices.Clone(fixture.instants)
			// Every period, including year, has a second nonempty database page.
			for year := 2016; year < 2040; year++ {
				inputs = append(inputs, [2]string{fmt.Sprintf("%04d-07-15T06:00:00Z", year), fmt.Sprintf("%04d-07-15", year)})
			}
			daily := map[string]deadlineCalendarItem{}
			var tickets []models.Ticket
			for index, pair := range inputs {
				at, err := time.Parse(time.RFC3339Nano, pair[0])
				if err != nil {
					t.Fatal(err)
				}
				closed := index%3 == 0
				ticket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate(fmt.Sprintf("Zoned %s %d", fixture.zone, index), category.ID).WithDueAt(at).WithClosed(closed))
				if err != nil {
					t.Fatal(err)
				}
				tickets = append(tickets, ticket)
				ownedTickets = append(ownedTickets, ticket.ID)
				row := daily[pair[1]]
				row.Start, row.Total = new(pair[1]), row.Total+1
				instant := at.Format("2006-01-02T15:04:05.000000Z")
				if row.First == nil || instant < *row.First {
					row.First = new(instant)
				}
				if row.Last == nil || instant > *row.Last {
					row.Last = new(instant)
				}
				if !closed {
					row.Open++
					if at.Before(reference) {
						row.Overdue++
					}
				}
				daily[pair[1]] = row
			}
			unset, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Zoned unset", category.ID))
			if err != nil {
				t.Fatal(err)
			}
			tickets = append(tickets, unset)
			ownedTickets = append(ownedTickets, unset.ID)
			private, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Foreign zoned ticket", outside.ID).WithDueAt(reference))
			if err != nil {
				t.Fatal(err)
			}
			ownedTickets = append(ownedTickets, private.ID)
			days := slices.Collect(maps.Values(daily))
			slices.SortFunc(days, func(a, b deadlineCalendarItem) int { return strings.Compare(*a.Start, *b.Start) })
			days = append(days, deadlineCalendarItem{Total: 1, Open: 1})
			before := native.Snapshot(t)
			newClient := func(mode string, authorizer auth.Authorizer) (*helpdeskClient, *summaryBackend, *int) {
				backend := &summaryBackend{Backend: runtime, mode: mode}
				app, err := helpdesk.New(backend, category.ID)
				if err != nil {
					t.Fatal(err)
				}
				audits := new(int)
				client := helpdeskHTTP(t, app, runtime, authorizer, func(context.Context, db.Session, admin.PreparedEvent) error {
					*audits++
					return errors.New("zoned calendar attempted audit")
				})
				client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
				return client, backend, audits
			}
			values := func(period string, page int) url.Values {
				return url.Values{"as_of": {reference.Format(time.RFC3339Nano)}, "period": {period}, "time_zone": {fixture.zone}, "p": {strconv.Itoa(page)}}
			}
			client, backend, audits := newClient("", helpdeskDeniedPermissions{helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.DeleteTicket, helpdesk.ViewCategory})
			for _, period := range []string{"day", "week", "month", "quarter", "year"} {
				want := days
				if period != "day" {
					want = expectedDeadlinePeriods(t, days, period)
				}
				if len(want) <= 20 {
					t.Fatal("zoned fixture lost a second page", period)
				}
				for _, bounded := range []bool{false, true} {
					filtered := want
					if bounded {
						filtered = []deadlineCalendarItem{}
						for _, row := range want {
							if row.Start != nil && *row.Start >= "2024-01-01" && *row.Start <= "2024-12-31" {
								filtered = append(filtered, row)
							}
						}
					}
					for _, number := range []int{1, 2, 50001} {
						query := values(period, number)
						if bounded {
							query.Set("from_date", "2024-01-01")
							query.Set("until_date", "2024-12-31")
						}
						snapshots, queries, groups, closed := backend.snapshots, backend.queries, backend.groups, backend.closed
						response := client.request(http.MethodGet, "/api/tickets/deadline-calendar/?"+query.Encode(), "", false)
						var page deadlineCalendarEnvelope
						if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
							t.Fatal("zoned response", period, response.Code, response.Body.String())
						}
						start := min(len(filtered), (number-1)*20)
						end := min(len(filtered), start+20)
						if page.TimeZone != fixture.zone || page.Category.ID != category.ID || page.Period != period || page.AsOf != "2024-03-10T07:00:00.000000Z" || page.Page != int64(number) || page.PageSize != 20 || page.Total != int64(len(filtered)) || page.Results == nil || !reflect.DeepEqual(page.Results, filtered[start:end]) {
							t.Fatal("independent local dates, full UTC instants or DB page differ", period, query, page, filtered[start:end])
						}
						if backend.snapshots != snapshots+1 || backend.queries != queries+2 || backend.groups != groups+1 || backend.closed != closed+1 || *audits != 0 {
							t.Fatal("zoned calendar escaped one snapshot or cursor lifetime", backend)
						}
					}
				}
				query := values(period, 2)
				query.Set("from_date", "0001-01-01")
				query.Set("until_date", "9999-12-31")
				response := client.request(http.MethodGet, helpdesk.DeadlineCalendarPath+"?"+query.Encode(), "", false)
				query.Set("as_of", "2024-03-10T07:00:00.000000Z")
				query.Set("p", "1")
				previous := `href="` + helpdesk.DeadlineCalendarPath + "?" + strings.ReplaceAll(query.Encode(), "&", "&amp;") + `">Previous page</a>`
				query.Del("from_date")
				query.Del("until_date")
				clear := `href="` + helpdesk.DeadlineCalendarPath + "?" + strings.ReplaceAll(query.Encode(), "&", "&amp;") + `">Clear date range</a>`
				for _, text := range []string{`name="time_zone" value="` + fixture.zone + `"`, `type="hidden" name="time_zone" value="` + fixture.zone + `"`, previous, clear, fixture.zone + " deadline periods"} {
					if response.Code != 200 || !strings.Contains(response.Body.String(), text) {
						t.Fatal("zoned HTML lost selected context", text, response.Code, response.Body.String())
					}
				}
			}
			// Domain failure remains visible even when HAVING excludes that date.
			if fixture.zone == "America/New_York" || fixture.zone == "Asia/Seoul" {
				broken := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
				if fixture.zone == "Asia/Seoul" {
					broken = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
				}
				original := *tickets[0].DueAt
				restore := func() {
					work, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if err := native.setDueAt(work, tickets[0].ID, original); err != nil {
						t.Error(err)
					}
					if err := native.setDueAt(work, private.ID, reference); err != nil {
						t.Error(err)
					}
				}
				defer restore()
				if err := native.setDueAt(ctx, private.ID, broken); err != nil {
					t.Fatal(err)
				}
				query := values("month", 1)
				query.Set("from_date", "2024-01-01")
				query.Set("until_date", "2024-12-31")
				if response := client.request(http.MethodGet, "/api/tickets/deadline-calendar/?"+query.Encode(), "", false); response.Code != 200 {
					t.Fatal("foreign category local overflow leaked into the calendar", response.Code)
				}
				if err := native.setDueAt(ctx, tickets[0].ID, broken); err != nil {
					t.Fatal(err)
				}
				stored := native.Snapshot(t)
				for _, path := range []string{helpdesk.DeadlineCalendarPath, "/api/tickets/deadline-calendar/"} {
					response := client.request(http.MethodGet, path+"?"+query.Encode(), "", false)
					if response.Code != 500 || strings.Contains(response.Body.String(), "results") || strings.Contains(response.Body.String(), "Zoned calendar") || native.Snapshot(t) != stored || *audits != 0 {
						t.Fatal("local overflow was hidden or published a partial page", response.Code, response.Body.String())
					}
				}
				restore()
			}
			for _, path := range []string{helpdesk.DeadlineCalendarPath, "/api/tickets/deadline-calendar/"} {
				denied, deniedBackend, _ := newClient("", helpdeskDeniedPermissions{helpdesk.ViewTicket})
				if response := denied.request(http.MethodGet, path+"?as_of=bad&time_zone=unknown", "", false); response.Code != 403 || deniedBackend.snapshots != 0 {
					t.Fatal("timezone parsing preceded admission")
				}
				for _, mode := range []string{"query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure", "cleanup_failure"} {
					failed, backend, audits := newClient(mode, auth.PrincipalAuthorizer{})
					work, cancel := context.WithCancel(ctx)
					backend.cancel = cancel
					response := failed.requestContext(work, http.MethodGet, path+"?"+values("day", 1).Encode(), "", false)
					cancel()
					if response.Code != 500 || strings.Contains(response.Body.String(), "results") || strings.Contains(response.Body.String(), "Zoned calendar") || *audits != 0 {
						t.Fatal("zoned failure published output", mode, response.Code)
					}
					if slices.Contains([]string{"scan_failure", "iteration_failure", "close_failure"}, mode) && backend.closed != 1 {
						t.Fatal("zoned failure leaked cursor", mode)
					}
				}
			}
			if after := native.Snapshot(t); after != before {
				t.Fatal("zoned calendar changed native tables, relations or audit")
			}
		})
	}
}
