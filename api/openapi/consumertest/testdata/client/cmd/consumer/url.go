package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskURLUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(request hs.TicketPatch) error {
		response, err := client.HelpdeskTicketPatch(ctx, &request, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*row, expected) {
			return fail("URL PATCH value/null/omission")
		}
		return nil
	}
	for _, text := range []string{"", "http://localhost:99999/a", "ftp://user:pass@Example.com/path", "HTTPS://例え.テスト/Path?x=%zz"} {
		expected.ExternalURL = hs.NewNilString(text)
		request := hs.TicketPatch{ExternalURL: hs.NewOptNilString(strings.Repeat(" ", 220) + text + "  ")}
		// Length/grammar apply after trimming on the server. The client must
		// permit the original input even when its raw length exceeds 200.
		if request.Validate() != nil {
			return hs.Ticket{}, fail("URL client constrained the raw input before normalization")
		}
		if err := patch(request); err != nil {
			return hs.Ticket{}, err
		}
		if err := patch(hs.TicketPatch{}); err != nil {
			return hs.Ticket{}, err
		}
		response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || !sameHelpdeskTicket(*row, expected) {
			return hs.Ticket{}, fail("URL PUT omission")
		}
	}
	for _, text := range []string{"example.com", "//example.com", "javascript://example.com", "https://a.c", "https://example.com/" + strings.Repeat("x", 201)} {
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalURL: hs.NewOptNilString(text)}, params)
		bad, ok := response.(*hs.HelpdeskTicketPatchBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || len(bad.Errors) == 0 || bad.Errors[0].Field != "external_url" {
			return hs.Ticket{}, fail("URL server input rejection")
		}
		if err := patch(hs.TicketPatch{}); err != nil {
			return hs.Ticket{}, err
		}
	}
	clear := hs.OptNilString{}
	clear.SetToNull()
	expected.ExternalURL.SetToNull()
	if err := patch(hs.TicketPatch{ExternalURL: clear}); err != nil {
		return hs.Ticket{}, err
	}
	expected.ExternalURL = hs.NewNilString("HTTPS://例え.テスト/Path?x=%zz")
	if err := patch(hs.TicketPatch{ExternalURL: hs.NewOptNilString(expected.ExternalURL.Value)}); err != nil {
		return hs.Ticket{}, err
	}
	return expected, nil
}

func checkGeneratedURLWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"labels":[],"external_payload":null,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null,"service_at":null`
	clear := hs.OptNilString{}
	clear.SetToNull()
	for _, input := range []hs.OptNilString{{}, clear, hs.NewOptNilString(""), hs.NewOptNilString("  HTTPS://Example.COM  "), hs.NewOptNilString("javascript:alert(1)"), hs.NewOptNilString("not-an-url")} {
		wire, output := `{}`, "null"
		if input.Set {
			if !input.Null {
				value, _ := json.Marshal(input.Value)
				output = string(value)
			}
			wire = `{"external_url":` + output + `}`
		}
		calls := 0
		client, err := nullableBooleanWireClient(base+`,"external_url":`+output+`}`, wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalURL: input}, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.ExternalURL.Null != (!input.Set || input.Null) || input.Set && !input.Null && row.ExternalURL.Value != input.Value {
			return fail("URL generated wire rewrote values or validated stored grammar")
		}
	}
	for _, suffix := range []string{`}`, `,"external_url":false}`, `,"external_url":0}`, `,"external_url":[]}`, `,"external_url":{}}`, `,"external_url":"` + strings.Repeat("x", 201) + `"}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("URL generated decoder admitted missing, wrong type or oversized output")
		}
	}
	return nil
}
