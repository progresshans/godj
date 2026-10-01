package main

import (
	"context"
	"net/http"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskServiceReports(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, first, second, outside int64) error {
	reverse, err := client.HelpdeskTicketServiceReport(ctx, hs.HelpdeskTicketServiceReportParams{ID: first})
	absent, ok := reverse.(*hs.NilServiceReportHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || !absent.Response.Null || !absent.XGodjCsrftoken.Set || !state.ready(absent.XGodjCsrftoken.Value) {
		return fail("generated reverse report absence")
	}
	foreign, err := client.HelpdeskTicketServiceReport(ctx, hs.HelpdeskTicketServiceReportParams{ID: outside})
	if value, ok := foreign.(*hs.GoDjAPIErrorHeaders); err != nil || !ok || transport.lastStatus() != 404 || value.Response.Code != "not_found" || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated reverse report category boundary")
	}
	created, err := client.HelpdeskServiceReportCreate(ctx, &hs.ServiceReportCreate{Ticket: first, Summary: "  First report\nSecond line  "})
	report, ok := created.(*hs.ServiceReport)
	if err != nil {
		return fail("generated report create decoding")
	}
	if !ok || transport.lastStatus() != 201 {
		return fail("generated report create success status")
	}
	if report.ID <= 0 || report.Ticket != first || report.Summary != "First report\nSecond line" || report.Completed {
		return fail("generated report create defaults")
	}
	id := report.ID
	duplicate, err := client.HelpdeskServiceReportCreate(ctx, &hs.ServiceReportCreate{Ticket: first, Summary: "duplicate"})
	if value, ok := duplicate.(*hs.HelpdeskServiceReportCreateBadRequest); err != nil || !ok || transport.lastStatus() != 400 || value.Code != "validation_error" || len(value.Errors) != 1 || value.Errors[0].Field != "ticket" || value.Errors[0].Code != "unique" {
		return fail("generated report duplicate diagnostic")
	}
	invalid, err := client.HelpdeskServiceReportCreate(ctx, &hs.ServiceReportCreate{Ticket: outside, Summary: "foreign"})
	if value, ok := invalid.(*hs.HelpdeskServiceReportCreateBadRequest); err != nil || !ok || len(value.Errors) != 1 || value.Errors[0].Field != "ticket" || value.Errors[0].Code != "invalid_choice" {
		return fail("generated report choice scope diagnostic")
	}
	detail, err := client.HelpdeskServiceReportDetail(ctx, hs.HelpdeskServiceReportDetailParams{ID: id})
	if value, ok := detail.(*hs.ServiceReportHeaders); err != nil || !ok || value.Response != *report || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated report detail")
	}
	listed, err := readOnly.HelpdeskServiceReportList(ctx)
	if value, ok := listed.(*hs.HelpdeskServiceReportListOKHeaders); err != nil || !ok || readOnlyTransport.lastStatus() != 200 || len(value.Response) != 1 || value.Response[0] != *report || !value.XGodjCsrftoken.Set || !readOnlyState.ready(value.XGodjCsrftoken.Value) {
		return fail("generated report authorized list")
	}
	deniedCreate, err := readOnly.HelpdeskServiceReportCreate(ctx, &hs.ServiceReportCreate{})
	if value, ok := deniedCreate.(*hs.HelpdeskServiceReportCreateForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated report create permission")
	}
	deniedPut, err := readOnly.HelpdeskServiceReportUpdate(ctx, &hs.ServiceReportUpdate{}, hs.HelpdeskServiceReportUpdateParams{ID: id})
	if value, ok := deniedPut.(*hs.HelpdeskServiceReportUpdateForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated report update permission")
	}
	deniedPatch, err := readOnly.HelpdeskServiceReportPatch(ctx, &hs.ServiceReportPatch{}, hs.HelpdeskServiceReportPatchParams{ID: id})
	if value, ok := deniedPatch.(*hs.HelpdeskServiceReportPatchForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated report patch permission")
	}
	deniedDelete, err := readOnly.HelpdeskServiceReportDelete(ctx, hs.HelpdeskServiceReportDeleteParams{ID: id})
	if value, ok := deniedDelete.(*hs.HelpdeskServiceReportDeleteForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated report delete permission")
	}
	state.setInvalid(true)
	csrf, err := client.HelpdeskServiceReportDelete(ctx, hs.HelpdeskServiceReportDeleteParams{ID: id})
	state.setInvalid(false)
	if value, ok := csrf.(*hs.HelpdeskServiceReportDeleteForbidden); err != nil || !ok || value.Code != "csrf_rejected" {
		return fail("generated DELETE requires CSRF")
	}
	changed, err := client.HelpdeskServiceReportPatch(ctx, &hs.ServiceReportPatch{Ticket: hs.NewOptInt64(first), Completed: hs.NewOptBool(true)}, hs.HelpdeskServiceReportPatchParams{ID: id})
	if value, ok := changed.(*hs.ServiceReport); err != nil || !ok || value.ID != id || value.Ticket != first || value.Summary != report.Summary || !value.Completed {
		return fail("generated report PATCH self uniqueness and omissions")
	}
	updated, err := client.HelpdeskServiceReportUpdate(ctx, &hs.ServiceReportUpdate{Ticket: second, Summary: "Reassigned"}, hs.HelpdeskServiceReportUpdateParams{ID: id})
	if value, ok := updated.(*hs.ServiceReport); err != nil || !ok || value.ID != id || value.Ticket != second || value.Summary != "Reassigned" || value.Completed {
		return fail("generated report PUT reassignment and default")
	}
	for _, key := range []int64{first, second} {
		result, err := client.HelpdeskTicketServiceReport(ctx, hs.HelpdeskTicketServiceReportParams{ID: key})
		value, ok := result.(*hs.NilServiceReportHeaders)
		if err != nil || !ok || value.Response.Null != (key == first) || (!value.Response.Null && (value.Response.Value.ID != id || value.Response.Value.Ticket != second)) || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
			return fail("generated reverse report reassignment")
		}
	}
	removed, err := client.HelpdeskServiceReportDelete(ctx, hs.HelpdeskServiceReportDeleteParams{ID: id})
	if _, ok := removed.(*hs.HelpdeskServiceReportDeleteNoContent); err != nil || !ok || transport.lastStatus() != 204 {
		return fail("generated report DELETE no-content response")
	}
	removed, err = client.HelpdeskServiceReportDelete(ctx, hs.HelpdeskServiceReportDeleteParams{ID: id})
	if value, ok := removed.(*hs.HelpdeskServiceReportDeleteNotFound); err != nil || !ok || value.Code != "not_found" {
		return fail("generated repeated report DELETE")
	}
	listed, err = client.HelpdeskServiceReportList(ctx)
	if value, ok := listed.(*hs.HelpdeskServiceReportListOKHeaders); err != nil || !ok || len(value.Response) != 0 || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated report final empty list")
	}
	return nil
}
