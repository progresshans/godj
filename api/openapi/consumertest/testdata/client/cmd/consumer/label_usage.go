package main

import (
	"context"
	"math/big"
	"net/http"
	"reflect"
	"slices"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
)

// Expected metrics come from retained ticket responses and label records.
// This independent module imports no GoDj aggregation or serialization code.
func checkHelpdeskLabelUsage(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, tickets []hs.Ticket) error {
	created, err := client.HelpdeskLabelCreate(ctx, &hs.LabelCreate{Name: "Client unused usage label"})
	unused, ok := created.(*hs.Label)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated {
		return fail("label usage unused fixture creation")
	}
	response, err := client.HelpdeskLabelList(ctx, hs.HelpdeskLabelListParams{Limit: hs.NewOptInt64(100)})
	labels, ok := response.(*hs.LabelListHeaders)
	if err != nil || !ok || labels.Response.Count != 2 || len(labels.Response.Items) != 2 || !labels.XGodjCsrftoken.Set || !state.ready(labels.XGodjCsrftoken.Value) {
		return fail("label usage source label records")
	}
	before := labels.Response
	expected := make([]hs.LabelUsageResultsItem, 0, len(before.Items))
	for _, label := range before.Items {
		row := hs.LabelUsageResultsItem{ID: label.ID, Name: label.Name, ExpectedCostTotal: hs.NilString{Null: true}, EffortAverage: hs.NilFloat64{Null: true}, ElapsedTotal: hs.NilString{Null: true}}
		cost := new(big.Rat)
		elapsed := new(big.Int)
		costPresent, elapsedPresent := false, false
		effortCount := int64(0)
		effortTotal := float64(0)
		for _, ticket := range tickets {
			if ticket.Category != category || !slices.Contains(ticket.Labels, label.ID) {
				continue
			}
			row.Total++
			if ticket.Closed {
				row.Closed++
			} else {
				row.Open++
			}
			if !ticket.ExpectedCost.Null {
				value, ok := new(big.Rat).SetString(ticket.ExpectedCost.Value)
				if !ok {
					return fail("label usage retained decimal input")
				}
				cost.Add(cost, value)
				costPresent = true
			}
			if !ticket.Effort.Null {
				effortTotal += ticket.Effort.Value
				effortCount++
			}
			if !ticket.Elapsed.Null {
				value, err := summaryDurationMicros(ticket.Elapsed.Value)
				if err != nil {
					return err
				}
				elapsed.Add(elapsed, value)
				elapsedPresent = true
			}
		}
		if costPresent {
			value := strings.TrimRight(strings.TrimRight(cost.FloatString(2), "0"), ".")
			if value == "-0" {
				value = "0"
			}
			row.ExpectedCostTotal = hs.NewNilString(value)
		}
		if effortCount > 0 {
			row.EffortAverage = hs.NewNilFloat64(effortTotal / float64(effortCount))
		}
		if elapsedPresent {
			value, err := summaryDurationText(elapsed)
			if err != nil {
				return err
			}
			row.ElapsedTotal = hs.NewNilString(value)
		}
		expected = append(expected, row)
	}
	if expected[0].Total == 0 || expected[1].ID != unused.ID || expected[1].Total != 0 {
		return fail("label usage linked and unused fixture diversity")
	}
	for _, viewer := range []struct {
		client    *hs.Client
		transport *observedTransport
		state     *sessionState
	}{{client, transport, state}, {readOnly, readOnlyTransport, readOnlyState}} {
		for _, usage := range []hs.OptHelpdeskLabelUsageUsage{{}, hs.NewOptHelpdeskLabelUsageUsage(hs.HelpdeskLabelUsageUsageAll), hs.NewOptHelpdeskLabelUsageUsage(hs.HelpdeskLabelUsageUsageUsed), hs.NewOptHelpdeskLabelUsageUsage(hs.HelpdeskLabelUsageUsageUnused)} {
			name := "all"
			if value, set := usage.Get(); set {
				name = string(value)
			}
			filtered := make([]hs.LabelUsageResultsItem, 0)
			for _, row := range expected {
				if name == "all" || name == "used" && row.Total > 0 || name == "unused" && row.Total == 0 {
					filtered = append(filtered, row)
				}
			}
			for _, number := range []int64{1, 50001} {
				params := hs.HelpdeskLabelUsageParams{Usage: usage}
				if number != 1 {
					params.P = hs.NewOptInt64(number)
				}
				response, err := viewer.client.HelpdeskLabelUsage(ctx, params)
				page, ok := response.(*hs.LabelUsageHeaders)
				wanted := filtered
				if number != 1 {
					wanted = make([]hs.LabelUsageResultsItem, 0)
				}
				if err != nil || !ok || viewer.transport.lastStatus() != http.StatusOK || !page.XGodjCsrftoken.Set || !viewer.state.ready(page.XGodjCsrftoken.Value) {
					return fail("label usage generated read-only success and CSRF response")
				}
				if page.Response.Category.ID != category || page.Response.Category.Name != "Hardware & repairs" || page.Response.Page != number || page.Response.PageSize != 20 || page.Response.Usage != name || page.Response.TotalLabels != int64(len(filtered)) || !reflect.DeepEqual(page.Response.Results, wanted) {
					return fail("label usage generated exact metrics, NULLs, filters and page total")
				}
			}
		}
	}
	for _, params := range []hs.HelpdeskLabelUsageParams{{P: hs.NewOptInt64(0)}, {P: hs.NewOptInt64(50002)}, {Usage: hs.NewOptHelpdeskLabelUsageUsage("")}, {Usage: hs.NewOptHelpdeskLabelUsageUsage("invalid")}} {
		response, err := client.HelpdeskLabelUsage(ctx, params)
		value, ok := response.(*hs.HelpdeskLabelUsageBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || value.Response.Code != "validation_error" {
			return fail("label usage generated invalid query rejection")
		}
	}
	after, err := client.HelpdeskLabelList(ctx, hs.HelpdeskLabelListParams{Limit: hs.NewOptInt64(100)})
	labelPage, ok := after.(*hs.LabelListHeaders)
	if err != nil || !ok || !reflect.DeepEqual(labelPage.Response, before) || !labelPage.XGodjCsrftoken.Set || !state.ready(labelPage.XGodjCsrftoken.Value) {
		return fail("label usage preserved labels")
	}
	if err := requireHelpdeskTickets(ctx, client, transport, state, tickets...); err != nil {
		return err
	}
	removed, err := client.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: unused.ID})
	if _, ok := removed.(*hs.HelpdeskLabelDeleteNoContent); err != nil || !ok || transport.lastStatus() != http.StatusNoContent {
		return fail("label usage unused fixture cleanup")
	}
	return nil
}
