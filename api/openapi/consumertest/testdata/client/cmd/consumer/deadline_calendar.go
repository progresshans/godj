package main

import (
	"context"
	"net/http"
	"sort"
	"time"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskDeadlineCalendar(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, tickets []hs.Ticket) error {
	reference := time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600))
	from := time.Date(2024, 2, 28, 23, 59, 59, 0, time.FixedZone("date keeps local components", 14*3600))
	until := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	cases := []hs.HelpdeskDeadlineCalendarParams{
		{AsOf: reference}, {AsOf: time.Time{}}, {AsOf: time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)},
		{AsOf: reference, FromDate: hs.NewOptDate(from), UntilDate: hs.NewOptDate(until)},
		{AsOf: reference, FromDate: hs.NewOptDate(until), UntilDate: hs.NewOptDate(until)},
		{AsOf: reference, FromDate: hs.NewOptDate(from)}, {AsOf: reference, UntilDate: hs.NewOptDate(until)},
		{AsOf: reference, FromDate: hs.NewOptDate(time.Date(9000, 1, 1, 0, 0, 0, 0, time.UTC)), UntilDate: hs.NewOptDate(time.Date(9000, 12, 31, 0, 0, 0, 0, time.UTC))},
	}
	for _, period := range []hs.HelpdeskDeadlineCalendarPeriod{hs.HelpdeskDeadlineCalendarPeriodDay, hs.HelpdeskDeadlineCalendarPeriodWeek, hs.HelpdeskDeadlineCalendarPeriodMonth, hs.HelpdeskDeadlineCalendarPeriodQuarter, hs.HelpdeskDeadlineCalendarPeriodYear} {
		for _, bounds := range [][2]string{{"", ""}, {"0001-01-01", "9999-12-31"}, {"2024-01-01", "2024-03-01"}, {"2024-02-01", "2024-02-01"}, {"2024-02-28", "2024-02-29"}, {"9000-01-01", "9000-12-31"}, {"2024-02-01", ""}, {"", "2024-02-29"}} {
			params := hs.HelpdeskDeadlineCalendarParams{AsOf: reference, Period: hs.NewOptHelpdeskDeadlineCalendarPeriod(period)}
			if bounds[0] != "" {
				date, err := time.Parse("2006-01-02", bounds[0])
				if err != nil {
					return err
				}
				params.FromDate = hs.NewOptDate(date)
			}
			if bounds[1] != "" {
				date, err := time.Parse("2006-01-02", bounds[1])
				if err != nil {
					return err
				}
				params.UntilDate = hs.NewOptDate(date)
			}
			cases = append(cases, params)
		}
	}
	for _, params := range cases {
		want := expectedDeadlineCalendar(params, tickets, nil)
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
				if value.Category.ID != category || value.Category.Name != "Hardware & repairs" || value.Page != number || value.PageSize != 20 || value.TimeZone != hs.DeadlineCalendarTimeZoneUTC || value.Period != hs.DeadlineCalendarPeriod(params.Period.Or(hs.HelpdeskDeadlineCalendarPeriodDay)) || !value.AsOf.Equal(params.AsOf.UTC().Truncate(time.Microsecond)) || value.AsOf.Location() != time.UTC || value.TotalGroups != int64(len(want)) || len(value.Results) != end-start || !calendarBoundMatches(value.FromDate, params.FromDate) || !calendarBoundMatches(value.UntilDate, params.UntilDate) {
					return fail("calendar generated date range, UTC reference and total/page envelope")
				}
				for index, want := range want[start:end] {
					got := value.Results[index]
					if got.PeriodStart.Null != want.PeriodStart.Null || !want.PeriodStart.Null && !got.PeriodStart.Value.Equal(want.PeriodStart.Value) || got.Total != want.Total || got.Open != want.Open || got.Overdue != want.Overdue || !calendarInstantMatches(got.FirstDueAt, want.FirstDueAt) || !calendarInstantMatches(got.LastDueAt, want.LastDueAt) {
						return fail("calendar independent period groups, nullable start and full deadline instants")
					}
				}
			}
		}
	}
	for _, params := range []hs.HelpdeskDeadlineCalendarParams{
		{AsOf: reference, Period: hs.NewOptHelpdeskDeadlineCalendarPeriod("hour")}, {AsOf: reference, Period: hs.NewOptHelpdeskDeadlineCalendarPeriod("")}, {AsOf: reference, Period: hs.NewOptHelpdeskDeadlineCalendarPeriod("Month")},
		{AsOf: reference, TimeZone: hs.NewOptHelpdeskDeadlineCalendarTimeZone("")},
		{AsOf: reference, TimeZone: hs.NewOptHelpdeskDeadlineCalendarTimeZone("Local")},
		{AsOf: reference, TimeZone: hs.NewOptHelpdeskDeadlineCalendarTimeZone("Asia/seoul")},
		{AsOf: reference, TimeZone: hs.NewOptHelpdeskDeadlineCalendarTimeZone("../UTC")},
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

func calendarInstantMatches(actual, expected hs.NilDateTime) bool {
	return actual.Null == expected.Null && (expected.Null || actual.Value.Equal(expected.Value) && actual.Value.Location() == time.UTC && actual.Value.Nanosecond()%1000 == 0)
}

func calendarBoundMatches(actual hs.NilDate, request hs.OptDate) bool {
	return actual.Null == !request.Set && (!request.Set || actual.Value.Format("2006-01-02") == request.Value.Format("2006-01-02"))
}

// Group retained Ticket responses with the standard library. No framework
// imports, ORM helpers or calendar endpoint responses enter this expectation.
// A zoned fixture supplies independent literal local dates for the same rows.
func expectedDeadlineCalendar(params hs.HelpdeskDeadlineCalendarParams, tickets []hs.Ticket, localDates []time.Time) []hs.DeadlineCalendarResultsItem {
	grouped := map[string]hs.DeadlineCalendarResultsItem{}
	for index, ticket := range tickets {
		key := "~"
		var day time.Time
		if !ticket.DueAt.Null {
			at := ticket.DueAt.Value.UTC()
			if localDates != nil {
				at = localDates[index]
			}
			day = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
			switch params.Period.Or(hs.HelpdeskDeadlineCalendarPeriodDay) {
			case hs.HelpdeskDeadlineCalendarPeriodWeek:
				day = day.AddDate(0, 0, -int((day.Weekday()+6)%7))
			case hs.HelpdeskDeadlineCalendarPeriodMonth:
				day = time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
			case hs.HelpdeskDeadlineCalendarPeriodQuarter:
				day = time.Date(day.Year(), (day.Month()-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC)
			case hs.HelpdeskDeadlineCalendarPeriodYear:
				day = time.Date(day.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
			}
			key = day.Format("2006-01-02")
		}
		if params.FromDate.Set && (ticket.DueAt.Null || key < params.FromDate.Value.Format("2006-01-02")) || params.UntilDate.Set && (ticket.DueAt.Null || key > params.UntilDate.Value.Format("2006-01-02")) {
			continue
		}
		row, exists := grouped[key]
		if !exists {
			row.PeriodStart = hs.NilDate{Null: true}
			row.FirstDueAt = hs.NilDateTime{Null: true}
			row.LastDueAt = hs.NilDateTime{Null: true}
			if !ticket.DueAt.Null {
				row.PeriodStart = hs.NewNilDate(day)
				row.FirstDueAt = hs.NewNilDateTime(ticket.DueAt.Value.UTC())
				row.LastDueAt = hs.NewNilDateTime(ticket.DueAt.Value.UTC())
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
			if ticket.DueAt.Value.Before(row.FirstDueAt.Value) {
				row.FirstDueAt = hs.NewNilDateTime(ticket.DueAt.Value.UTC())
			}
			if ticket.DueAt.Value.After(row.LastDueAt.Value) {
				row.LastDueAt = hs.NewNilDateTime(ticket.DueAt.Value.UTC())
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
