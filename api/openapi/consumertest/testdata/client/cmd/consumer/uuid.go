package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/google/uuid"
)

func checkHelpdeskUUIDUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(request hs.TicketPatch) error {
		response, err := client.HelpdeskTicketPatch(ctx, &request, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *row != expected {
			return fail("UUID PATCH value/null/omission")
		}
		return nil
	}
	expected.ExternalReference = hs.NewNilUUID(uuid.UUID{})
	if err := patch(hs.TicketPatch{ExternalReference: hs.NewOptNilUUID(uuid.UUID{})}); err != nil {
		return hs.Ticket{}, err
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok := response.(*hs.Ticket)
	if err != nil || !ok || *row != expected {
		return hs.Ticket{}, fail("UUID PUT omission")
	}
	clear := hs.OptNilUUID{}
	clear.SetToNull()
	expected.ExternalReference.SetToNull()
	if err := patch(hs.TicketPatch{ExternalReference: clear}); err != nil {
		return hs.Ticket{}, err
	}
	for _, raw := range []string{"00000000-0000-0001-0000-000000000000", "7fffffff-ffff-ffff-ffff-ffffffffffff", "80000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff", "12345678-9abc-4def-8123-456789abcdef"} {
		value, err := uuid.Parse(raw)
		if err != nil {
			return hs.Ticket{}, err
		}
		expected.ExternalReference = hs.NewNilUUID(value)
		if err := patch(hs.TicketPatch{ExternalReference: hs.NewOptNilUUID(value)}); err != nil {
			return hs.Ticket{}, err
		}
	}
	return expected, nil
}

func checkGeneratedUUIDWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"expected_cost":null,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null,"service_at":null`
	clear := hs.OptNilUUID{}
	clear.SetToNull()
	type sample struct {
		request       hs.TicketPatch
		wire          string
		value         uuid.UUID
		present, null bool
	}
	cases := []sample{{hs.TicketPatch{}, "null", uuid.UUID{}, false, true}, {hs.TicketPatch{ExternalReference: clear}, "null", uuid.UUID{}, true, true}}
	for _, raw := range []string{"00000000-0000-0000-0000-000000000000", "00000000-0000-0001-0000-000000000000", "80000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		value, err := uuid.Parse(raw)
		if err != nil {
			return err
		}
		wire, _ := json.Marshal(raw)
		cases = append(cases, sample{hs.TicketPatch{ExternalReference: hs.NewOptNilUUID(value)}, string(wire), value, true, false})
	}
	for _, test := range cases {
		calls := 0
		httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/1/" {
				return nil, fail("UUID wire route")
			}
			wire, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			var object map[string]json.RawMessage
			if json.Unmarshal(wire, &object) != nil {
				return nil, fail("UUID wire JSON")
			}
			raw, present := object["external_reference"]
			count := 0
			if test.present {
				count = 1
			}
			if present != test.present || len(object) != count || present && string(raw) != test.wire {
				return nil, fail("UUID request canonical string/NULL/omission")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(base + `,"external_reference":` + test.wire + `}`)), Request: request}, nil
		})}
		client, err := hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(httpClient))
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &test.request, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.ExternalReference.Null != test.null || !test.null && row.ExternalReference.Value != test.value {
			return fail("UUID generated response lost bytes or presence")
		}
	}
	for _, suffix := range []string{`}`, `,"external_reference":0}`, `,"external_reference":true}`, `,"external_reference":{}}`, `,"external_reference":[]}`, `,"external_reference":"invalid"}`, `,"external_reference":"0000000000000000000000000000000"}`, `,"external_reference":"g0000000-0000-0000-0000-000000000000"}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("UUID generated decoder admitted missing or malformed response")
		}
	}
	return nil
}
