package main

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"

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
	if err != nil || len(initial) != 1 || initial[0].ID != target.TicketID || initial[0].Category != target.CategoryID || initial[0].Subject != "Existing ticket" || !initial[0].Priority.Null || !initial[0].Resolution.Null || !initial[0].DueAt.Null {
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
	resolution := "First line\n" + strings.Repeat("Multiline explanation. ", 80) + "\n</textarea><script>untrusted</script>"
	dueAt := time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("caller", 9*3600))
	createdResponse, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: "  Consumer ticket  ", Details: details, Resolution: hs.NewOptNilString(resolution), DueAt: hs.NewOptNilDateTime(dueAt)})
	created, ok := createdResponse.(*hs.Ticket)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || created.ID <= 0 || created.ID == target.TicketID || created.ID == target.OtherTicketID || created.Subject != "Consumer ticket" || !created.Details.Null || !created.Priority.Null || created.Resolution.Null || created.Resolution.Value != resolution || created.DueAt.Null || !created.DueAt.Value.Equal(dueAt.UTC().Truncate(time.Microsecond)) || created.Closed || created.Category != target.CategoryID {
		return fail("helpdesk create projection and defaults")
	}
	expected := []hs.Ticket{seed, *created}
	for _, test := range []struct {
		subject string
		value   int64
		null    bool
	}{{"Urgent priority", 1, false}, {"Low priority", -1, false}, {"Zero priority", 0, false}, {"Null priority", 0, true}} {
		priority := hs.OptNilTicketCreatePriority{}
		if test.null {
			priority.SetToNull()
		} else {
			priority.SetTo(hs.TicketCreatePriority(test.value))
		}
		text := hs.OptNilString{}
		if test.subject == "Urgent priority" {
			text.SetTo("")
		} else if test.subject == "Low priority" {
			text.SetToNull()
		}
		due := hs.OptNilDateTime{}
		if test.subject == "Urgent priority" {
			due.SetTo(time.Time{})
		} else if test.subject == "Low priority" {
			due.SetTo(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))
		} else if test.subject == "Null priority" {
			due.SetToNull()
		}
		response, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: test.subject, Priority: priority, Resolution: text, DueAt: due})
		value, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusCreated || value.ID <= expected[len(expected)-1].ID || value.Subject != test.subject || value.Category != target.CategoryID || value.Closed || !value.Details.Null || value.Priority.Null != test.null || (!test.null && value.Priority.Value != test.value) {
			return fail("helpdesk integer create precision and null")
		}
		if value.Resolution.Null != (test.subject != "Urgent priority") || value.Resolution.Value != "" {
			return fail("helpdesk Text omission, null, or empty string")
		}
		if value.DueAt.Null != (!due.Set || due.Null) || (!value.DueAt.Null && !value.DueAt.Value.Equal(due.Value)) {
			return fail("helpdesk datetime range, omission, or null")
		}
		expected = append(expected, *value)
	}
	if err := requireHelpdeskTickets(ctx, client, transport, state, expected...); err != nil {
		return err
	}
	for _, value := range []int64{2, 99, math.MinInt64, math.MaxInt64} {
		request := hs.TicketCreate{Subject: "Invalid choice", Priority: hs.NewOptNilTicketCreatePriority(hs.TicketCreatePriority(value))}
		if request.Validate() == nil {
			return fail("generated choice input validator")
		}
		// The generated encoder intentionally does not call Validate. The real
		// server must independently reject a caller's explicit enum cast.
		response, err := client.HelpdeskTicketCreate(ctx, &request)
		bad, ok := response.(*hs.HelpdeskTicketCreateBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || len(bad.Errors) != 1 || bad.Errors[0].Field != "priority" || bad.Errors[0].Code != "invalid_choice" {
			return fail("server choice input rejection")
		}
	}
	if err := requireHelpdeskTickets(ctx, client, transport, state, expected...); err != nil {
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
	if err := requireHelpdeskTickets(ctx, readOnly, readOnlyTransport, readOnlyState, expected...); err != nil {
		return err
	}
	// The safe read above supplies valid CSRF credentials. Empty input confirms
	// that permission rejection precedes application validation for this role.
	deniedResponse, err := readOnly.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: ""})
	denied, ok := deniedResponse.(*hs.HelpdeskTicketCreateForbidden)
	if err != nil || !ok || readOnlyTransport.lastStatus() != http.StatusForbidden || denied.Code != "permission_denied" {
		return fail("helpdesk read-only create permission")
	}
	return requireHelpdeskTickets(ctx, client, transport, state, expected...)
}

func helpdeskList(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState) ([]hs.Ticket, error) {
	response, err := client.HelpdeskTicketList(ctx)
	list, ok := response.(*hs.HelpdeskTicketListOKHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || !list.XGodjCsrftoken.Set || !transport.capturedCSRF() || !state.ready(list.XGodjCsrftoken.Value) {
		return nil, fail("helpdesk safe bare list")
	}
	return list.Response, nil
}

func requireHelpdeskTickets(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState, expected ...hs.Ticket) error {
	tickets, err := helpdeskList(ctx, client, transport, state)
	if err != nil || len(tickets) != len(expected) {
		return fail("helpdesk created and existing rows")
	}
	for index, ticket := range tickets {
		if ticket != expected[index] || index > 0 && tickets[index-1].ID >= ticket.ID {
			return fail("helpdesk stored integer values and ordered rows")
		}
	}
	return nil
}
