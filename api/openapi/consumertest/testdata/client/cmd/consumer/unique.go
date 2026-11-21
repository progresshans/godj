package main

import (
	"context"
	"net/http"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskUniqueness(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState, seed, original hs.Ticket, outsideID int64) error {
	if original.ExternalReference.Null {
		return fail("uniqueness fixture has no reference")
	}
	reference := hs.NewOptNilUUID(original.ExternalReference.Value)
	created, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: "rejected duplicate", ExternalReference: reference})
	badCreate, ok := created.(*hs.HelpdeskTicketCreateBadRequest)
	if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || badCreate.Code != "validation_error" || len(badCreate.Errors) != 1 || badCreate.Errors[0].Field != "external_reference" || badCreate.Errors[0].Code != "unique" || len(badCreate.Errors[0].Params) != 0 {
		return fail("generated duplicate create diagnostic")
	}
	updated, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Subject: hs.NewOptString("must remain unchanged"), ExternalReference: reference}, hs.HelpdeskTicketPatchParams{ID: seed.ID})
	badPatch, ok := updated.(*hs.HelpdeskTicketPatchBadRequest)
	if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || badPatch.Code != "validation_error" || len(badPatch.Errors) != 1 || badPatch.Errors[0].Field != "external_reference" || badPatch.Errors[0].Code != "unique" {
		return fail("generated duplicate update diagnostic")
	}
	detail, err := client.HelpdeskTicketDetail(ctx, hs.HelpdeskTicketDetailParams{ID: seed.ID})
	stored, ok := detail.(*hs.TicketDetailHeaders)
	if err != nil || !ok || !sameHelpdeskTicket(stored.Response.Ticket, seed) || !stored.XGodjCsrftoken.Set || !state.ready(stored.XGodjCsrftoken.Value) {
		return fail("duplicate update changed unrelated fields")
	}
	for _, closed := range []bool{!original.Closed, original.Closed} {
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalReference: reference, Closed: hs.NewOptBool(closed)}, hs.HelpdeskTicketPatchParams{ID: original.ID})
		row, ok := response.(*hs.Ticket)
		expected := original
		expected.Closed = closed
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*row, expected) {
			return fail("self exclusion during another field change")
		}
	}
	response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalReference: reference}, hs.HelpdeskTicketPatchParams{ID: outsideID})
	if _, ok := response.(*hs.HelpdeskTicketPatchNotFound); err != nil || !ok {
		return fail("uniqueness bypassed target authorization")
	}
	return nil
}
