package main

import (
	"context"
	hs "example.com/godj-openapi-client/helpdesksession"
	"net/http"
)

func checkHelpdeskDurationUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(request hs.TicketPatch) error {
		response, err := client.HelpdeskTicketPatch(ctx, &request, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *row != expected {
			return fail("duration PATCH/presence")
		}
		return nil
	}
	expected.Elapsed = hs.NewNilString("00:00:00")
	if err := patch(hs.TicketPatch{Elapsed: hs.NewOptNilString("00:00:00")}); err != nil {
		return hs.Ticket{}, err
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok := response.(*hs.Ticket)
	if err != nil || !ok || *row != expected {
		return hs.Ticket{}, fail("duration PUT omission")
	}
	clear := hs.OptNilString{}
	clear.SetToNull()
	expected.Elapsed.SetToNull()
	if err := patch(hs.TicketPatch{Elapsed: clear}); err != nil {
		return hs.Ticket{}, err
	}
	for _, value := range []string{"-106751992 19:59:05.224192", "106751991 04:00:54.775807", "-1 23:59:59.999999", "00:00:00.000001", "1 02:03:04.123456"} {
		expected.Elapsed = hs.NewNilString(value)
		if err := patch(hs.TicketPatch{Elapsed: hs.NewOptNilString(value)}); err != nil {
			return hs.Ticket{}, err
		}
	}
	return expected, nil
}

func checkGeneratedDurationWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"service_at":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null`
	clear := hs.OptNilString{}
	clear.SetToNull()
	for _, test := range []struct {
		request     hs.TicketPatch
		wire, value string
	}{
		{hs.TicketPatch{}, `{}`, `null`},
		{hs.TicketPatch{Elapsed: clear}, `{"elapsed":null}`, `null`},
		{hs.TicketPatch{Elapsed: hs.NewOptNilString("00:00:00")}, `{"elapsed":"00:00:00"}`, `"00:00:00"`},
		{hs.TicketPatch{Elapsed: hs.NewOptNilString("-1 23:59:59.999999")}, `{"elapsed":"-1 23:59:59.999999"}`, `"-1 23:59:59.999999"`},
		{hs.TicketPatch{Elapsed: hs.NewOptNilString("00:00:00.000001")}, `{"elapsed":"00:00:00.000001"}`, `"00:00:00.000001"`},
	} {
		calls := 0
		client, err := nullableBooleanWireClient(base+`,"elapsed":`+test.value+`}`, test.wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &test.request, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.Elapsed.Null != (test.value == `null`) {
			return fail("duration wire presence")
		}
		if !row.Elapsed.Null && `"`+row.Elapsed.Value+`"` != test.value {
			return fail("duration wire lost precision")
		}
	}
	for _, suffix := range []string{`}`, `,"elapsed":false}`, `,"elapsed":0}`, `,"elapsed":"24:00:00"}`, `,"elapsed":"12:34:56Z"}`, `,"elapsed":"1 02:03:04.1234567"}`, `,"elapsed":"2000-01-01"}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("duration required response/domain rejection")
		}
	}
	for _, value := range []string{"24:00:00", "12:34:56Z", "2000-01-01", "1 02:03:04.1234567"} {
		request := hs.TicketPatch{Elapsed: hs.NewOptNilString(value)}
		// The generator exposes Validate but its encoder doesn't invoke it.
		if request.Validate() == nil {
			return fail("duration explicit request validation")
		}
	}

	return nil
}
