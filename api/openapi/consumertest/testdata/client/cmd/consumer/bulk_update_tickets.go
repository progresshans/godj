package main

import (
	"context"
	"net/http"
	"slices"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/go-faster/jx"
	"github.com/google/uuid"
)

func checkHelpdeskBulkUpdates(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state *sessionState, bulk []hs.Ticket, outsideTicket, outsideLabel int64, existing uuid.UUID) ([]hs.Ticket, error) {
	before, err := helpdeskList(ctx, client, transport, state)
	if err != nil {
		return nil, err
	}
	for _, test := range []struct {
		inputs      []hs.TicketBulkPatch
		field, code string
	}{
		{[]hs.TicketBulkPatch{{ID: bulk[0].ID, Closed: hs.NewOptBool(true)}, {ID: bulk[1].ID, Subject: hs.NewOptString("")}}, "subject", "blank"},
		{[]hs.TicketBulkPatch{{ID: bulk[0].ID}, {ID: bulk[0].ID}}, "id", "invalid_choice"},
		{[]hs.TicketBulkPatch{{ID: bulk[0].ID}, {ID: bulk[1].ID, Labels: []int64{outsideLabel}}}, "labels", "invalid_choice"},
		{[]hs.TicketBulkPatch{{ID: bulk[0].ID}, {ID: bulk[1].ID, ExternalReference: hs.NewOptNilUUID(existing)}}, "external_reference", "unique"},
	} {
		response, err := client.HelpdeskTicketBulkUpdate(ctx, test.inputs)
		failure, ok := response.(*hs.HelpdeskTicketBulkUpdateBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || failure.Code != "validation_error" || len(failure.Errors) != 1 || failure.Errors[0].Field != test.field || failure.Errors[0].Code != test.code || !slices.ContainsFunc(failure.Errors[0].Params, func(parameter hs.GoDjAPIErrorErrorsItemParamsItem) bool {
			return parameter.Key == "index" && parameter.Value == "1"
		}) {
			return nil, fail("generated bulk update indexed validation")
		}
		if err := requireHelpdeskTickets(ctx, client, transport, state, before...); err != nil {
			return nil, err
		}
	}
	for _, id := range []int64{outsideTicket, 1 << 60} {
		response, err := client.HelpdeskTicketBulkUpdate(ctx, []hs.TicketBulkPatch{{ID: bulk[0].ID, Closed: hs.NewOptBool(true)}, {ID: id, Closed: hs.NewOptBool(true)}})
		if failure, ok := response.(*hs.HelpdeskTicketBulkUpdateNotFound); err != nil || !ok || failure.Code != "not_found" || len(failure.Errors) != 0 {
			return nil, fail("generated bulk update complete current scope")
		}
		if err := requireHelpdeskTickets(ctx, client, transport, state, before...); err != nil {
			return nil, err
		}
	}
	response, err := readOnly.HelpdeskTicketBulkUpdate(ctx, []hs.TicketBulkPatch{{ID: bulk[0].ID, Subject: hs.NewOptString("")}})
	if failure, ok := response.(*hs.HelpdeskTicketBulkUpdateForbidden); err != nil || !ok || failure.Code != "permission_denied" {
		return nil, fail("generated bulk update permission precedes validation")
	}
	state.setInvalid(true)
	response, err = client.HelpdeskTicketBulkUpdate(ctx, []hs.TicketBulkPatch{{ID: bulk[0].ID}})
	state.setInvalid(false)
	if failure, ok := response.(*hs.HelpdeskTicketBulkUpdateForbidden); err != nil || !ok || failure.Code != "csrf_rejected" {
		return nil, fail("generated bulk update CSRF")
	}
	inputs := []hs.TicketBulkPatch{
		{ID: bulk[1].ID, Subject: hs.NewOptString("Client bulk second edited"), Closed: hs.NewOptBool(true), Labels: []int64{}},
		{ID: bulk[0].ID, Details: hs.NewOptNilString("Bulk update details"), ExternalPayload: jx.Raw(`{"":9007199254740993,"updated":true}`)},
	}
	response, err = client.HelpdeskTicketBulkUpdate(ctx, inputs)
	updated, ok := response.(*hs.HelpdeskTicketBulkUpdateOKApplicationJSON)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || len(*updated) != 2 || (*updated)[0].ID != bulk[1].ID || (*updated)[1].ID != bulk[0].ID {
		return nil, fail("generated bulk update ordered output")
	}
	first, second := (*updated)[1], (*updated)[0]
	if first.Subject != bulk[0].Subject || first.Closed || first.Details.Null || first.Details.Value != "Bulk update details" || first.ExpectedCost != bulk[0].ExpectedCost || first.Reviewed != bulk[0].Reviewed || !slices.Equal(first.Labels, bulk[0].Labels) || string(first.ExternalPayload) != `{"":9007199254740993,"updated":true}` || second.Subject != "Client bulk second edited" || !second.Closed || len(second.Labels) != 0 || second.Labels == nil {
		return nil, fail("generated bulk update per-row masks and collection presence")
	}
	digest, err := expectedPayloadDigest(first.ExternalPayload)
	if err != nil || first.ExternalPayloadDigest != digest {
		return nil, fail("generated bulk update stored JSON digest")
	}
	if inputs[0].ID != bulk[1].ID || inputs[0].Labels == nil || inputs[1].Details.Value != "Bulk update details" {
		return nil, fail("generated bulk update request ownership")
	}
	response, err = client.HelpdeskTicketBulkUpdate(ctx, []hs.TicketBulkPatch{{ID: bulk[0].ID}, {ID: bulk[1].ID}})
	noops, ok := response.(*hs.HelpdeskTicketBulkUpdateOKApplicationJSON)
	if err != nil || !ok || len(*noops) != 2 || !sameHelpdeskTicket((*noops)[0], first) || !sameHelpdeskTicket((*noops)[1], second) {
		return nil, fail("generated bulk update id-only no-op")
	}
	return []hs.Ticket{first, second}, nil
}
