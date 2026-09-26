package main

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/go-faster/jx"
	"github.com/google/uuid"
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
	if err != nil || len(initial) != 1 || initial[0].ID != target.TicketID || initial[0].Category != target.CategoryID || initial[0].Subject != "Existing ticket" || !initial[0].Priority.Null || !initial[0].Resolution.Null || !initial[0].DueAt.Null || !initial[0].Reviewed.Null || !initial[0].ServiceOn.Null || !initial[0].ServiceAt.Null || !initial[0].Elapsed.Null || !initial[0].Effort.Null || !initial[0].ExpectedCost.Null || !initial[0].ExternalReference.Null || string(initial[0].ExternalPayload) != "null" {
		return fail("helpdesk initial bare list")
	}
	seed := initial[0]
	detailResponse, err := client.HelpdeskTicketDetail(ctx, hs.HelpdeskTicketDetailParams{ID: target.TicketID})
	detail, ok := detailResponse.(*hs.TicketDetailHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(detail.Response.Ticket, seed) || detail.Response.Category.ID != target.CategoryID || detail.Response.Category.Name != "Hardware & repairs" || !detail.XGodjCsrftoken.Set || !state.ready(detail.XGodjCsrftoken.Value) {
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
	serviceOn := time.Date(2026, 9, 20, 0, 0, 0, 0, time.FixedZone("calendar", 14*3600))
	reference, err := uuid.Parse("12345678-9abc-4def-8123-456789abcdef")
	if err != nil {
		return err
	}
	createdResponse, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: "  Consumer ticket  ", Details: details, Resolution: hs.NewOptNilString(resolution), DueAt: hs.NewOptNilDateTime(dueAt), ServiceOn: hs.NewOptNilDate(serviceOn), ServiceAt: hs.NewOptNilString("00:00:00"), Effort: hs.NewOptNilFloat64(0.1), ExpectedCost: hs.NewOptNilString("0.10"), ExternalReference: hs.NewOptNilUUID(reference), ExternalPayload: jx.Raw(`{"":340282366920938463463374607431768211455}`)})
	created, ok := createdResponse.(*hs.Ticket)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || created.ID <= 0 || created.ID == target.TicketID || created.ID == target.OtherTicketID || created.Subject != "Consumer ticket" || !created.Details.Null || !created.Priority.Null || created.Resolution.Null || created.Resolution.Value != resolution || created.DueAt.Null || !created.DueAt.Value.Equal(dueAt.UTC().Truncate(time.Microsecond)) || !created.Reviewed.Null || created.ServiceOn.Null || created.ServiceOn.Value.Format("2006-01-02") != "2026-09-20" || created.ServiceAt.Null || created.ServiceAt.Value != "00:00:00" || !created.Elapsed.Null || created.Effort.Null || created.Effort.Value != 0.1 || created.ExpectedCost.Null || created.ExpectedCost.Value != "0.10" || created.Closed || created.Category != target.CategoryID {
		return fail("helpdesk create projection and defaults")
	}
	if created.ExternalReference.Null || created.ExternalReference.Value != reference || string(created.ExternalPayload) != `{"":340282366920938463463374607431768211455}` {
		return fail("helpdesk UUID create value")
	}
	if err := checkHelpdeskUniqueness(ctx, client, transport, state, seed, *created, target.OtherTicketID); err != nil {
		return err
	}
	if err := checkHelpdeskJSONSearch(ctx, client, transport, state, *created); err != nil {
		return err
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
		reviewed := hs.OptNilBool{}
		if test.subject == "Urgent priority" {
			reviewed.SetTo(true)
		} else if test.subject == "Low priority" {
			reviewed.SetTo(false)
		} else if test.subject == "Zero priority" {
			reviewed.SetToNull()
		}
		day := hs.OptNilDate{}
		if test.subject == "Urgent priority" {
			day.SetTo(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC))
		} else if test.subject == "Low priority" {
			day.SetTo(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))
		} else if test.subject == "Null priority" {
			day.SetToNull()
		}
		response, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: test.subject, Priority: priority, Resolution: text, DueAt: due, Reviewed: reviewed, ServiceOn: day})
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
		if value.Reviewed.Null != (!reviewed.Set || reviewed.Null) || (!value.Reviewed.Null && value.Reviewed.Value != reviewed.Value) {
			return fail("helpdesk nullable Boolean create presence")
		}
		if !value.ServiceAt.Null || !value.Elapsed.Null || !value.Effort.Null || !value.ExpectedCost.Null || !value.ExternalReference.Null || string(value.ExternalPayload) != "null" || value.ServiceOn.Null != (!day.Set || day.Null) || (!value.ServiceOn.Null && !value.ServiceOn.Value.Equal(day.Value)) {
			return fail("helpdesk date range, null or omission")
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
	updated, err := checkHelpdeskNullableUpdates(ctx, client, transport, state, *created, target.OtherTicketID)
	if err != nil {
		return err
	}
	updated, err = checkHelpdeskCalendarDateUpdates(ctx, client, transport, updated)
	if err != nil {
		return err
	}
	updated, err = checkHelpdeskClockTimeUpdates(ctx, client, transport, updated)
	if err != nil {
		return err
	}
	updated, err = checkHelpdeskDurationUpdates(ctx, client, transport, updated)
	if err != nil {
		return err
	}
	updated, err = checkHelpdeskFloatUpdates(ctx, client, transport, updated)
	if err != nil {
		return err
	}
	updated, err = checkHelpdeskDecimalUpdates(ctx, client, transport, updated)
	if err != nil {
		return err
	}
	updated, err = checkHelpdeskUUIDUpdates(ctx, client, transport, updated)
	if err != nil {
		return err
	}
	updated, err = checkHelpdeskJSONUpdates(ctx, client, transport, updated)
	if err != nil {
		return err
	}
	expected[1] = updated
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
	patchDenied, err := readOnly.HelpdeskTicketPatch(ctx, &hs.TicketPatch{ExternalPayload: jx.Raw(`{"denied":true}`)}, hs.HelpdeskTicketPatchParams{ID: created.ID})
	if value, ok := patchDenied.(*hs.HelpdeskTicketPatchForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("helpdesk read-only patch permission")
	}
	putDenied, err := readOnly.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{}, hs.HelpdeskTicketUpdateParams{ID: created.ID})
	if value, ok := putDenied.(*hs.HelpdeskTicketUpdateForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("helpdesk read-only put permission before input validation")
	}
	if err := checkHelpdeskServiceReports(ctx, client, readOnly, transport, readOnlyTransport, state, readOnlyState, seed.ID, created.ID, target.OtherTicketID); err != nil {
		return err
	}
	if err := checkHelpdeskLabels(ctx, client, readOnly, transport, readOnlyTransport, state, readOnlyState, target.CategoryID, target.OtherLabelID); err != nil {
		return err
	}
	if err := checkHelpdeskTicketLabels(ctx, client, readOnly, transport, state, readOnlyState, seed.ID, created.ID, target.OtherTicketID, target.OtherLabelID, target.OtherTicketLabelID); err != nil {
		return err
	}
	retainedLabel, err := checkHelpdeskTicketCollections(ctx, client, readOnly, transport, state, created.ID, target.OtherTicketID, target.OtherLabelID)
	if err != nil {
		return err
	}
	for index := range expected {
		if expected[index].ID == seed.ID || expected[index].ID == created.ID {
			expected[index].Labels = []int64{retainedLabel}
		}
	}
	return requireHelpdeskTickets(ctx, client, transport, state, expected...)
}

func helpdeskList(ctx context.Context, client *hs.Client, transport *observedTransport, state *sessionState) ([]hs.Ticket, error) {
	response, err := client.HelpdeskTicketList(ctx, hs.HelpdeskTicketListParams{})
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
		if !sameHelpdeskTicket(ticket, expected[index]) || index > 0 && tickets[index-1].ID >= ticket.ID {
			return fail("helpdesk stored integer values and ordered rows")
		}
	}
	return nil
}
