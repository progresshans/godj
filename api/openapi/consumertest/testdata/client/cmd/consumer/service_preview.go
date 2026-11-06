package main

import (
	"context"
	"errors"
	"io"
	"math/big"
	"net/http"
	"sort"
	"time"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskServicePreview(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, tickets []hs.Ticket) (result error) {
	if len(tickets) != 6 {
		return fail("service preview requires independent component and boundary fixtures")
	}
	// Fixture writes are separate from preview GETs. Restore all four original
	// columns, including NULL, so the parent can check its retained rows/audit.
	prepared := append([]hs.Ticket(nil), tickets...)
	var attempted []int
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		// A safe error response can issue a new CSRF cookie. Obtain its
		// matching header before restoration instead of retrying a write.
		if _, err := helpdeskList(cleanup, client, transport, state); err != nil {
			result = errors.Join(result, err)
			return
		}
		for _, index := range attempted {
			result = errors.Join(result, patchServiceFixture(cleanup, client, transport, tickets[index]))
		}
		result = errors.Join(result, requireHelpdeskTickets(cleanup, client, transport, state, tickets...))
	}()
	for index := range prepared {
		row := &prepared[index]
		row.ServiceOn = hs.NewNilDate(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC))
		row.ServiceAt = hs.NewNilString("00:00:00.000001")
		row.Elapsed = hs.NewNilString("1 01:30:00.000001")
		row.DueAt = hs.NewNilDateTime(time.Date(2024, 3, 1, 12, 0, 0, 2000, time.UTC))
		switch index {
		case 0:
			row.ServiceAt, row.Elapsed = hs.NewNilString("00:00:00"), hs.NewNilString("00:00:00")
		case 1:
			row.ServiceAt, row.Elapsed = hs.NewNilString("23:59:59.999999"), hs.NewNilString("-1 23:59:59.999999")
		case 2:
			row.ServiceOn = hs.NewNilDate(time.Date(2, 1, 1, 0, 0, 0, 0, time.UTC))
			row.DueAt = hs.NewNilDateTime(time.Time{})
		case 3:
			row.ServiceOn = hs.NewNilDate(time.Date(9998, 12, 30, 0, 0, 0, 0, time.UTC))
			row.DueAt = hs.NewNilDateTime(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))
		case 4:
			row.ServiceOn = hs.NilDate{Null: true}
		case 5:
			row.ServiceAt = hs.NilString{Null: true}
		}
		attempted = append(attempted, index)
		if err := patchServiceFixture(ctx, client, transport, *row); err != nil {
			return err
		}
	}
	viewers := []struct {
		client    *hs.Client
		transport *observedTransport
		state     *sessionState
	}{{client, transport, state}, {readOnly, readOnlyTransport, readOnlyState}}
	minimum := time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600))
	check := func(params hs.HelpdeskServicePreviewParams) error {
		rows, shift, bound, err := expectedServicePreview(params, prepared)
		if err != nil {
			return err
		}
		for _, viewer := range viewers {
			pages := []int64{1, 50001}
			for number := int64(2); number <= int64(len(rows)/20)+2; number++ {
				pages = append(pages, number)
			}
			for _, number := range pages {
				request := params
				if number != 1 {
					request.P = hs.NewOptInt64(number)
				}
				before := viewer.transport.requestCount()
				response, err := viewer.client.HelpdeskServicePreview(ctx, request)
				page, ok := response.(*hs.ServicePreviewHeaders)
				if err != nil || !ok || viewer.transport.requestCount() != before+1 || viewer.transport.lastStatus() != http.StatusOK || !page.XGodjCsrftoken.Set || !viewer.state.ready(page.XGodjCsrftoken.Value) {
					return fail("service preview generated view-only admission, one request and CSRF response")
				}
				value := page.Response
				start, end := min(len(rows), int(number-1)*20), min(len(rows), int(number)*20)
				if value.Category.ID != category || value.Category.Name != "Hardware & repairs" || value.Page != number || value.PageSize != 20 || value.Shift != shift || !calendarInstantMatches(value.MinStart, bound) || value.TotalTickets != int64(len(rows)) || len(value.Results) != end-start {
					return fail("service preview independent offset, range and filtered page envelope")
				}
				for index, want := range rows[start:end] {
					got := value.Results[index]
					if got.ID != want.ID || got.Subject != want.Subject || got.ServiceOn.Null != want.ServiceOn.Null || !want.ServiceOn.Null && got.ServiceOn.Value.Format("2006-01-02") != want.ServiceOn.Value.Format("2006-01-02") || got.ServiceAt != want.ServiceAt || got.Elapsed != want.Elapsed || !calendarInstantMatches(got.DueAt, want.DueAt) || !calendarInstantMatches(got.ServiceStart, want.ServiceStart) || !calendarInstantMatches(got.AdjustedStart, want.AdjustedStart) || !calendarInstantMatches(got.FinishAt, want.FinishAt) || got.DeadlineSlack != want.DeadlineSlack {
						return fail("service preview independent exact dates, clocks, duration, NULL propagation and ordering")
					}
				}
			}
		}
		return requireHelpdeskTickets(ctx, client, transport, state, prepared...)
	}
	for _, params := range []hs.HelpdeskServicePreviewParams{
		{}, {Shift: hs.NewOptString("00:00:00")},
		{Shift: hs.NewOptString("00:00:00.000001")}, {Shift: hs.NewOptString("-1 23:59:59.999999")},
		{Shift: hs.NewOptString("1 00:00:00"), MinStart: hs.NewOptDateTime(minimum)},
		{Shift: hs.NewOptString("-1 00:00:00"), MinStart: hs.NewOptDateTime(minimum)},
		{MinStart: hs.NewOptDateTime(time.Time{})},
		{MinStart: hs.NewOptDateTime(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))},
	} {
		if err := check(params); err != nil {
			return err
		}
	}
	// Reuse the two component-absence fixtures to distinguish missing elapsed
	// and missing deadline without touching the later bulk/priority audit owners.
	for _, index := range []int{4, 5} {
		prepared[index].ServiceOn = hs.NewNilDate(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC))
		prepared[index].ServiceAt = hs.NewNilString("00:00:00")
		if index == 4 {
			prepared[index].Elapsed = hs.NilString{Null: true}
		} else {
			prepared[index].DueAt = hs.NilDateTime{Null: true}
		}
		if err := patchServiceFixture(ctx, client, transport, prepared[index]); err != nil {
			return err
		}
	}
	for _, params := range []hs.HelpdeskServicePreviewParams{{}, {Shift: hs.NewOptString("00:00:00.000001"), MinStart: hs.NewOptDateTime(minimum)}} {
		if err := check(params); err != nil {
			return err
		}
	}
	for _, params := range []hs.HelpdeskServicePreviewParams{
		{P: hs.NewOptInt64(0)}, {P: hs.NewOptInt64(50002)},
		{Shift: hs.NewOptString("")}, {Shift: hs.NewOptString("0 00:00:00")},
		{Shift: hs.NewOptString("00:00:00.000000")}, {Shift: hs.NewOptString("1000000000 00:00:00")},
		{MinStart: hs.NewOptDateTime(time.Date(0, 12, 31, 0, 0, 0, 0, time.UTC))},
		{MinStart: hs.NewOptDateTime(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))},
		{MinStart: hs.NewOptDateTime(time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("outside UTC", 3600)))},
	} {
		before := transport.requestCount()
		response, err := client.HelpdeskServicePreview(ctx, params)
		value, ok := response.(*hs.HelpdeskServicePreviewBadRequest)
		if err != nil || !ok || transport.requestCount() != before+1 || transport.lastStatus() != http.StatusBadRequest || value.Response.Code != "validation_error" {
			return fail("service preview generated invalid temporal query rejection")
		}
	}
	checkFailure := func(params hs.HelpdeskServicePreviewParams) error {
		for _, viewer := range viewers {
			before := viewer.transport.requestCount()
			response, err := viewer.client.HelpdeskServicePreview(ctx, params)
			value, ok := response.(*hs.HelpdeskServicePreviewInternalServerError)
			if err != nil || !ok || viewer.transport.requestCount() != before+1 || viewer.transport.lastStatus() != http.StatusInternalServerError {
				return fail("service preview arithmetic failure must not retry")
			}
			body, err := io.ReadAll(value)
			if err != nil || string(body) != "Internal Server Error\n" {
				return fail("service preview arithmetic failure must not publish partial results or diagnostics")
			}
		}
		return nil
	}
	for _, number := range []int64{1, 50001} {
		if err := checkFailure(hs.HelpdeskServicePreviewParams{P: hs.NewOptInt64(number), Shift: hs.NewOptString("3652059 00:00:00")}); err != nil {
			return err
		}
	}
	if err := requireHelpdeskTickets(ctx, client, transport, state, prepared...); err != nil {
		return err
	}
	// Finish is a selected result rather than the ordering expression. Select
	// its row when testing that a valid but unrepresentable finish fails.
	prepared[0].Elapsed = hs.NewNilString("106751991 04:00:54.775807")
	if err := patchServiceFixture(ctx, client, transport, prepared[0]); err != nil {
		return err
	}
	if err := checkFailure(hs.HelpdeskServicePreviewParams{}); err != nil {
		return err
	}
	return requireHelpdeskTickets(ctx, client, transport, state, prepared...)
}

func patchServiceFixture(ctx context.Context, client *hs.Client, transport *observedTransport, want hs.Ticket) error {
	patch := &hs.TicketPatch{}
	if want.ServiceOn.Null {
		patch.ServiceOn.SetToNull()
	} else {
		patch.ServiceOn.SetTo(want.ServiceOn.Value)
	}
	if want.ServiceAt.Null {
		patch.ServiceAt.SetToNull()
	} else {
		patch.ServiceAt.SetTo(want.ServiceAt.Value)
	}
	if want.Elapsed.Null {
		patch.Elapsed.SetToNull()
	} else {
		patch.Elapsed.SetTo(want.Elapsed.Value)
	}
	if want.DueAt.Null {
		patch.DueAt.SetToNull()
	} else {
		patch.DueAt.SetTo(want.DueAt.Value)
	}
	before := transport.requestCount()
	response, err := client.HelpdeskTicketPatch(ctx, patch, hs.HelpdeskTicketPatchParams{ID: want.ID})
	got, ok := response.(*hs.Ticket)
	if err != nil || !ok || transport.requestCount() != before+1 || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*got, want) {
		return fail("service preview fixture preparation or restoration changed retained columns")
	}
	return nil
}

// Reconstruct UTC instants with the standard library and exact integer
// microseconds. Neither framework code nor a preview response is an oracle.
func expectedServicePreview(params hs.HelpdeskServicePreviewParams, tickets []hs.Ticket) ([]hs.ServicePreviewResultsItem, string, hs.NilDateTime, error) {
	shift := params.Shift.Or("00:00:00")
	delta, err := summaryDurationMicros(shift)
	if err != nil {
		return nil, "", hs.NilDateTime{}, err
	}
	minimum := hs.NilDateTime{Null: true}
	if params.MinStart.Set {
		minimum = hs.NewNilDateTime(params.MinStart.Value.UTC().Truncate(time.Microsecond))
	}
	rows := make([]hs.ServicePreviewResultsItem, 0, len(tickets))
	instant := func(micros *big.Int) (hs.NilDateTime, error) {
		if !micros.IsInt64() {
			return hs.NilDateTime{}, fail("service preview expected timestamp range")
		}
		at := time.UnixMicro(micros.Int64()).UTC()
		if at.Year() < 1 || at.Year() > 9999 {
			return hs.NilDateTime{}, fail("service preview expected DateTime domain")
		}
		return hs.NewNilDateTime(at), nil
	}
	for _, ticket := range tickets {
		row := hs.ServicePreviewResultsItem{ID: ticket.ID, Subject: ticket.Subject, ServiceOn: ticket.ServiceOn, ServiceAt: ticket.ServiceAt, Elapsed: ticket.Elapsed, DueAt: ticket.DueAt, ServiceStart: hs.NilDateTime{Null: true}, AdjustedStart: hs.NilDateTime{Null: true}, FinishAt: hs.NilDateTime{Null: true}, DeadlineSlack: hs.NilString{Null: true}}
		if !ticket.ServiceOn.Null && !ticket.ServiceAt.Null {
			wall, err := time.Parse("15:04:05", ticket.ServiceAt.Value)
			if err != nil {
				return nil, "", minimum, err
			}
			day := ticket.ServiceOn.Value
			start := time.Date(day.Year(), day.Month(), day.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), time.UTC)
			row.ServiceStart = hs.NewNilDateTime(start)
			adjusted := new(big.Int).Add(big.NewInt(start.UnixMicro()), delta)
			row.AdjustedStart, err = instant(adjusted)
			if err != nil {
				return nil, "", minimum, err
			}
			if !ticket.Elapsed.Null {
				elapsed, err := summaryDurationMicros(ticket.Elapsed.Value)
				if err != nil {
					return nil, "", minimum, err
				}
				finish := new(big.Int).Add(adjusted, elapsed)
				row.FinishAt, err = instant(finish)
				if err != nil {
					return nil, "", minimum, err
				}
				if !ticket.DueAt.Null {
					slack, err := summaryDurationText(new(big.Int).Sub(big.NewInt(ticket.DueAt.Value.UnixMicro()), finish))
					if err != nil {
						return nil, "", minimum, err
					}
					row.DeadlineSlack = hs.NewNilString(slack)
				}
			}
		}
		if !minimum.Null && (row.AdjustedStart.Null || row.AdjustedStart.Value.Before(minimum.Value)) {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i].AdjustedStart, rows[j].AdjustedStart
		if left.Null != right.Null {
			return !left.Null
		}
		if !left.Null && !left.Value.Equal(right.Value) {
			return left.Value.Before(right.Value)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, shift, minimum, nil
}
