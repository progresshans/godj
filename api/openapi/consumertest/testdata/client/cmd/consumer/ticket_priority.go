package main

import (
	"context"
	"net/http"
	"slices"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskPriorityRaise(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state *sessionState, outside int64) ([]hs.Ticket, error) {
	inputs := []hs.TicketCreate{
		{Subject: "Client priority Low", Priority: hs.NewOptNilTicketCreatePriority(hs.TicketCreatePriority(-1))},
		{Subject: "Client priority Normal", Priority: hs.NewOptNilTicketCreatePriority(hs.TicketCreatePriority(0)), Closed: hs.NewOptBool(true)},
		{Subject: "Client priority Unset"},
		{Subject: "Client priority Urgent", Priority: hs.NewOptNilTicketCreatePriority(hs.TicketCreatePriority(1))},
	}
	result, err := client.HelpdeskTicketBulkCreate(ctx, inputs)
	created, ok := result.(*hs.HelpdeskTicketBulkCreateCreatedApplicationJSON)
	if err != nil || !ok || len(*created) != 4 {
		return nil, fail("priority fixture real creation")
	}
	rows := []hs.Ticket(*created)
	before, err := helpdeskList(ctx, client, transport, state)
	if err != nil {
		return nil, err
	}
	denied, err := readOnly.HelpdeskTicketRaisePriority(ctx, &hs.TicketPriorityRaise{Ids: []int64{0}})
	if failure, ok := denied.(*hs.HelpdeskTicketRaisePriorityForbidden); err != nil || !ok || failure.Code != "permission_denied" {
		return nil, fail("priority permission before invalid selection")
	}
	state.setInvalid(true)
	denied, err = client.HelpdeskTicketRaisePriority(ctx, &hs.TicketPriorityRaise{Ids: []int64{rows[0].ID}})
	state.setInvalid(false)
	if failure, ok := denied.(*hs.HelpdeskTicketRaisePriorityForbidden); err != nil || !ok || failure.Code != "csrf_rejected" {
		return nil, fail("priority CSRF boundary")
	}
	for _, ids := range [][]int64{{rows[0].ID, rows[0].ID}, {0}, {-1}, {}} {
		response, err := client.HelpdeskTicketRaisePriority(ctx, &hs.TicketPriorityRaise{Ids: ids})
		if failure, ok := response.(*hs.HelpdeskTicketRaisePriorityBadRequest); err != nil || !ok || failure.Code != "validation_error" {
			return nil, fail("priority selection rejection")
		}
		if err := requireHelpdeskTickets(ctx, client, transport, state, before...); err != nil {
			return nil, err
		}
	}
	for _, id := range []int64{outside, 1 << 60} {
		response, err := client.HelpdeskTicketRaisePriority(ctx, &hs.TicketPriorityRaise{Ids: []int64{rows[0].ID, id}})
		if failure, ok := response.(*hs.HelpdeskTicketRaisePriorityNotFound); err != nil || !ok || failure.Code != "not_found" || len(failure.Errors) != 0 {
			return nil, fail("priority all-selected current scope")
		}
		if err := requireHelpdeskTickets(ctx, client, transport, state, before...); err != nil {
			return nil, err
		}
	}
	ids := []int64{rows[3].ID, rows[2].ID, rows[1].ID, rows[0].ID}
	input := &hs.TicketPriorityRaise{Ids: slices.Clone(ids)}
	for turn := 0; turn < 3; turn++ {
		response, err := client.HelpdeskTicketRaisePriority(ctx, input)
		values, ok := response.(*hs.HelpdeskTicketRaisePriorityOKApplicationJSON)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || len(*values) != 4 || !slices.Equal(input.Ids, ids) {
			return nil, fail("priority generated complete response or input ownership")
		}
		for index, value := range *values {
			expected := rows[3-index]
			expected.Priority = hs.NewNilInt64(1)
			if turn == 0 && (index == 1 || index == 3) {
				expected.Priority = hs.NewNilInt64(0)
			}
			if value.ID != ids[index] || !sameHelpdeskTicket(value, expected) {
				return nil, fail("priority current arithmetic, null policy or unrelated field preservation")
			}
			rows[3-index] = value
		}
	}
	return rows, nil
}
