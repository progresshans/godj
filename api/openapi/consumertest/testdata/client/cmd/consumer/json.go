package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/go-faster/jx"
)

func sameHelpdeskTicket(left, right hs.Ticket) bool {
	// The generated any-JSON field is owned raw bytes, making Ticket no longer
	// Go-comparable. Compare every field, retaining exact JSON numeric spelling.
	return reflect.DeepEqual(left, right)
}

func checkHelpdeskJSONUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(raw jx.Raw) error {
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalPayload: raw}, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*row, expected) {
			return fail("JSON PATCH exact value/null/omission")
		}
		return nil
	}
	for _, raw := range []string{`null`, `false`, `0`, `1.00`, `1e400`, `1e-400`, `9007199254740993.00`, `[]`, `{}`, `""`, `"{\"a\":1}"`, `{"":{"__proto__":{"constructor":false}},"a":[null,9007199254740993]}`, `{"":340282366920938463463374607431768211455,"nested":[null,false,"<script>data</script>"]}`} {
		expected.ExternalPayload = jx.Raw(raw)
		if err := patch(jx.Raw(raw)); err != nil {
			return hs.Ticket{}, err
		}
		if err := patch(nil); err != nil {
			return hs.Ticket{}, err
		}
		response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || !sameHelpdeskTicket(*row, expected) {
			return hs.Ticket{}, fail("JSON PUT omission")
		}
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `"\u0000"`, `{"\u0000":1}`, `"\ud800"`, `NaN`, strings.Repeat("[", 14) + "0" + strings.Repeat("]", 14)} {
		// Raw client values are not a substitute for the server's duplicate,
		// Unicode and resource validation. No hand-written field codec is used.
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalPayload: jx.Raw(raw)}, params)
		if _, ok := response.(*hs.HelpdeskTicketPatchBadRequest); err != nil || !ok || transport.lastStatus() != http.StatusBadRequest {
			return hs.Ticket{}, fail("JSON raw input server rejection")
		}
	}
	if err := patch(nil); err != nil {
		return hs.Ticket{}, err
	}
	return expected, nil
}

func checkGeneratedJSONWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null,"service_at":null`
	for _, raw := range []string{"", `null`, `false`, `0`, `1.00`, `1e400`, `1e-400`, `9007199254740993.00`, `340282366920938463463374607431768211455`, `[]`, `{}`, `""`, `"{\"a\":1}"`, `{"":{"__proto__":[null,false]}}`} {
		calls := 0
		responseValue := raw
		if responseValue == "" {
			responseValue = "null"
		}
		client, err := hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/1/" {
				return nil, fail("JSON wire route")
			}
			wire, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(wire, &fields) != nil {
				return nil, fail("JSON request wire")
			}
			value, present := fields["external_payload"]
			count := 0
			if raw != "" {
				count = 1
			}
			if present != (raw != "") || len(fields) != count || present && string(value) != raw {
				return nil, fail("JSON request lost exact bytes, null or omission")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(base + `,"external_payload":` + responseValue + `}`)), Request: request}, nil
		})}))
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalPayload: jx.Raw(raw)}, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || string(row.ExternalPayload) != responseValue {
			return fail("JSON response decoder lost token precision or null presence")
		}
	}
	for _, suffix := range []string{`}`, `,"external_payload":NaN}`, `,"external_payload":{]}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("JSON response decoder admitted missing or malformed value")
		}
	}
	return nil
}
