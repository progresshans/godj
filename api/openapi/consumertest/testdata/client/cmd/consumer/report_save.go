package main

import (
	"context"
	"net/http"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskReportSave(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state *sessionState, ticket, outside int64) error {
	params := hs.HelpdeskTicketServiceReportSaveParams{ID: ticket}
	response, err := client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "  Client report\nSecond line  ", Completed: hs.NewOptBool(true)}, params)
	created, ok := response.(*hs.HelpdeskTicketServiceReportSaveCreated)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || !created.Created || created.Report.ID <= 0 || created.Report.Ticket != ticket || created.Report.Summary != "Client report\nSecond line" || !created.Report.Completed {
		return fail("generated report save creation")
	}
	id := created.Report.ID
	response, err = client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "Client retained report"}, params)
	updated, ok := response.(*hs.HelpdeskTicketServiceReportSaveOK)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || updated.Created || updated.Report.ID != id || updated.Report.Ticket != ticket || updated.Report.Summary != "Client retained report" || updated.Report.Completed {
		return fail("generated report save update/defaults/identity")
	}
	response, err = client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "Client retained report"}, params)
	if unchanged, ok := response.(*hs.HelpdeskTicketServiceReportSaveOK); err != nil || !ok || unchanged.Created || unchanged.Report != updated.Report {
		return fail("generated report save unchanged result")
	}
	response, err = client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "outside"}, hs.HelpdeskTicketServiceReportSaveParams{ID: outside})
	if failure, ok := response.(*hs.HelpdeskTicketServiceReportSaveNotFound); err != nil || !ok || failure.Code != "not_found" {
		return fail("generated report save category scope")
	}
	response, err = client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{}, params)
	if failure, ok := response.(*hs.HelpdeskTicketServiceReportSaveBadRequest); err != nil || !ok || failure.Code != "validation_error" || len(failure.Errors) != 1 || failure.Errors[0].Field != "summary" || failure.Errors[0].Code != "blank" {
		return fail("generated report save blank input")
	}
	response, err = readOnly.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{}, params)
	if failure, ok := response.(*hs.HelpdeskTicketServiceReportSaveForbidden); err != nil || !ok || failure.Code != "permission_denied" {
		return fail("generated report save permission before parsing")
	}
	state.setInvalid(true)
	response, err = client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "forbidden"}, params)
	state.setInvalid(false)
	if failure, ok := response.(*hs.HelpdeskTicketServiceReportSaveForbidden); err != nil || !ok || failure.Code != "csrf_rejected" {
		return fail("generated report save CSRF")
	}
	duplicate, err := client.HelpdeskServiceReportCreate(ctx, &hs.ServiceReportCreate{Ticket: ticket, Summary: "duplicate"})
	if failure, ok := duplicate.(*hs.HelpdeskServiceReportCreateBadRequest); err != nil || !ok || failure.Code != "validation_error" {
		return fail("generated report save changed ordinary creation")
	}
	detail, err := client.HelpdeskServiceReportDetail(ctx, hs.HelpdeskServiceReportDetailParams{ID: id})
	if stored, ok := detail.(*hs.ServiceReportHeaders); err != nil || !ok || stored.Response != updated.Report || !stored.XGodjCsrftoken.Set || !state.ready(stored.XGodjCsrftoken.Value) {
		return fail("generated report save final durable response")
	}
	return nil
}
