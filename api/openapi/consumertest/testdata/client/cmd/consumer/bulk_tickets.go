package main

import (
	"context"
	"net/http"
	"slices"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/go-faster/jx"
	"github.com/google/uuid"
)

func checkHelpdeskBulkTickets(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state *sessionState, category, label, outsideLabel int64, existing uuid.UUID) ([]hs.Ticket, error) {
	before, err := helpdeskList(ctx, client, transport, state)
	if err != nil {
		return nil, err
	}
	duplicate := uuid.MustParse("71452167-cccc-4aaa-8444-019028364555")
	for _, test := range []struct {
		inputs             []hs.TicketCreate
		field, code, index string
	}{
		{[]hs.TicketCreate{{Subject: "valid"}, {Subject: ""}}, "subject", "blank", "1"},
		{[]hs.TicketCreate{{Subject: "valid"}, {Subject: "existing", ExternalReference: hs.NewOptNilUUID(existing)}}, "external_reference", "unique", "1"},
		{[]hs.TicketCreate{{Subject: "one", ExternalReference: hs.NewOptNilUUID(duplicate)}, {Subject: "two", ExternalReference: hs.NewOptNilUUID(duplicate)}}, "external_reference", "unique", "1"},
		{[]hs.TicketCreate{{Subject: "valid"}, {Subject: "outside", Labels: []int64{outsideLabel}}}, "labels", "invalid_choice", "1"},
		{[]hs.TicketCreate{}, "__all__", "invalid_count", ""},
	} {
		response, err := client.HelpdeskTicketBulkCreate(ctx, test.inputs)
		failure, ok := response.(*hs.HelpdeskTicketBulkCreateBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || failure.Code != "validation_error" || len(failure.Errors) != 1 || failure.Errors[0].Field != test.field || failure.Errors[0].Code != test.code {
			return nil, fail("generated bulk indexed validation")
		}
		if test.index != "" && !slices.ContainsFunc(failure.Errors[0].Params, func(parameter hs.GoDjAPIErrorErrorsItemParamsItem) bool {
			return parameter.Key == "index" && parameter.Value == test.index
		}) {
			return nil, fail("generated bulk row index parameter")
		}
		if err := requireHelpdeskTickets(ctx, client, transport, state, before...); err != nil {
			return nil, err
		}
	}
	denied, err := readOnly.HelpdeskTicketBulkCreate(ctx, []hs.TicketCreate{{Subject: ""}})
	if failure, ok := denied.(*hs.HelpdeskTicketBulkCreateForbidden); err != nil || !ok || failure.Code != "permission_denied" {
		return nil, fail("generated bulk permission precedes validation")
	}
	state.setInvalid(true)
	denied, err = client.HelpdeskTicketBulkCreate(ctx, []hs.TicketCreate{{Subject: "forbidden"}})
	state.setInvalid(false)
	if failure, ok := denied.(*hs.HelpdeskTicketBulkCreateForbidden); err != nil || !ok || failure.Code != "csrf_rejected" {
		return nil, fail("generated bulk CSRF")
	}
	inputs := []hs.TicketCreate{
		{Subject: "  Client bulk first  ", Labels: []int64{label, label}, ExpectedCost: hs.NewOptNilString("12.30"), Reviewed: hs.NewOptNilBool(false), ServiceAt: hs.NewOptNilString("00:00:00"), ExternalPayload: jx.Raw(`{"":9007199254740993}`)},
		{Subject: "Client bulk second", Labels: []int64{label}},
	}
	response, err := client.HelpdeskTicketBulkCreate(ctx, inputs)
	created, ok := response.(*hs.HelpdeskTicketBulkCreateCreatedApplicationJSON)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || len(*created) != 2 || (*created)[0].ID <= before[len(before)-1].ID || (*created)[1].ID <= (*created)[0].ID {
		return nil, fail("generated bulk ordered created identities")
	}
	for index, ticket := range *created {
		if ticket.Category != category || !slices.Equal(ticket.Labels, []int64{label}) || ticket.Closed || ticket.Subject != []string{"Client bulk first", "Client bulk second"}[index] {
			return nil, fail("generated bulk scope defaults or membership")
		}
		digest, err := expectedPayloadDigest(ticket.ExternalPayload)
		if err != nil || ticket.ExternalPayloadDigest != digest {
			return nil, fail("generated bulk stored payload digest")
		}
	}
	first := (*created)[0]
	if first.Reviewed.Null || first.Reviewed.Value || first.ExpectedCost.Null || first.ExpectedCost.Value != "12.30" || first.ServiceAt.Null || first.ServiceAt.Value != "00:00:00" || string(first.ExternalPayload) != `{"":9007199254740993}` || !(*created)[1].Reviewed.Null {
		return nil, fail("generated bulk scalar fidelity")
	}
	if inputs[0].Subject != "  Client bulk first  " || !slices.Equal(inputs[0].Labels, []int64{label, label}) {
		return nil, fail("generated bulk modified request objects")
	}
	return []hs.Ticket(*created), nil
}
