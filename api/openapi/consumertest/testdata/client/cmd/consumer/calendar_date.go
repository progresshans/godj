package main

import (
	"context"
	"errors"
	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/ogen-go/ogen/ogenerrors"
	"net/http"
	"time"
)

func checkHelpdeskCalendarDateUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(request hs.TicketPatch) error {
		response, err := client.HelpdeskTicketPatch(ctx, &request, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *row != expected {
			return fail("helpdesk calendar date PATCH/presence")
		}
		return nil
	}
	expected.ServiceOn = hs.NewNilDate(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := patch(hs.TicketPatch{ServiceOn: hs.NewOptNilDate(expected.ServiceOn.Value)}); err != nil {
		return hs.Ticket{}, err
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok := response.(*hs.Ticket)
	if err != nil || !ok || *row != expected {
		return hs.Ticket{}, fail("helpdesk calendar date PUT omission")
	}
	clear := hs.OptNilDate{}
	clear.SetToNull()
	expected.ServiceOn.SetToNull()
	if err := patch(hs.TicketPatch{ServiceOn: clear}); err != nil {
		return hs.Ticket{}, err
	}
	for _, value := range []time.Time{time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC)} {
		expected.ServiceOn = hs.NewNilDate(value)
		if err := patch(hs.TicketPatch{ServiceOn: hs.NewOptNilDate(value)}); err != nil {
			return hs.Ticket{}, err
		}
	}
	bad, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ServiceOn: hs.NewOptNilDate(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC))}, params)
	rejected, ok := bad.(*hs.HelpdeskTicketPatchBadRequest)
	if err != nil || !ok || len(rejected.Errors) != 1 || rejected.Errors[0].Field != "service_on" || rejected.Errors[0].Code != "invalid" {
		return hs.Ticket{}, fail("server rejects calendar date outside supported years")
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	return expected, nil
}

func checkGeneratedCalendarDateWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_at":null`
	clear := hs.OptNilDate{}
	clear.SetToNull()
	for _, test := range []struct {
		request     hs.TicketPatch
		wire, value string
	}{
		{hs.TicketPatch{}, `{}`, `null`},
		{hs.TicketPatch{ServiceOn: clear}, `{"service_on":null}`, `null`},
		{hs.TicketPatch{ServiceOn: hs.NewOptNilDate(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC))}, `{"service_on":"0001-01-01"}`, `"0001-01-01"`},
		{hs.TicketPatch{ServiceOn: hs.NewOptNilDate(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))}, `{"service_on":"9999-12-31"}`, `"9999-12-31"`},
		{hs.TicketPatch{ServiceOn: hs.NewOptNilDate(time.Date(2026, 9, 20, 0, 0, 0, 0, time.FixedZone("+14", 14*3600)))}, `{"service_on":"2026-09-20"}`, `"2026-09-20"`},
	} {
		calls := 0
		client, err := nullableBooleanWireClient(base+`,"service_on":`+test.value+`}`, test.wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &test.request, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.ServiceOn.Null != (test.value == `null`) {
			return fail("generated date wire presence")
		}
		if !row.ServiceOn.Null && `"`+row.ServiceOn.Value.Format("2006-01-02")+`"` != test.value {
			return fail("generated date wire changed calendar day")
		}
	}
	for _, suffix := range []string{`}`, `,"service_on":false}`, `,"service_on":0}`, `,"service_on":"1900-02-29"}`, `,"service_on":"2000-02-29T00:00:00Z"}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		_, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1})
		var decodeError *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decodeError) {
			return fail("generated required date response rejection")
		}
	}
	return nil
}
