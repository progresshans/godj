package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskDecimalUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(request hs.TicketPatch) error {
		response, err := client.HelpdeskTicketPatch(ctx, &request, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *row != expected {
			return fail("decimal PATCH precision/presence")
		}
		return nil
	}
	expected.ExpectedCost = hs.NewNilString("0.00")
	if err := patch(hs.TicketPatch{ExpectedCost: hs.NewOptNilString("0.00")}); err != nil {
		return hs.Ticket{}, err
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	if err := patch(hs.TicketPatch{ExpectedCost: hs.NewOptNilString("-0.00")}); err != nil {
		return hs.Ticket{}, err
	}
	response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok := response.(*hs.Ticket)
	if err != nil || !ok || *row != expected {
		return hs.Ticket{}, fail("decimal PUT omission")
	}
	clear := hs.OptNilString{}
	clear.SetToNull()
	expected.ExpectedCost.SetToNull()
	if err := patch(hs.TicketPatch{ExpectedCost: clear}); err != nil {
		return hs.Ticket{}, err
	}
	for _, value := range []string{"-9999999999.99", "9999999999.99", "-0.01", "0.01", "0.10", "1.50"} {
		expected.ExpectedCost = hs.NewNilString(value)
		if err := patch(hs.TicketPatch{ExpectedCost: hs.NewOptNilString(value)}); err != nil {
			return hs.Ticket{}, err
		}
	}
	for _, test := range []struct{ value, code string }{{"1.500", "max_decimal_places"}, {"10000000000", "max_whole_digits"}, {"NaN", "invalid"}} {
		bad, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExpectedCost: hs.NewOptNilString(test.value)}, params)
		rejected, ok := bad.(*hs.HelpdeskTicketPatchBadRequest)
		if err != nil || !ok || len(rejected.Errors) != 1 || rejected.Errors[0].Field != "expected_cost" || rejected.Errors[0].Code != test.code {
			return hs.Ticket{}, fail("server decimal validation before persistence")
		}
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	return expected, nil
}

func checkGeneratedDecimalWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null,"service_at":null`
	clear := hs.OptNilString{}
	clear.SetToNull()
	cases := []struct {
		request       hs.TicketPatch
		wire, value   string
		present, null bool
	}{
		{hs.TicketPatch{}, "null", "", false, true}, {hs.TicketPatch{ExpectedCost: clear}, "null", "", true, true},
	}
	for _, value := range []string{"0.00", "-0.00", "0.10", "9999999999.99", "-9999999999.99"} {
		raw, _ := json.Marshal(value)
		cases = append(cases, struct {
			request       hs.TicketPatch
			wire, value   string
			present, null bool
		}{hs.TicketPatch{ExpectedCost: hs.NewOptNilString(value)}, string(raw), value, true, false})
	}
	for _, test := range cases {
		calls := 0
		httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/1/" {
				return nil, fail("decimal wire route")
			}
			wire, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			var object map[string]json.RawMessage
			if json.Unmarshal(wire, &object) != nil {
				return nil, fail("decimal wire JSON")
			}
			raw, present := object["expected_cost"]
			expectedCount := 0
			if test.present {
				expectedCount = 1
			}
			if present != test.present || len(object) != expectedCount || present && string(raw) != test.wire {
				return nil, fail("decimal request string/NULL/omission")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(base + `,"expected_cost":` + test.wire + `}`)), Request: request}, nil
		})}
		client, err := hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(httpClient))
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &test.request, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.ExpectedCost.Null != test.null || !test.null && row.ExpectedCost.Value != test.value {
			return fail("decimal generated response precision/presence")
		}
	}
	for _, suffix := range []string{`}`, `,"expected_cost":0.1}`, `,"expected_cost":true}`, `,"expected_cost":"1.5"}`, `,"expected_cost":"1.500"}`, `,"expected_cost":"01.50"}`, `,"expected_cost":"NaN"}`, `,"expected_cost":"10000000000.00"}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("decimal generated decoder admitted invalid response")
		}
	}
	for _, value := range []string{"1.5", "1.500", "NaN", "10000000000.00"} {
		request := hs.TicketPatch{ExpectedCost: hs.NewOptNilString(value)}
		if request.Validate() == nil {
			return fail("explicit generated decimal request validation")
		}
	}
	return nil
}
