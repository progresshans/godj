package main

import (
	"context"
	hs "example.com/godj-openapi-client/helpdesksession"
	"net/http"
)

func checkHelpdeskClockTimeUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(request hs.TicketPatch) error {
		response, err := client.HelpdeskTicketPatch(ctx, &request, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *row != expected {
			return fail("clock PATCH/presence")
		}
		return nil
	}
	expected.ServiceAt = hs.NewNilString("00:00:00")
	if err := patch(hs.TicketPatch{ServiceAt: hs.NewOptNilString("00:00:00")}); err != nil {
		return hs.Ticket{}, err
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok := response.(*hs.Ticket)
	if err != nil || !ok || *row != expected {
		return hs.Ticket{}, fail("clock PUT omission")
	}
	clear := hs.OptNilString{}
	clear.SetToNull()
	expected.ServiceAt.SetToNull()
	if err := patch(hs.TicketPatch{ServiceAt: clear}); err != nil {
		return hs.Ticket{}, err
	}
	for _, value := range []string{"23:59:59.999999", "00:00:00.000001", "12:34:56.123456"} {
		expected.ServiceAt = hs.NewNilString(value)
		if err := patch(hs.TicketPatch{ServiceAt: hs.NewOptNilString(value)}); err != nil {
			return hs.Ticket{}, err
		}
	}
	return expected, nil
}

func checkGeneratedClockTimeWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null`
	clear := hs.OptNilString{}
	clear.SetToNull()
	for _, test := range []struct {
		request     hs.TicketPatch
		wire, value string
	}{
		{hs.TicketPatch{}, `{}`, `null`},
		{hs.TicketPatch{ServiceAt: clear}, `{"service_at":null}`, `null`},
		{hs.TicketPatch{ServiceAt: hs.NewOptNilString("00:00:00")}, `{"service_at":"00:00:00"}`, `"00:00:00"`},
		{hs.TicketPatch{ServiceAt: hs.NewOptNilString("23:59:59.999999")}, `{"service_at":"23:59:59.999999"}`, `"23:59:59.999999"`},
		{hs.TicketPatch{ServiceAt: hs.NewOptNilString("00:00:00.000001")}, `{"service_at":"00:00:00.000001"}`, `"00:00:00.000001"`},
	} {
		calls := 0
		client, err := nullableBooleanWireClient(base+`,"service_at":`+test.value+`}`, test.wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &test.request, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.ServiceAt.Null != (test.value == `null`) {
			return fail("clock wire presence")
		}
		if !row.ServiceAt.Null && `"`+row.ServiceAt.Value+`"` != test.value {
			return fail("clock wire lost precision")
		}
	}
	for _, suffix := range []string{`}`, `,"service_at":false}`, `,"service_at":0}`, `,"service_at":"24:00:00"}`, `,"service_at":"12:34:56Z"}`, `,"service_at":"12:34:56.1234567"}`, `,"service_at":"2000-01-01"}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("clock required response/domain rejection")
		}
	}
	for _, value := range []string{"24:00:00", "12:34:56Z", "2000-01-01", "12:34:56.1234567"} {
		request := hs.TicketPatch{ServiceAt: hs.NewOptNilString(value)}
		// The generator exposes Validate but its encoder doesn't invoke it.
		if request.Validate() == nil {
			return fail("clock explicit request validation")
		}
	}

	return nil
}
