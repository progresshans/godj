package main

import (
	"context"
	"net/http"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskSession(ctx context.Context, target endpoint) error {
	httpClient, transport, state, err := newSessionClient(target, target.Session, "/api/tickets/")
	if err != nil {
		return err
	}
	defer transport.base.CloseIdleConnections()
	client, err := hs.NewClient(target.URL, helpdeskSessionSource{state}, hs.WithClient(httpClient))
	if err != nil {
		return fail("helpdesk client setup")
	}
	initial, err := helpdeskList(ctx, client, transport, state)
	if err != nil || len(initial) != 1 || initial[0].ID != target.TicketID || initial[0].Category != target.CategoryID || initial[0].Subject != "Existing ticket" {
		return fail("helpdesk initial bare list")
	}
	seed := initial[0]
	detailResponse, err := client.HelpdeskTicketDetail(ctx, hs.HelpdeskTicketDetailParams{ID: target.TicketID})
	detail, ok := detailResponse.(*hs.TicketDetailHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || detail.Response.Ticket != seed || detail.Response.Category.ID != target.CategoryID || detail.Response.Category.Name != "Hardware & repairs" || !detail.XGodjCsrftoken.Set || !state.ready(detail.XGodjCsrftoken.Value) {
		return fail("helpdesk nested detail")
	}
	outside, err := client.HelpdeskTicketDetail(ctx, hs.HelpdeskTicketDetailParams{ID: target.OtherTicketID})
	notFound, ok := outside.(*hs.GoDjAPIErrorHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusNotFound || notFound.Response.Code != "not_found" || !notFound.XGodjCsrftoken.Set || !state.ready(notFound.XGodjCsrftoken.Value) {
		return fail("helpdesk category isolation")
	}
	details := hs.OptNilString{}
	details.SetToNull()
	createdResponse, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: "  Consumer ticket  ", Details: details})
	created, ok := createdResponse.(*hs.Ticket)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || created.ID <= 0 || created.ID == target.TicketID || created.ID == target.OtherTicketID || created.Subject != "Consumer ticket" || !created.Details.Null || created.Closed || created.Category != target.CategoryID {
		return fail("helpdesk create projection and defaults")
	}
	if err := requireHelpdeskTickets(ctx, client, transport, state, seed, *created); err != nil {
		return err
	}
	readOnlyHTTP, readOnlyTransport, readOnlyState, err := newSessionClient(target, target.ReadOnlySession, "/api/tickets/")
	if err != nil {
		return err
	}
	defer readOnlyTransport.base.CloseIdleConnections()
	readOnly, err := hs.NewClient(target.URL, helpdeskSessionSource{readOnlyState}, hs.WithClient(readOnlyHTTP))
	if err != nil {
		return fail("helpdesk read-only client setup")
	}
	if err := requireHelpdeskTickets(ctx, readOnly, readOnlyTransport, readOnlyState, seed, *created); err != nil {
		return err
	}
	// The safe read above supplies valid CSRF credentials. Empty input confirms
	// that permission rejection precedes application validation for this role.
	deniedResponse, err := readOnly.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: ""})
	denied, ok := deniedResponse.(*hs.HelpdeskTicketCreateForbidden)
	if err != nil || !ok || readOnlyTransport.lastStatus() != http.StatusForbidden || denied.Code != "permission_denied" {
		return fail("helpdesk read-only create permission")
	}
	return requireHelpdeskTickets(ctx, client, transport, state, seed, *created)
}

func helpdeskList(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState) ([]hs.Ticket, error) {
	response, err := client.HelpdeskTicketList(ctx)
	list, ok := response.(*hs.HelpdeskTicketListOKHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || !list.XGodjCsrftoken.Set || !transport.capturedCSRF() || !state.ready(list.XGodjCsrftoken.Value) {
		return nil, fail("helpdesk safe bare list")
	}
	return list.Response, nil
}

func requireHelpdeskTickets(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState, seed, created hs.Ticket) error {
	tickets, err := helpdeskList(ctx, client, transport, state)
	if err != nil || len(tickets) != 2 || tickets[0] != seed || tickets[1] != created || tickets[0].ID >= tickets[1].ID {
		return fail("helpdesk created and existing rows")
	}
	return nil
}
