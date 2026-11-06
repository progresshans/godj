package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	hs "example.com/godj-openapi-client/helpdesksession"
)

// Only literal dates and retained Ticket values form this oracle. The external
// client imports no framework packages and never reads the server's tzdata.
func checkHelpdeskZonedDeadlineCalendar(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, tickets []hs.Ticket) (result error) {
	if len(tickets) != 6 {
		return fail("zoned calendar requires six independently retained rows")
	}
	prepared := append([]hs.Ticket(nil), tickets...)
	attempted := make(map[int]bool)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if _, err := helpdeskList(cleanup, client, transport, state); err != nil {
			result = errors.Join(result, err)
			return
		}
		for index := range tickets {
			if attempted[index] {
				result = errors.Join(result, patchCalendarFixture(cleanup, client, transport, tickets[index]))
			}
		}
		result = errors.Join(result, requireHelpdeskTickets(cleanup, client, transport, state, tickets...))
	}()
	for _, fixture := range []struct {
		zone   string
		values [6][2]string
	}{
		{"America/New_York", [6][2]string{
			{"2024-03-10T04:59:59.999999Z", "2024-03-09"}, {"2024-03-10T05:00:00Z", "2024-03-10"},
			{"2024-03-10T07:00:00Z", "2024-03-10"}, {"2024-03-11T03:59:59.999999Z", "2024-03-10"}, {"2024-03-11T04:00:00Z", "2024-03-11"}, {},
		}},
		{"US/Eastern", [6][2]string{
			{"2024-11-03T03:59:59.999999Z", "2024-11-02"}, {"2024-11-03T05:30:00Z", "2024-11-03"},
			{"2024-11-03T06:30:00Z", "2024-11-03"}, {"2024-11-04T04:59:59.999999Z", "2024-11-03"}, {"2024-11-04T05:00:00Z", "2024-11-04"}, {},
		}},
		{"Asia/Seoul", [6][2]string{
			{"1899-12-31T15:32:07.999999Z", "1899-12-31"}, {"1899-12-31T15:32:08Z", "1900-01-01"},
			{"2023-12-31T14:59:59.999999Z", "2023-12-31"}, {"2023-12-31T15:00:00Z", "2024-01-01"}, {"2024-02-29T15:00:00Z", "2024-03-01"}, {},
		}},
		{"Australia/Lord_Howe", [6][2]string{
			{"2024-10-05T13:29:59.999999Z", "2024-10-05"}, {"2024-10-05T13:30:00Z", "2024-10-06"},
			{"2024-10-05T15:30:00Z", "2024-10-06"}, {"2024-10-06T12:59:59.999999Z", "2024-10-06"}, {"2024-10-06T13:00:00Z", "2024-10-07"}, {},
		}},
		{"Pacific/Apia", [6][2]string{
			{"2011-12-30T09:59:59.999999Z", "2011-12-29"}, {"2011-12-30T10:00:00Z", "2011-12-31"},
			{"2011-12-31T09:59:59.999999Z", "2011-12-31"}, {"2011-12-31T10:00:00Z", "2012-01-01"}, {"2012-01-01T09:59:59.999999Z", "2012-01-01"}, {},
		}},
	} {
		localDates := make([]time.Time, len(prepared))
		for index, pair := range fixture.values {
			prepared[index].DueAt = hs.NilDateTime{Null: true}
			if pair[0] != "" {
				at, err := time.Parse(time.RFC3339Nano, pair[0])
				if err != nil {
					return err
				}
				localDates[index], err = time.Parse("2006-01-02", pair[1])
				if err != nil {
					return err
				}
				prepared[index].DueAt = hs.NewNilDateTime(at)
			}
			attempted[index] = true
			if err := patchCalendarFixture(ctx, client, transport, prepared[index]); err != nil {
				return err
			}
		}
		for _, period := range []hs.HelpdeskDeadlineCalendarPeriod{hs.HelpdeskDeadlineCalendarPeriodDay, hs.HelpdeskDeadlineCalendarPeriodWeek, hs.HelpdeskDeadlineCalendarPeriodMonth, hs.HelpdeskDeadlineCalendarPeriodQuarter, hs.HelpdeskDeadlineCalendarPeriodYear} {
			params := hs.HelpdeskDeadlineCalendarParams{AsOf: prepared[2].DueAt.Value, TimeZone: hs.NewOptHelpdeskDeadlineCalendarTimeZone(hs.HelpdeskDeadlineCalendarTimeZone(fixture.zone)), Period: hs.NewOptHelpdeskDeadlineCalendarPeriod(period)}
			for _, bounded := range []bool{false, true} {
				if bounded {
					params.FromDate = hs.NewOptDate(time.Date(localDates[2].Year(), 1, 1, 0, 0, 0, 0, time.UTC))
					params.UntilDate = hs.NewOptDate(time.Date(localDates[2].Year(), 12, 31, 0, 0, 0, 0, time.UTC))
				}
				want := expectedDeadlineCalendar(params, prepared, localDates)
				for _, viewer := range []struct {
					client    *hs.Client
					transport *observedTransport
					state     *sessionState
				}{{client, transport, state}, {readOnly, readOnlyTransport, readOnlyState}} {
					for _, number := range []int64{1, 2} {
						request := params
						request.P = hs.NewOptInt64(number)
						before := viewer.transport.requestCount()
						response, err := viewer.client.HelpdeskDeadlineCalendar(ctx, request)
						page, ok := response.(*hs.DeadlineCalendarHeaders)
						if err != nil || !ok || viewer.transport.requestCount() != before+1 || viewer.transport.lastStatus() != http.StatusOK || !page.XGodjCsrftoken.Set || !viewer.state.ready(page.XGodjCsrftoken.Value) {
							return fail("zoned calendar generated admission headers and single request")
						}
						value := page.Response
						start, end := min(len(want), int(number-1)*20), min(len(want), int(number)*20)
						if value.Category.ID != category || value.TimeZone != hs.DeadlineCalendarTimeZone(fixture.zone) || value.Period != hs.DeadlineCalendarPeriod(period) || value.Page != number || value.PageSize != 20 || !value.AsOf.Equal(params.AsOf) || value.AsOf.Location() != time.UTC || value.TotalGroups != int64(len(want)) || len(value.Results) != end-start || !calendarBoundMatches(value.FromDate, params.FromDate) || !calendarBoundMatches(value.UntilDate, params.UntilDate) {
							return fail("zoned calendar generated local range and UTC reference envelope")
						}
						for index, want := range want[start:end] {
							got := value.Results[index]
							if got.PeriodStart.Null != want.PeriodStart.Null || !want.PeriodStart.Null && !got.PeriodStart.Value.Equal(want.PeriodStart.Value) || got.Total != want.Total || got.Open != want.Open || got.Overdue != want.Overdue || !calendarInstantMatches(got.FirstDueAt, want.FirstDueAt) || !calendarInstantMatches(got.LastDueAt, want.LastDueAt) {
								return fail("zoned calendar independent DST alias seconds and date-line observations")
							}
						}
					}
				}
			}
		}
		if err := requireHelpdeskTickets(ctx, client, transport, state, prepared...); err != nil {
			return err
		}
	}
	for _, boundary := range []struct {
		zone string
		at   time.Time
	}{
		{"America/New_York", time.Time{}}, {"Asia/Seoul", time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)},
	} {
		original := prepared[0]
		prepared[0].DueAt = hs.NewNilDateTime(boundary.at)
		if err := patchCalendarFixture(ctx, client, transport, prepared[0]); err != nil {
			return err
		}
		params := hs.HelpdeskDeadlineCalendarParams{AsOf: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), TimeZone: hs.NewOptHelpdeskDeadlineCalendarTimeZone(hs.HelpdeskDeadlineCalendarTimeZone(boundary.zone)), FromDate: hs.NewOptDate(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))}
		before := transport.requestCount()
		response, err := client.HelpdeskDeadlineCalendar(ctx, params)
		failed, ok := response.(*hs.HelpdeskDeadlineCalendarInternalServerError)
		if err != nil || !ok || transport.requestCount() != before+1 || transport.lastStatus() != http.StatusInternalServerError {
			return fail("zoned calendar local overflow must fail before hidden aggregate output")
		}
		body, err := io.ReadAll(failed)
		if err != nil || string(body) != "Internal Server Error\n" {
			return fail("zoned calendar overflow exposed partial rows or storage details")
		}
		if err := requireHelpdeskTickets(ctx, client, transport, state, prepared...); err != nil {
			return err
		}
		prepared[0] = original
		if err := patchCalendarFixture(ctx, client, transport, original); err != nil {
			return err
		}
	}
	return nil
}

func patchCalendarFixture(ctx context.Context, client *hs.Client, transport *observedTransport, want hs.Ticket) error {
	patch := &hs.TicketPatch{}
	if want.DueAt.Null {
		patch.DueAt.SetToNull()
	} else {
		patch.DueAt.SetTo(want.DueAt.Value)
	}
	before := transport.requestCount()
	response, err := client.HelpdeskTicketPatch(ctx, patch, hs.HelpdeskTicketPatchParams{ID: want.ID})
	got, ok := response.(*hs.Ticket)
	if err != nil || !ok || transport.requestCount() != before+1 || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*got, want) {
		return fail("zoned calendar fixture preparation or restoration changed retained columns")
	}
	return nil
}
