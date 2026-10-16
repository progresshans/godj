package main

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskDeadlineCalendar(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, tickets []hs.Ticket) error {
	reference := time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600))
	from := time.Date(2024, 2, 28, 23, 59, 59, 0, time.FixedZone("date keeps local components", 14*3600))
	until := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	for _, params := range []hs.HelpdeskDeadlineCalendarParams{
		{AsOf: reference}, {AsOf: time.Time{}}, {AsOf: time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)},
		{AsOf: reference, FromDate: hs.NewOptDate(from), UntilDate: hs.NewOptDate(until)},
		{AsOf: reference, FromDate: hs.NewOptDate(until), UntilDate: hs.NewOptDate(until)},
		{AsOf: reference, FromDate: hs.NewOptDate(from)}, {AsOf: reference, UntilDate: hs.NewOptDate(until)},
		{AsOf: reference, FromDate: hs.NewOptDate(time.Date(9000, 1, 1, 0, 0, 0, 0, time.UTC)), UntilDate: hs.NewOptDate(time.Date(9000, 12, 31, 0, 0, 0, 0, time.UTC))},
	} {
		want := expectedDeadlineCalendar(params, tickets)
		for _, viewer := range []struct {
			client    *hs.Client
			transport *observedTransport
			state     *sessionState
		}{{client, transport, state}, {readOnly, readOnlyTransport, readOnlyState}} {
			pages := []int64{1, 50001}
			for number := int64(2); number <= int64(len(want)/20)+2; number++ {
				pages = append(pages, number)
			}
			for _, number := range pages {
				request := params
				if number != 1 {
					request.P = hs.NewOptInt64(number)
				}
				before := viewer.transport.requestCount()
				response, err := viewer.client.HelpdeskDeadlineCalendar(ctx, request)
				page, ok := response.(*hs.DeadlineCalendarHeaders)
				if err != nil || !ok || viewer.transport.requestCount() != before+1 || viewer.transport.lastStatus() != 200 || !page.XGodjCsrftoken.Set || !viewer.state.ready(page.XGodjCsrftoken.Value) {
					return fail("calendar generated view-only admission, one request and CSRF headers")
				}
				value := page.Response
				start := min(len(want), int(number-1)*20)
				end := min(len(want), start+20)
				if value.Category.ID != category || value.Category.Name != "Hardware & repairs" || value.Page != number || value.PageSize != 20 || !value.AsOf.Equal(params.AsOf.UTC().Truncate(time.Microsecond)) || value.AsOf.Location() != time.UTC || value.TotalGroups != int64(len(want)) || len(value.Results) != end-start || !calendarBoundMatches(value.FromDate, params.FromDate) || !calendarBoundMatches(value.UntilDate, params.UntilDate) {
					return fail("calendar generated date range, UTC reference and total/page envelope")
				}
				for index, want := range want[start:end] {
					got := value.Results[index]
					if got.Date.Null != want.Date.Null || !want.Date.Null && !got.Date.Value.Equal(want.Date.Value) || got.Total != want.Total || got.Open != want.Open || got.Overdue != want.Overdue || got.FirstDueTime != want.FirstDueTime || got.LastDueTime != want.LastDueTime {
						return fail("calendar independent grouped counts, nullable date and exact clocks")
					}
				}
			}
		}
	}
	for _, params := range []hs.HelpdeskDeadlineCalendarParams{
		{AsOf: reference, P: hs.NewOptInt64(0)}, {AsOf: reference, P: hs.NewOptInt64(50002)},
		{AsOf: reference, FromDate: hs.NewOptDate(until), UntilDate: hs.NewOptDate(from)},
		{AsOf: reference, FromDate: hs.NewOptDate(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC))},
		{AsOf: reference, UntilDate: hs.NewOptDate(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))},
		{AsOf: time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("outside UTC domain", 3600))},
	} {
		before := transport.requestCount()
		response, err := client.HelpdeskDeadlineCalendar(ctx, params)
		value, ok := response.(*hs.HelpdeskDeadlineCalendarBadRequest)
		if err != nil || !ok || transport.requestCount() != before+1 || transport.lastStatus() != http.StatusBadRequest || value.Response.Code != "validation_error" {
			return fail("calendar generated invalid range/date/reference rejection")
		}
	}
	return requireHelpdeskTickets(ctx, client, transport, state, tickets...)
}

func calendarBoundMatches(actual hs.NilDate, request hs.OptDate) bool {
	return actual.Null == !request.Set && (!request.Set || actual.Value.Format("2006-01-02") == request.Value.Format("2006-01-02"))
}

// Group retained Ticket responses with the standard library. No framework
// imports, ORM helpers or calendar endpoint responses enter this expectation.
func expectedDeadlineCalendar(params hs.HelpdeskDeadlineCalendarParams, tickets []hs.Ticket) []hs.DeadlineCalendarResultsItem {
	grouped := map[string]hs.DeadlineCalendarResultsItem{}
	for _, ticket := range tickets {
		key := "~"
		var day time.Time
		clock := ""
		if !ticket.DueAt.Null {
			at := ticket.DueAt.Value.UTC()
			key = at.Format("2006-01-02")
			day = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
			clock = strings.TrimSuffix(at.Format("15:04:05.000000"), ".000000")
		}
		if params.FromDate.Set && (ticket.DueAt.Null || key < params.FromDate.Value.Format("2006-01-02")) || params.UntilDate.Set && (ticket.DueAt.Null || key > params.UntilDate.Value.Format("2006-01-02")) {
			continue
		}
		row, exists := grouped[key]
		if !exists {
			row.Date = hs.NilDate{Null: true}
			row.FirstDueTime = hs.NilString{Null: true}
			row.LastDueTime = hs.NilString{Null: true}
			if !ticket.DueAt.Null {
				row.Date = hs.NewNilDate(day)
				row.FirstDueTime = hs.NewNilString(clock)
				row.LastDueTime = hs.NewNilString(clock)
			}
		}
		row.Total++
		if !ticket.Closed {
			row.Open++
			if !ticket.DueAt.Null && ticket.DueAt.Value.Before(params.AsOf.UTC().Truncate(time.Microsecond)) {
				row.Overdue++
			}
		}
		if !ticket.DueAt.Null {
			if clock < row.FirstDueTime.Value {
				row.FirstDueTime = hs.NewNilString(clock)
			}
			if clock > row.LastDueTime.Value {
				row.LastDueTime = hs.NewNilString(clock)
			}
		}
		grouped[key] = row
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]hs.DeadlineCalendarResultsItem, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, grouped[key])
	}
	return rows
}
