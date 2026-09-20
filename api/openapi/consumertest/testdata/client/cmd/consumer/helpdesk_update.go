package main

import (
	"context"
	"net/http"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskNullableUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState, original hs.Ticket, outsideID int64) (hs.Ticket, error) {
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	expected := original
	expected.Reviewed, expected.Closed = hs.NewNilBool(true), true
	response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Reviewed: hs.NewOptNilBool(true), Closed: hs.NewOptBool(true)}, params)
	row, ok := response.(*hs.Ticket)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*row, expected) {
		return hs.Ticket{}, fail("helpdesk PATCH true and unrelated fields")
	}
	response, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, params)
	row, ok = response.(*hs.Ticket)
	if err != nil || !ok || !sameHelpdeskTicket(*row, expected) {
		return hs.Ticket{}, fail("helpdesk empty PATCH preserved defaults and nullable fields")
	}
	expected.Closed = false
	put, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok = put.(*hs.Ticket)
	if err != nil || !ok || !sameHelpdeskTicket(*row, expected) {
		return hs.Ticket{}, fail("helpdesk PUT default false and nullable omission")
	}
	clear := hs.OptNilBool{}
	clear.SetToNull()
	expected.Reviewed.SetToNull()
	response, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Reviewed: clear}, params)
	row, ok = response.(*hs.Ticket)
	if err != nil || !ok || !sameHelpdeskTicket(*row, expected) {
		return hs.Ticket{}, fail("helpdesk explicit null PATCH")
	}
	expected.Reviewed = hs.NewNilBool(false)
	put, err = client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject, Reviewed: hs.NewOptNilBool(false)}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok = put.(*hs.Ticket)
	if err != nil || !ok || !sameHelpdeskTicket(*row, expected) {
		return hs.Ticket{}, fail("helpdesk explicit false PUT")
	}
	put, err = client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Reviewed: hs.NewOptNilBool(true)}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	if bad, ok := put.(*hs.HelpdeskTicketUpdateBadRequest); err != nil || !ok || len(bad.Errors) != 1 || bad.Errors[0].Field != "subject" {
		return hs.Ticket{}, fail("helpdesk PUT required subject")
	}
	response, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Reviewed: hs.NewOptNilBool(true)}, hs.HelpdeskTicketPatchParams{ID: outsideID})
	if missing, ok := response.(*hs.HelpdeskTicketPatchNotFound); err != nil || !ok || missing.Code != "not_found" {
		return hs.Ticket{}, fail("helpdesk PATCH category ownership")
	}
	state.setInvalid(true)
	response, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Reviewed: hs.NewOptNilBool(true)}, params)
	state.setInvalid(false)
	if denied, ok := response.(*hs.HelpdeskTicketPatchForbidden); err != nil || !ok || denied.Code != "csrf_rejected" {
		return hs.Ticket{}, fail("helpdesk PATCH invalid CSRF")
	}
	response, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, params)
	row, ok = response.(*hs.Ticket)
	if err != nil || !ok || !sameHelpdeskTicket(*row, expected) {
		return hs.Ticket{}, fail("helpdesk rejected writes changed stored state")
	}
	return expected, nil
}
