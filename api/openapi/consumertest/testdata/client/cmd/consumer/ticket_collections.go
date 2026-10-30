package main

import (
	"context"
	"slices"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskTicketCollections(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state *sessionState, owner, outsideTicket, outsideLabel int64) (int64, error) {
	labels, err := client.HelpdeskLabelList(ctx, hs.HelpdeskLabelListParams{})
	page, ok := labels.(*hs.LabelListHeaders)
	if err != nil || !ok || len(page.Response.Items) != 1 || !page.XGodjCsrftoken.Set || !state.ready(page.XGodjCsrftoken.Value) {
		return 0, fail("collection label fixture")
	}
	retained := page.Response.Items[0].ID
	created, err := client.HelpdeskLabelCreate(ctx, &hs.LabelCreate{Name: "Client temporary collection label"})
	temporary, ok := created.(*hs.Label)
	if err != nil || !ok {
		return 0, fail("collection temporary label")
	}
	both := []int64{retained, temporary.ID}
	slices.Sort(both)
	newResult, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: "Client temporary collection ticket", Labels: []int64{temporary.ID, retained, temporary.ID}})
	newTicket, ok := newResult.(*hs.Ticket)
	if err != nil || !ok || transport.lastStatus() != 201 || !slices.Equal(newTicket.Labels, both) {
		return 0, fail("generated collection create and deduplication")
	}
	if deleted, err := client.HelpdeskTicketDelete(ctx, hs.HelpdeskTicketDeleteParams{ID: newTicket.ID}); err != nil {
		return 0, err
	} else if _, ok := deleted.(*hs.HelpdeskTicketDeleteNoContent); !ok {
		return 0, fail("collection temporary ticket cleanup")
	}
	initial, err := client.HelpdeskTicketDetail(ctx, hs.HelpdeskTicketDetailParams{ID: owner})
	detail, ok := initial.(*hs.TicketDetailHeaders)
	if err != nil || !ok || len(detail.Response.Ticket.Labels) != 0 || !detail.XGodjCsrftoken.Set || !state.ready(detail.XGodjCsrftoken.Value) {
		return 0, fail("collection owner fixture")
	}
	expected := detail.Response.Ticket
	patch := func(keys []int64) error {
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Labels: keys}, hs.HelpdeskTicketPatchParams{ID: owner})
		value, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != 200 || !sameHelpdeskTicket(*value, expected) {
			return fail("generated collection replacement or omission")
		}
		return nil
	}
	links := func() (map[int64]int64, error) {
		response, err := client.HelpdeskTicketLabelList(ctx, hs.HelpdeskTicketLabelListParams{Limit: hs.NewOptInt64(100)})
		page, ok := response.(*hs.TicketLabelListHeaders)
		if err != nil || !ok || !page.XGodjCsrftoken.Set || !state.ready(page.XGodjCsrftoken.Value) {
			return nil, fail("collection link identity query")
		}
		result := map[int64]int64{}
		for _, link := range page.Response.Items {
			if link.Ticket == owner {
				if result[link.Label] != 0 {
					return nil, fail("collection stored duplicate pair")
				}
				result[link.Label] = link.ID
			}
		}
		return result, nil
	}
	expected.Labels = both
	if err := patch([]int64{temporary.ID, retained, temporary.ID}); err != nil {
		return 0, err
	}
	before, err := links()
	if err != nil || len(before) != 2 {
		return 0, fail("collection desired set persisted")
	}
	if err := patch(nil); err != nil {
		return 0, err
	}
	put, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: expected.Subject, Closed: hs.NewOptBool(expected.Closed)}, hs.HelpdeskTicketUpdateParams{ID: owner})
	if value, ok := put.(*hs.Ticket); err != nil || !ok || !sameHelpdeskTicket(*value, expected) {
		return 0, fail("generated PUT preserves omitted collection")
	}
	if err := patch([]int64{retained, temporary.ID, retained}); err != nil {
		return 0, err
	}
	after, err := links()
	if err != nil || len(after) != 2 || after[retained] != before[retained] || after[temporary.ID] != before[temporary.ID] {
		return 0, fail("generated collection retains link identity")
	}
	bad, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Subject: hs.NewOptString("must not change"), Labels: []int64{retained, outsideLabel}}, hs.HelpdeskTicketPatchParams{ID: owner})
	if value, ok := bad.(*hs.HelpdeskTicketPatchBadRequest); err != nil || !ok || len(value.Errors) != 1 || value.Errors[0].Field != "labels" || value.Errors[0].Code != "invalid_choice" {
		return 0, fail("generated collection rejects whole foreign set")
	}
	if err := patch(nil); err != nil {
		return 0, err
	}
	foreign, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Labels: both}, hs.HelpdeskTicketPatchParams{ID: outsideTicket})
	if value, ok := foreign.(*hs.HelpdeskTicketPatchNotFound); err != nil || !ok || value.Code != "not_found" {
		return 0, fail("generated collection owner scope")
	}
	denied, err := readOnly.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Labels: both}, hs.HelpdeskTicketPatchParams{ID: owner})
	if value, ok := denied.(*hs.HelpdeskTicketPatchForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return 0, fail("generated collection mutation permission")
	}
	state.setInvalid(true)
	deniedCSRF, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Labels: []int64{}}, hs.HelpdeskTicketPatchParams{ID: owner})
	state.setInvalid(false)
	if value, ok := deniedCSRF.(*hs.HelpdeskTicketPatchForbidden); err != nil || !ok || value.Code != "csrf_rejected" {
		return 0, fail("generated collection CSRF")
	}
	expected.Labels = []int64{}
	if err := patch([]int64{}); err != nil {
		return 0, err
	}
	if current, err := links(); err != nil || len(current) != 0 {
		return 0, fail("generated empty array clears all links")
	}
	expected.Labels = []int64{retained}
	if err := patch([]int64{retained, retained}); err != nil {
		return 0, err
	}
	if current, err := links(); err != nil || len(current) != 1 || current[retained] <= before[retained] {
		return 0, fail("collection clear and recreate identity")
	}
	deleted, err := client.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: temporary.ID})
	if _, ok := deleted.(*hs.HelpdeskLabelDeleteNoContent); err != nil || !ok {
		return 0, fail("collection temporary label cleanup")
	}
	return retained, nil
}
