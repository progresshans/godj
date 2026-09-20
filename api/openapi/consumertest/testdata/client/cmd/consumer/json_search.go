package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/go-faster/jx"
)

func checkHelpdeskJSONSearch(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState, original hs.Ticket) error {
	patch := func(raw jx.Raw) error {
		expected := original
		expected.ExternalPayload = raw
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalPayload: raw}, hs.HelpdeskTicketPatchParams{ID: original.ID})
		actual, ok := response.(*hs.Ticket)
		if err != nil {
			return fail("JSON search fixture PATCH request")
		}
		if !ok || transport.lastStatus() != http.StatusOK {
			return fail("JSON search fixture PATCH status " + strconv.Itoa(transport.lastStatus()))
		}
		if !sameHelpdeskTicket(*actual, expected) {
			return fail("JSON search fixture PATCH preserved fields")
		}
		return nil
	}
	if err := patch(jx.Raw(`{"source":"Partner%_API"}`)); err != nil {
		return err
	}
	for _, tc := range []struct {
		params hs.HelpdeskTicketListParams
		count  int
	}{
		{hs.HelpdeskTicketListParams{Source: hs.NewOptString("partner")}, 1},
		{hs.HelpdeskTicketListParams{Search: hs.NewOptString("partner%_")}, 1},
		{hs.HelpdeskTicketListParams{Search: hs.NewOptString(original.Subject), Source: hs.NewOptString("%_")}, 1},
		{hs.HelpdeskTicketListParams{Search: hs.NewOptString("not-a-ticket"), Source: hs.NewOptString("partner")}, 0},
		{hs.HelpdeskTicketListParams{Source: hs.NewOptString("missing")}, 0},
	} {
		response, err := client.HelpdeskTicketList(ctx, tc.params)
		rows, ok := response.(*hs.HelpdeskTicketListOKHeaders)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || !rows.XGodjCsrftoken.Set || !state.ready(rows.XGodjCsrftoken.Value) || len(rows.Response) != tc.count || tc.count > 0 && rows.Response[0].ID != original.ID {
			return fail("generated JSON search/source query parameters")
		}
	}
	for _, value := range []string{"\x00", strings.Repeat("a", 65)} {
		response, err := client.HelpdeskTicketList(ctx, hs.HelpdeskTicketListParams{Source: hs.NewOptString(value)})
		failure, ok := response.(*hs.GoDjAPIErrorHeaders)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || failure.Response.Code != "validation_error" || !failure.XGodjCsrftoken.Set || !state.ready(failure.XGodjCsrftoken.Value) {
			return fail("generated invalid JSON source parameter")
		}
	}
	return patch(original.ExternalPayload)
}
