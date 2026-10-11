package main

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
)

// The expected groups come from the independently retained mutation results,
// not from another summary request or a GoDj helper imported by this module.
func checkHelpdeskSummary(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state *sessionState, category int64, tickets []hs.Ticket) error {
	groups := make(map[hs.NilInt64]hs.TicketSummaryResultsItem)
	type metrics struct {
		cost        *big.Rat
		elapsed     *big.Int
		effort      float64
		effortCount int
	}
	values := make(map[hs.NilInt64]metrics)
	for _, ticket := range tickets {
		row := groups[ticket.Priority]
		if row.Total == 0 {
			row.ExpectedCostTotal.SetToNull()
			row.EffortAverage.SetToNull()
			row.ElapsedTotal.SetToNull()
		}
		metric := values[ticket.Priority]
		if !ticket.ExpectedCost.Null {
			cost, ok := new(big.Rat).SetString(ticket.ExpectedCost.Value)
			if !ok {
				return fail("retained ticket has invalid decimal cost")
			}
			if metric.cost == nil {
				metric.cost = new(big.Rat)
			}
			metric.cost.Add(metric.cost, cost)
		}
		if !ticket.Effort.Null {
			metric.effort += ticket.Effort.Value
			metric.effortCount++
		}
		if !ticket.Elapsed.Null {
			elapsed, err := summaryDurationMicros(ticket.Elapsed.Value)
			if err != nil {
				return err
			}
			if metric.elapsed == nil {
				metric.elapsed = new(big.Int)
			}
			metric.elapsed.Add(metric.elapsed, elapsed)
		}
		values[ticket.Priority] = metric
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
		row.RaiseToPriority = ticket.Priority.Value
		if ticket.Priority.Null {
			row.RaiseToPriority = 0
		} else if row.RaiseToPriority == -1 || row.RaiseToPriority == 0 {
			row.RaiseToPriority++
		}
		row.WouldChange = ticket.Priority.Null || ticket.Priority.Value != row.RaiseToPriority
		switch row.RaiseToPriority {
		case -1:
			row.RaiseToLabel = "Low"
		case 0:
			row.RaiseToLabel = "Normal"
		case 1:
			row.RaiseToLabel = "Urgent"
		default:
			row.RaiseToLabel = "Other (" + strconv.FormatInt(row.RaiseToPriority, 10) + ")"
		}
		groups[ticket.Priority] = row
	}
	var expected []hs.TicketSummaryResultsItem
	for key, row := range groups {
		metric := values[key]
		if metric.cost != nil {
			// Stored ticket costs have two decimal places. Exact rational
			// addition avoids binary64 and preserves sums wider than a field.
			cost := strings.TrimRight(strings.TrimRight(metric.cost.FloatString(2), "0"), ".")
			if cost == "-0" {
				cost = "0"
			}
			row.ExpectedCostTotal.SetTo(cost)
		}
		if metric.effortCount > 0 {
			row.EffortAverage.SetTo(metric.effort / float64(metric.effortCount))
		}
		if metric.elapsed != nil {
			elapsed, err := summaryDurationText(metric.elapsed)
			if err != nil {
				return err
			}
			row.ElapsedTotal.SetTo(elapsed)
		}
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

// These helpers use only the independent client's standard library and
// retained response strings. They do not import GoDj or ask the summary API
// to supply its own expected totals.
func summaryDurationMicros(text string) (*big.Int, error) {
	day, clock := int64(0), text
	if prefix, tail, found := strings.Cut(text, " "); found {
		parsed, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return nil, err
		}
		day, clock = parsed, tail
	}
	parts := strings.Split(clock, ":")
	if len(parts) != 3 {
		return nil, fail("retained duration clock")
	}
	hour, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}
	minute, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, err
	}
	seconds, fraction, _ := strings.Cut(parts[2], ".")
	second, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return nil, err
	}
	microseconds := int64(0)
	if fraction != "" {
		if len(fraction) != 6 {
			return nil, fail("retained duration precision")
		}
		microseconds, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	if day < -999999999 || day > 999999999 || hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return nil, fail("retained duration range")
	}
	result := big.NewInt(day)
	result.Mul(result, big.NewInt(86400000000))
	result.Add(result, big.NewInt(((hour*60+minute)*60+second)*1000000+microseconds))
	return result, nil
}
func summaryDurationText(microseconds *big.Int) (string, error) {
	var days, remainder big.Int
	days.DivMod(microseconds, big.NewInt(86400000000), &remainder)
	if !days.IsInt64() || days.Int64() < -999999999 || days.Int64() > 999999999 {
		return "", fail("expected duration sum exceeds model domain")
	}
	rest := remainder.Int64()
	text := fmt.Sprintf("%02d:%02d:%02d", rest/3600000000, rest/60000000%60, rest/1000000%60)
	if rest%1000000 != 0 {
		text += fmt.Sprintf(".%06d", rest%1000000)
	}
	if days.Sign() != 0 {
		text = days.String() + " " + text
	}
	return text, nil
}
