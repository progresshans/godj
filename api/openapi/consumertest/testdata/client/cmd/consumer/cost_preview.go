package main

import (
	"context"
	"math/big"
	"net/http"
	"reflect"
	"sort"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskCostPreview(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, tickets []hs.Ticket) error {
	for _, params := range []hs.HelpdeskCostPreviewParams{
		{},
		{Factor: hs.NewOptString("1.25"), Delta: hs.NewOptString("0.1"), MinCost: hs.NewOptString("0")},
		{Factor: hs.NewOptString("-0.5"), Delta: hs.NewOptString("-0.01")},
		{Factor: hs.NewOptString("0"), MinCost: hs.NewOptString("0")},
	} {
		factor, delta := "1", "0"
		if params.Factor.Set {
			factor = params.Factor.Value
		}
		if params.Delta.Set {
			delta = params.Delta.Value
		}
		multiplier, _ := new(big.Rat).SetString(factor)
		adjustment, _ := new(big.Rat).SetString(delta)
		var threshold *big.Rat
		minimum := hs.NilString{Null: true}
		if params.MinCost.Set {
			threshold, _ = new(big.Rat).SetString(params.MinCost.Value)
			minimum = hs.NewNilString(params.MinCost.Value)
		}
		expected := make([]hs.CostPreviewResultsItem, 0, len(tickets))
		values := map[int64]*big.Rat{}
		for _, ticket := range tickets {
			row := hs.CostPreviewResultsItem{ID: ticket.ID, Subject: ticket.Subject, ExpectedCost: hs.NilString{Null: true}, AdjustedCost: hs.NilString{Null: true}}
			if !ticket.ExpectedCost.Null {
				value, ok := new(big.Rat).SetString(ticket.ExpectedCost.Value)
				if !ok {
					return fail("cost preview retained exact source value")
				}
				original := strings.TrimRight(strings.TrimRight(value.FloatString(2), "0"), ".")
				value.Mul(value, multiplier)
				value.Add(value, adjustment)
				if threshold != nil && value.Cmp(threshold) < 0 {
					continue
				}
				text := strings.TrimRight(strings.TrimRight(value.FloatString(6), "0"), ".")
				if text == "-0" {
					text = "0"
				}
				row.ExpectedCost, row.AdjustedCost = hs.NewNilString(original), hs.NewNilString(text)
				values[row.ID] = value
			} else if threshold != nil {
				continue
			}
			expected = append(expected, row)
		}
		sort.Slice(expected, func(i, j int) bool {
			left, right := values[expected[i].ID], values[expected[j].ID]
			if left == nil || right == nil {
				if left != right {
					return left != nil
				}
			} else if order := left.Cmp(right); order != 0 {
				return order > 0
			}
			return expected[i].ID < expected[j].ID
		})
		for _, viewer := range []struct {
			client    *hs.Client
			transport *observedTransport
			state     *sessionState
		}{{client, transport, state}, {readOnly, readOnlyTransport, readOnlyState}} {
			for _, number := range []int64{1, 2, 50001} {
				request := params
				if number != 1 {
					request.P = hs.NewOptInt64(number)
				}
				response, err := viewer.client.HelpdeskCostPreview(ctx, request)
				page, ok := response.(*hs.CostPreviewHeaders)
				if err != nil || !ok || viewer.transport.lastStatus() != http.StatusOK || !page.XGodjCsrftoken.Set || !viewer.state.ready(page.XGodjCsrftoken.Value) {
					return fail("cost preview generated read-only admission and CSRF response")
				}
				start := min(len(expected), int(number-1)*20)
				end := min(len(expected), start+20)
				if page.Response.Category.ID != category || page.Response.Category.Name != "Hardware & repairs" || page.Response.Page != number || page.Response.PageSize != 20 || page.Response.Factor != factor || page.Response.Delta != delta || page.Response.MinCost != minimum || page.Response.TotalTickets != int64(len(expected)) || !reflect.DeepEqual(page.Response.Results, expected[start:end]) {
					return fail("cost preview generated exact values, NULLs, ordering and filtered total")
				}
			}
		}
	}
	for _, params := range []hs.HelpdeskCostPreviewParams{{P: hs.NewOptInt64(0)}, {Factor: hs.NewOptString("1.0")}, {Factor: hs.NewOptString("1e0")}, {Factor: hs.NewOptString("100")}, {Delta: hs.NewOptString("0.001")}, {MinCost: hs.NewOptString("")}} {
		response, err := client.HelpdeskCostPreview(ctx, params)
		value, ok := response.(*hs.HelpdeskCostPreviewBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || value.Response.Code != "validation_error" {
			return fail("cost preview generated invalid exact query rejection")
		}
	}
	return requireHelpdeskTickets(ctx, client, transport, state, tickets...)
}
