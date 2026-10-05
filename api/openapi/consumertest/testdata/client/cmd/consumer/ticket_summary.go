package main

import (
	"context"
	"math"
	"reflect"
	"slices"
	"strconv"

	hs "example.com/godj-openapi-client/helpdesksession"
)

// The expected groups come from the independently retained mutation results,
// not from another summary request or a GoDj helper imported by this module.
func checkHelpdeskSummary(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state *sessionState, category int64, tickets []hs.Ticket) error {
	groups := make(map[hs.NilInt64]hs.TicketSummaryResultsItem)
	for _, ticket := range tickets {
		row := groups[ticket.Priority]
		row.Priority = ticket.Priority
		row.Total++
		if !ticket.Closed {
			row.Open++
		}
		if ticket.Priority.Null {
			row.PriorityLabel = "Not set"
		} else {
			switch ticket.Priority.Value {
			case -1:
				row.PriorityLabel = "Low"
			case 0:
				row.PriorityLabel = "Normal"
			case 1:
				row.PriorityLabel = "Urgent"
			default:
				row.PriorityLabel = "Other (" + strconv.FormatInt(ticket.Priority.Value, 10) + ")"
			}
		}
		groups[ticket.Priority] = row
	}
	var expected []hs.TicketSummaryResultsItem
	for _, row := range groups {
		expected = append(expected, row)
	}
	slices.SortFunc(expected, func(a, b hs.TicketSummaryResultsItem) int {
		if a.Open > b.Open {
			return -1
		}
		if a.Open < b.Open {
			return 1
		}
		if a.Priority.Null {
			if b.Priority.Null {
				return 0
			}
			return 1
		}
		if b.Priority.Null {
			return -1
		}
		if a.Priority.Value > b.Priority.Value {
			return -1
		}
		if a.Priority.Value < b.Priority.Value {
			return 1
		}
		return 0
	})
	for _, viewer := range []*hs.Client{client, readOnly} {
		response, err := viewer.HelpdeskTicketSummary(ctx, hs.HelpdeskTicketSummaryParams{})
		value, ok := response.(*hs.TicketSummaryHeaders)
		if err != nil || !ok || value.Response.Category.ID != category || value.Response.Category.Name != "Hardware & repairs" || value.Response.Page != 1 || value.Response.PageSize != 20 || value.Response.MinOpen != 0 || value.Response.TotalGroups != int64(len(groups)) || !reflect.DeepEqual(value.Response.Results, expected) {
			return fail("summary generated native groups and view-only admission")
		}
	}
	for _, minimum := range []int64{1, math.MaxInt64} {
		response, err := client.HelpdeskTicketSummary(ctx, hs.HelpdeskTicketSummaryParams{MinOpen: hs.NewOptInt64(minimum)})
		value, ok := response.(*hs.TicketSummaryHeaders)
		filtered := make([]hs.TicketSummaryResultsItem, 0)
		for _, row := range expected {
			if row.Open >= minimum {
				filtered = append(filtered, row)
			}
		}
		if err != nil || !ok || value.Response.MinOpen != minimum || value.Response.TotalGroups != int64(len(filtered)) || !reflect.DeepEqual(value.Response.Results, filtered) {
			return fail("summary generated HAVING and empty group array")
		}
	}
	response, err := client.HelpdeskTicketSummary(ctx, hs.HelpdeskTicketSummaryParams{P: hs.NewOptInt64(50001)})
	page, ok := response.(*hs.TicketSummaryHeaders)
	if err != nil || !ok || page.Response.Page != 50001 || page.Response.TotalGroups != int64(len(groups)) || len(page.Response.Results) != 0 || page.Response.Results == nil {
		return fail("summary generated past-end total")
	}
	for _, params := range []hs.HelpdeskTicketSummaryParams{{P: hs.NewOptInt64(0)}, {P: hs.NewOptInt64(50002)}, {MinOpen: hs.NewOptInt64(-1)}} {
		response, err := client.HelpdeskTicketSummary(ctx, params)
		if failure, ok := response.(*hs.HelpdeskTicketSummaryBadRequest); err != nil || !ok || failure.Response.Code != "validation_error" {
			return fail("summary generated invalid query")
		}
	}
	return requireHelpdeskTickets(ctx, client, transport, state, tickets...)
}
