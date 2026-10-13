package main

import (
	"context"
	"net/http"
	"reflect"

	hs "example.com/godj-openapi-client/helpdesksession"
)

// Expectations come from the retained ticket and mutation response. This
// independent module cannot use GoDj's relation, comparison or CASE helpers.
func checkHelpdeskReportStatus(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category int64, ticket hs.Ticket, report hs.ServiceReport) error {
	row := hs.ServiceReportStatusResultsItem{ID: report.ID, Ticket: ticket.ID, Summary: report.Summary,
		Completed: report.Completed, TicketClosed: ticket.Closed, Matched: report.Completed == ticket.Closed}
	for _, viewer := range []struct {
		client    *hs.Client
		transport *observedTransport
		state     *sessionState
	}{{client, transport, state}, {readOnly, readOnlyTransport, readOnlyState}} {
		for _, comparison := range []hs.OptHelpdeskServiceReportStatusComparison{
			{}, hs.NewOptHelpdeskServiceReportStatusComparison(hs.HelpdeskServiceReportStatusComparisonAll),
			hs.NewOptHelpdeskServiceReportStatusComparison(hs.HelpdeskServiceReportStatusComparisonSame),
			hs.NewOptHelpdeskServiceReportStatusComparison(hs.HelpdeskServiceReportStatusComparisonDifferent),
		} {
			name := "all"
			if value, set := comparison.Get(); set {
				name = string(value)
			}
			for _, number := range []int64{1, 50001} {
				filtered := name == "all" || name == "same" && row.Matched || name == "different" && !row.Matched
				total := int64(0)
				expected := make([]hs.ServiceReportStatusResultsItem, 0)
				if filtered {
					total = 1
					if number == 1 {
						expected = append(expected, row)
					}
				}
				params := hs.HelpdeskServiceReportStatusParams{Comparison: comparison}
				if number != 1 {
					params.P = hs.NewOptInt64(number)
				}
				response, err := viewer.client.HelpdeskServiceReportStatus(ctx, params)
				value, ok := response.(*hs.ServiceReportStatusHeaders)
				if err != nil || !ok || viewer.transport.lastStatus() != http.StatusOK ||
					!value.XGodjCsrftoken.Set || !viewer.state.ready(value.XGodjCsrftoken.Value) ||
					value.Response.Category.ID != category || value.Response.Category.Name != "Hardware & repairs" ||
					value.Response.Page != number || value.Response.PageSize != 20 || value.Response.Comparison != name ||
					value.Response.TotalReports != total || !reflect.DeepEqual(value.Response.Results, expected) {
					return fail("generated report status native flags, filters, empty page and view permissions")
				}
			}
		}
	}
	for _, params := range []hs.HelpdeskServiceReportStatusParams{
		{P: hs.NewOptInt64(0)}, {P: hs.NewOptInt64(50002)},
		{Comparison: hs.NewOptHelpdeskServiceReportStatusComparison("")},
		{Comparison: hs.NewOptHelpdeskServiceReportStatusComparison("invalid")},
	} {
		response, err := client.HelpdeskServiceReportStatus(ctx, params)
		value, ok := response.(*hs.HelpdeskServiceReportStatusBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || value.Response.Code != "validation_error" {
			return fail("generated report status rejects invalid page and enum query")
		}
	}
	detail, err := client.HelpdeskTicketDetail(ctx, hs.HelpdeskTicketDetailParams{ID: ticket.ID})
	if value, ok := detail.(*hs.TicketDetailHeaders); err != nil || !ok || !sameHelpdeskTicket(value.Response.Ticket, ticket) {
		return fail("generated report status preserves stored ticket")
	}
	response, err := client.HelpdeskServiceReportDetail(ctx, hs.HelpdeskServiceReportDetailParams{ID: report.ID})
	value, ok := response.(*hs.ServiceReportHeaders)
	if err != nil || !ok || value.Response != report {
		return fail("generated report status preserves stored report")
	}
	return nil
}
