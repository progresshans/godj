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

func checkHelpdeskDeadlinePreview(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, tickets []hs.Ticket) (result error) {
	viewers := []struct {
		client    *hs.Client
		transport *observedTransport
		state     *sessionState
	}{{client, transport, state}, {readOnly, readOnlyTransport, readOnlyState}}
	reference := time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600))
	minimum := time.Time{}
	maximum := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	check := func(params hs.HelpdeskDeadlinePreviewParams, retained []hs.Ticket) error {
		rows, shift, threshold, err := expectedDeadlinePreview(params, retained)
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
				response, err := viewer.client.HelpdeskDeadlinePreview(ctx, request)
				page, ok := response.(*hs.DeadlinePreviewHeaders)
				if err != nil || !ok || viewer.transport.requestCount() != before+1 || viewer.transport.lastStatus() != http.StatusOK || !page.XGodjCsrftoken.Set || !viewer.state.ready(page.XGodjCsrftoken.Value) {
					return fail("deadline preview generated read-only admission and one-request CSRF response")
				}
				value := page.Response
				start := min(len(rows), int(number-1)*20)
				end := min(len(rows), start+20)
				if value.Category.ID != category || value.Category.Name != "Hardware & repairs" || value.Page != number || value.PageSize != 20 || !value.AsOf.Equal(params.AsOf.UTC().Truncate(time.Microsecond)) || value.AsOf.Location() != time.UTC || value.Shift != shift || value.MinRemaining != threshold || value.TotalTickets != int64(len(rows)) || len(value.Results) != end-start {
					return fail("deadline preview explicit reference, range, NULL and filtered page envelope")
				}
				for index, want := range rows[start:end] {
					got := value.Results[index]
					if got.ID != want.ID || got.Subject != want.Subject || got.DueAt.Null != want.DueAt.Null || got.AdjustedDueAt.Null != want.AdjustedDueAt.Null || got.Remaining != want.Remaining || !want.DueAt.Null && (!got.DueAt.Value.Equal(want.DueAt.Value) || !got.AdjustedDueAt.Value.Equal(want.AdjustedDueAt.Value)) {
						return fail("deadline preview independent exact microseconds, ordering and NULL distinction")
					}
				}
			}
		}
		return requireHelpdeskTickets(ctx, client, transport, state, retained...)
	}
	for _, params := range []hs.HelpdeskDeadlinePreviewParams{
		{AsOf: reference},
		{AsOf: minimum, Shift: hs.NewOptString("00:00:00")},
		{AsOf: maximum},
		{AsOf: reference, MinRemaining: hs.NewOptString("00:00:00")},
		{AsOf: reference, MinRemaining: hs.NewOptString("3652059 00:00:00")},
		{AsOf: reference, MinRemaining: hs.NewOptString("-3652059 00:00:00")},
	} {
		if err := check(params, tickets); err != nil {
			return err
		}
	}
	// The retained fixtures include both DateTime domain endpoints. A one
	// microsecond shift must fail the entire query, including later pages.
	for _, viewer := range viewers {
		for _, shift := range []string{"00:00:00.000001", "-1 23:59:59.999999", "999999999 23:59:59.999999"} {
			for _, number := range []int64{1, 50001} {
				before := viewer.transport.requestCount()
				response, err := viewer.client.HelpdeskDeadlinePreview(ctx, hs.HelpdeskDeadlinePreviewParams{AsOf: reference, Shift: hs.NewOptString(shift), P: hs.NewOptInt64(number)})
				failed, ok := response.(*hs.HelpdeskDeadlinePreviewInternalServerError)
				if err != nil || !ok || viewer.transport.requestCount() != before+1 || viewer.transport.lastStatus() != http.StatusInternalServerError {
					return fail("deadline preview out-of-range computation must fail without retry")
				}
				body, err := io.ReadAll(failed)
				if err != nil || string(body) != "Internal Server Error\n" {
					return fail("deadline preview computation failure disclosed partial results or diagnostics")
				}
			}
		}
	}
	for _, params := range []hs.HelpdeskDeadlinePreviewParams{
		{AsOf: reference, P: hs.NewOptInt64(0)},
		{AsOf: reference, P: hs.NewOptInt64(50002)},
		{AsOf: reference, Shift: hs.NewOptString("")},
		{AsOf: reference, Shift: hs.NewOptString("0 00:00:00")},
		{AsOf: reference, Shift: hs.NewOptString("00:00:00.000000")},
		{AsOf: reference, Shift: hs.NewOptString("1000000000 00:00:00")},
		{AsOf: reference, MinRemaining: hs.NewOptString("")},
		{AsOf: time.Date(0, 12, 31, 0, 0, 0, 0, time.UTC)},
		{AsOf: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{AsOf: time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("outside UTC domain", 3600))},
	} {
		before := transport.requestCount()
		response, err := client.HelpdeskDeadlinePreview(ctx, params)
		value, ok := response.(*hs.HelpdeskDeadlinePreviewBadRequest)
		if err != nil || !ok || transport.requestCount() != before+1 || transport.lastStatus() != http.StatusBadRequest || value.Response.Code != "validation_error" {
			return fail("deadline preview generated invalid temporal query rejection")
		}
	}
	if err := requireHelpdeskTickets(ctx, client, transport, state, tickets...); err != nil {
		return err
	}

	// Fixture preparation uses the writable generated API and is separate from
	// preview GETs. Preserve and restore both original endpoints so the parent
	// still checks the complete retained data, including years 1 and 9999.
	prepared := append([]hs.Ticket(nil), tickets...)
	type replacement struct {
		index int
		due   time.Time
	}
	var replacements []replacement
	for index, ticket := range tickets {
		if ticket.DueAt.Null {
			continue
		}
		switch {
		case ticket.DueAt.Value.Equal(minimum):
			replacements = append(replacements, replacement{index, time.Date(1, 1, 2, 0, 0, 0, 1000, time.UTC)})
		case ticket.DueAt.Value.Equal(maximum):
			replacements = append(replacements, replacement{index, time.Date(9999, 12, 30, 0, 0, 0, 1000, time.UTC)})
		}
	}
	if len(replacements) != 2 {
		return fail("deadline preview requires both retained DateTime boundary fixtures")
	}
	var attempted []int
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, index := range attempted {
			result = errors.Join(result, patchDeadlineFixture(cleanup, client, transport, tickets[index]))
		}
		result = errors.Join(result, requireHelpdeskTickets(cleanup, client, transport, state, tickets...))
	}()
	for _, replacement := range replacements {
		prepared[replacement.index].DueAt = hs.NewNilDateTime(replacement.due)
		attempted = append(attempted, replacement.index)
		if err := patchDeadlineFixture(ctx, client, transport, prepared[replacement.index]); err != nil {
			return err
		}
	}
	for _, params := range []hs.HelpdeskDeadlinePreviewParams{
		{AsOf: reference, Shift: hs.NewOptString("00:00:00.000001")},
		{AsOf: reference, Shift: hs.NewOptString("-1 23:59:59.999999")},
		{AsOf: reference, Shift: hs.NewOptString("1 00:00:00"), MinRemaining: hs.NewOptString("00:00:00")},
		{AsOf: reference, Shift: hs.NewOptString("-1 00:00:00")},
	} {
		if err := check(params, prepared); err != nil {
			return err
		}
	}
	return nil
}

func patchDeadlineFixture(ctx context.Context, client *hs.Client, transport *observedTransport, want hs.Ticket) error {
	before := transport.requestCount()
	response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{DueAt: hs.NewOptNilDateTime(want.DueAt.Value)}, hs.HelpdeskTicketPatchParams{ID: want.ID})
	got, ok := response.(*hs.Ticket)
	if err != nil || !ok || transport.requestCount() != before+1 || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*got, want) {
		return fail("deadline preview fixture preparation or restoration changed retained columns")
	}
	return nil
}

// All expectations use retained API values and standard-library integer
// arithmetic. time.Duration and Time.Sub cannot represent this date range.
func expectedDeadlinePreview(params hs.HelpdeskDeadlinePreviewParams, tickets []hs.Ticket) ([]hs.DeadlinePreviewResultsItem, string, hs.NilString, error) {
	shift := "00:00:00"
	if params.Shift.Set {
		shift = params.Shift.Value
	}
	delta, err := summaryDurationMicros(shift)
	if err != nil {
		return nil, "", hs.NilString{}, err
	}
	minimum := hs.NilString{Null: true}
	var threshold *big.Int
	if params.MinRemaining.Set {
		minimum = hs.NewNilString(params.MinRemaining.Value)
		threshold, err = summaryDurationMicros(params.MinRemaining.Value)
		if err != nil {
			return nil, "", hs.NilString{}, err
		}
	}
	reference := big.NewInt(params.AsOf.UTC().Truncate(time.Microsecond).UnixMicro())
	rows := make([]hs.DeadlinePreviewResultsItem, 0, len(tickets))
	values := map[int64]*big.Int{}
	for _, ticket := range tickets {
		row := hs.DeadlinePreviewResultsItem{ID: ticket.ID, Subject: ticket.Subject, DueAt: hs.NilDateTime{Null: true}, AdjustedDueAt: hs.NilDateTime{Null: true}, Remaining: hs.NilString{Null: true}}
		if !ticket.DueAt.Null {
			adjusted := new(big.Int).Add(big.NewInt(ticket.DueAt.Value.UnixMicro()), delta)
			if !adjusted.IsInt64() {
				return nil, "", hs.NilString{}, fail("deadline preview expected timestamp range")
			}
			instant := time.UnixMicro(adjusted.Int64()).UTC()
			if instant.Year() < 1 || instant.Year() > 9999 {
				return nil, "", hs.NilString{}, fail("deadline preview expected DateTime domain")
			}
			remaining := new(big.Int).Sub(adjusted, reference)
			if threshold != nil && remaining.Cmp(threshold) < 0 {
				continue
			}
			text, err := summaryDurationText(remaining)
			if err != nil {
				return nil, "", hs.NilString{}, err
			}
			row.DueAt, row.AdjustedDueAt, row.Remaining = hs.NewNilDateTime(ticket.DueAt.Value), hs.NewNilDateTime(instant), hs.NewNilString(text)
			values[row.ID] = remaining
		} else if threshold != nil {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := values[rows[i].ID], values[rows[j].ID]
		if left == nil || right == nil {
			if left != right {
				return left != nil
			}
		} else if order := left.Cmp(right); order != 0 {
			return order < 0
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, shift, minimum, nil
}
