package main

import (
	"context"
	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskTicketLabels(ctx context.Context, client, readOnly *hs.Client, transport *observedTransport, state, readOnlyState *sessionState, seed, second, outsideTicket, outsideLabel, outsideLink int64) error {
	pageResult, err := client.HelpdeskLabelList(ctx, hs.HelpdeskLabelListParams{})
	labels, ok := pageResult.(*hs.LabelListHeaders)
	if err != nil || !ok || len(labels.Response.Items) != 1 || !labels.XGodjCsrftoken.Set || !state.ready(labels.XGodjCsrftoken.Value) {
		return fail("link fixture retained label")
	}
	retainedLabel := labels.Response.Items[0]
	tempResult, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: "Client cascade ticket"})
	tempTicket, ok := tempResult.(*hs.Ticket)
	if err != nil || !ok || tempTicket.ID <= 0 {
		return fail("link cascade ticket creation")
	}
	labelResult, err := client.HelpdeskLabelCreate(ctx, &hs.LabelCreate{Name: "Client cascade label"})
	tempLabel, ok := labelResult.(*hs.Label)
	if err != nil || !ok || tempLabel.ID <= 0 {
		return fail("link cascade label creation")
	}
	create := func(ticket, label int64) (*hs.TicketLabel, error) {
		result, err := client.HelpdeskTicketLabelCreate(ctx, &hs.TicketLabelCreate{Ticket: ticket, Label: label})
		value, ok := result.(*hs.TicketLabel)
		if err != nil || !ok || transport.lastStatus() != 201 || value.ID <= 0 || value.Ticket != ticket || value.Label != label {
			return nil, fail("generated ticket label create")
		}
		return value, nil
	}
	one, err := create(tempTicket.ID, retainedLabel.ID)
	if err != nil {
		return err
	}
	two, err := create(seed, tempLabel.ID)
	if err != nil {
		return err
	}
	three, err := create(tempTicket.ID, tempLabel.ID)
	if err != nil {
		return err
	}
	retained, err := create(seed, retainedLabel.ID)
	if err != nil {
		return err
	}
	duplicate, err := client.HelpdeskTicketLabelCreate(ctx, &hs.TicketLabelCreate{Ticket: seed, Label: retainedLabel.ID})
	if value, ok := duplicate.(*hs.HelpdeskTicketLabelCreateBadRequest); err != nil || !ok || len(value.Errors) != 1 || value.Errors[0].Field != "__all__" || value.Errors[0].Code != "unique_together" {
		return fail("generated duplicate link diagnostic")
	}
	for _, entry := range []struct {
		ticket, label int64
		field         string
	}{{outsideTicket, tempLabel.ID, "ticket"}, {seed, outsideLabel, "label"}} {
		result, err := client.HelpdeskTicketLabelCreate(ctx, &hs.TicketLabelCreate{Ticket: entry.ticket, Label: entry.label})
		if value, ok := result.(*hs.HelpdeskTicketLabelCreateBadRequest); err != nil || !ok || len(value.Errors) != 1 || value.Errors[0].Field != entry.field || value.Errors[0].Code != "invalid_choice" {
			return fail("generated link endpoint scope")
		}
	}
	foreign, err := client.HelpdeskTicketLabelDetail(ctx, hs.HelpdeskTicketLabelDetailParams{ID: outsideLink})
	if value, ok := foreign.(*hs.GoDjAPIErrorHeaders); err != nil || !ok || transport.lastStatus() != 404 || value.Response.Code != "not_found" || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated inaccessible link")
	}
	detail, err := client.HelpdeskTicketLabelDetail(ctx, hs.HelpdeskTicketLabelDetailParams{ID: retained.ID})
	if value, ok := detail.(*hs.TicketLabelHeaders); err != nil || !ok || value.Response != *retained || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated link detail and CSRF")
	}
	page, err := readOnly.HelpdeskTicketLabelList(ctx, hs.HelpdeskTicketLabelListParams{Limit: hs.NewOptInt64(1), Offset: hs.NewOptInt64(1)})
	if value, ok := page.(*hs.TicketLabelListHeaders); err != nil || !ok || value.Response.Count != 4 || value.Response.Limit != 1 || value.Response.Offset != 1 || len(value.Response.Items) != 1 || value.Response.Items[0] != *two || !value.XGodjCsrftoken.Set || !readOnlyState.ready(value.XGodjCsrftoken.Value) {
		return fail("generated link read-only page and scope")
	}
	invalidPage := hs.TicketLabelList{Items: []hs.TicketLabel{}, Limit: 0}
	if err := invalidPage.Validate(); err == nil {
		return fail("generated link page bounds")
	}
	deniedCreate, err := readOnly.HelpdeskTicketLabelCreate(ctx, &hs.TicketLabelCreate{})
	if value, ok := deniedCreate.(*hs.HelpdeskTicketLabelCreateForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated link create permission")
	}
	deniedPut, err := readOnly.HelpdeskTicketLabelUpdate(ctx, &hs.TicketLabelUpdate{}, hs.HelpdeskTicketLabelUpdateParams{ID: two.ID})
	if value, ok := deniedPut.(*hs.HelpdeskTicketLabelUpdateForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated link PUT permission")
	}
	deniedPatch, err := readOnly.HelpdeskTicketLabelPatch(ctx, &hs.TicketLabelPatch{}, hs.HelpdeskTicketLabelPatchParams{ID: two.ID})
	if value, ok := deniedPatch.(*hs.HelpdeskTicketLabelPatchForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated link PATCH permission")
	}
	deniedDelete, err := readOnly.HelpdeskTicketLabelDelete(ctx, hs.HelpdeskTicketLabelDeleteParams{ID: two.ID})
	if value, ok := deniedDelete.(*hs.HelpdeskTicketLabelDeleteForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated unlink permission")
	}
	for _, patch := range []*hs.TicketLabelPatch{{}, {Ticket: hs.NewOptInt64(two.Ticket), Label: hs.NewOptInt64(two.Label)}} {
		result, err := client.HelpdeskTicketLabelPatch(ctx, patch, hs.HelpdeskTicketLabelPatchParams{ID: two.ID})
		if value, ok := result.(*hs.TicketLabel); err != nil || !ok || *value != *two {
			return fail("generated empty/self link PATCH")
		}
	}
	collision, err := client.HelpdeskTicketLabelPatch(ctx, &hs.TicketLabelPatch{Label: hs.NewOptInt64(retainedLabel.ID)}, hs.HelpdeskTicketLabelPatchParams{ID: two.ID})
	if value, ok := collision.(*hs.HelpdeskTicketLabelPatchBadRequest); err != nil || !ok || len(value.Errors) != 1 || value.Errors[0].Code != "unique_together" {
		return fail("generated omitted ticket collision")
	}
	moved, err := client.HelpdeskTicketLabelUpdate(ctx, &hs.TicketLabelUpdate{Ticket: second, Label: tempLabel.ID}, hs.HelpdeskTicketLabelUpdateParams{ID: two.ID})
	if value, ok := moved.(*hs.TicketLabel); err != nil || !ok || value.ID != two.ID || value.Ticket != second || value.Label != tempLabel.ID {
		return fail("generated link PUT reassign")
	}
	restored, err := client.HelpdeskTicketLabelPatch(ctx, &hs.TicketLabelPatch{Ticket: hs.NewOptInt64(seed)}, hs.HelpdeskTicketLabelPatchParams{ID: two.ID})
	if value, ok := restored.(*hs.TicketLabel); err != nil || !ok || *value != *two {
		return fail("generated link PATCH omitted label")
	}
	state.setInvalid(true)
	deniedCSRF, err := client.HelpdeskTicketLabelDelete(ctx, hs.HelpdeskTicketLabelDeleteParams{ID: three.ID})
	state.setInvalid(false)
	if value, ok := deniedCSRF.(*hs.HelpdeskTicketLabelDeleteForbidden); err != nil || !ok || value.Code != "csrf_rejected" {
		return fail("generated unlink CSRF")
	}
	removed, err := client.HelpdeskTicketLabelDelete(ctx, hs.HelpdeskTicketLabelDeleteParams{ID: three.ID})
	if _, ok := removed.(*hs.HelpdeskTicketLabelDeleteNoContent); err != nil || !ok || transport.lastStatus() != 204 {
		return fail("generated unlink no-content")
	}
	three, err = create(tempTicket.ID, tempLabel.ID)
	if err != nil {
		return err
	}
	reportResult, err := client.HelpdeskServiceReportCreate(ctx, &hs.ServiceReportCreate{Ticket: tempTicket.ID, Summary: "Protect links"})
	report, ok := reportResult.(*hs.ServiceReport)
	if err != nil || !ok {
		return fail("generated protection fixture")
	}
	blocked, err := client.HelpdeskTicketDelete(ctx, hs.HelpdeskTicketDeleteParams{ID: tempTicket.ID})
	if value, ok := blocked.(*hs.HelpdeskTicketDeleteBadRequest); err != nil || !ok || len(value.Errors) != 1 || value.Errors[0].Field != "__all__" || value.Errors[0].Code != "protected" {
		return fail("generated protected ticket delete")
	}
	for _, link := range []*hs.TicketLabel{one, three} {
		result, err := client.HelpdeskTicketLabelDetail(ctx, hs.HelpdeskTicketLabelDetailParams{ID: link.ID})
		if value, ok := result.(*hs.TicketLabelHeaders); err != nil || !ok || value.Response != *link || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
			return fail("generated protection removed a link")
		}
	}
	removedReport, err := client.HelpdeskServiceReportDelete(ctx, hs.HelpdeskServiceReportDeleteParams{ID: report.ID})
	if _, ok := removedReport.(*hs.HelpdeskServiceReportDeleteNoContent); err != nil || !ok {
		return fail("generated protection removal")
	}
	removedTicket, err := client.HelpdeskTicketDelete(ctx, hs.HelpdeskTicketDeleteParams{ID: tempTicket.ID})
	if _, ok := removedTicket.(*hs.HelpdeskTicketDeleteNoContent); err != nil || !ok || transport.lastStatus() != 204 {
		return fail("generated ticket cascade deletion")
	}
	for _, link := range []*hs.TicketLabel{one, three} {
		result, err := client.HelpdeskTicketLabelDetail(ctx, hs.HelpdeskTicketLabelDetailParams{ID: link.ID})
		if value, ok := result.(*hs.GoDjAPIErrorHeaders); err != nil || !ok || value.Response.Code != "not_found" || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
			return fail("generated ticket cascade left link")
		}
	}
	labelDetail, err := client.HelpdeskLabelDetail(ctx, hs.HelpdeskLabelDetailParams{ID: tempLabel.ID})
	if value, ok := labelDetail.(*hs.LabelHeaders); err != nil || !ok || value.Response != *tempLabel || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated cascade deleted opposite label")
	}
	removedLabel, err := client.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: tempLabel.ID})
	if _, ok := removedLabel.(*hs.HelpdeskLabelDeleteNoContent); err != nil || !ok || transport.lastStatus() != 204 {
		return fail("generated label cascade deletion")
	}
	lost, err := client.HelpdeskTicketLabelDetail(ctx, hs.HelpdeskTicketLabelDetailParams{ID: two.ID})
	if value, ok := lost.(*hs.GoDjAPIErrorHeaders); err != nil || !ok || value.Response.Code != "not_found" || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated label cascade left link")
	}
	page, err = client.HelpdeskTicketLabelList(ctx, hs.HelpdeskTicketLabelListParams{})
	if value, ok := page.(*hs.TicketLabelListHeaders); err != nil || !ok || value.Response.Count != 1 || len(value.Response.Items) != 1 || value.Response.Items[0] != *retained || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated cascade lost unrelated retained link")
	}
	return nil
}
