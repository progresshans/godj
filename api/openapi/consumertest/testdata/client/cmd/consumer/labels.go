package main

import (
	"context"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskLabels(ctx context.Context, client, readOnly *hs.Client, transport, readOnlyTransport *observedTransport, state, readOnlyState *sessionState, category, outside int64) error {
	created, err := client.HelpdeskLabelCreate(ctx, &hs.LabelCreate{Name: "  Client shared label  "})
	first, ok := created.(*hs.Label)
	if err != nil || !ok || transport.lastStatus() != 201 || first.ID <= 0 || first.Category != category || first.Name != "Client shared label" {
		return fail("generated Label create/trim and same name in another category")
	}
	duplicate, err := client.HelpdeskLabelCreate(ctx, &hs.LabelCreate{Name: first.Name})
	if value, ok := duplicate.(*hs.HelpdeskLabelCreateBadRequest); err != nil || !ok || value.Code != "validation_error" || len(value.Errors) != 1 || value.Errors[0].Field != "__all__" || value.Errors[0].Code != "unique_together" {
		return fail("generated Label tuple diagnostic")
	}
	created, err = client.HelpdeskLabelCreate(ctx, &hs.LabelCreate{Name: "Client second label"})
	second, ok := created.(*hs.Label)
	if err != nil || !ok || second.ID <= first.ID || second.Category != category {
		return fail("generated second Label create")
	}
	detail, err := client.HelpdeskLabelDetail(ctx, hs.HelpdeskLabelDetailParams{ID: first.ID})
	if value, ok := detail.(*hs.LabelHeaders); err != nil || !ok || value.Response != *first || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated Label detail and CSRF rotation")
	}
	foreign, err := client.HelpdeskLabelDetail(ctx, hs.HelpdeskLabelDetailParams{ID: outside})
	if value, ok := foreign.(*hs.GoDjAPIErrorHeaders); err != nil || !ok || transport.lastStatus() != 404 || value.Response.Code != "not_found" || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated Label detail scope")
	}
	page, err := readOnly.HelpdeskLabelList(ctx, hs.HelpdeskLabelListParams{Limit: hs.NewOptInt64(1), Offset: hs.NewOptInt64(1), Search: hs.NewOptString("Client")})
	if value, ok := page.(*hs.LabelListHeaders); err != nil || !ok || readOnlyTransport.lastStatus() != 200 || value.Response.Count != 2 || value.Response.Limit != 1 || value.Response.Offset != 1 || len(value.Response.Items) != 1 || value.Response.Items[0] != *second || !value.XGodjCsrftoken.Set || !readOnlyState.ready(value.XGodjCsrftoken.Value) {
		return fail("generated Label pagination/search scope")
	}
	badPage := hs.LabelList{Count: 0, Limit: 0, Offset: 0, Items: []hs.Label{}}
	if err := badPage.Validate(); err == nil {
		return fail("generated Label page lost numeric bounds")
	}
	deniedCreate, err := readOnly.HelpdeskLabelCreate(ctx, &hs.LabelCreate{})
	if value, ok := deniedCreate.(*hs.HelpdeskLabelCreateForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated Label create permission before validation")
	}
	deniedUpdate, err := readOnly.HelpdeskLabelUpdate(ctx, &hs.LabelUpdate{}, hs.HelpdeskLabelUpdateParams{ID: first.ID})
	if value, ok := deniedUpdate.(*hs.HelpdeskLabelUpdateForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated Label update permission")
	}
	deniedPatch, err := readOnly.HelpdeskLabelPatch(ctx, &hs.LabelPatch{}, hs.HelpdeskLabelPatchParams{ID: first.ID})
	if value, ok := deniedPatch.(*hs.HelpdeskLabelPatchForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated Label patch permission")
	}
	deniedDelete, err := readOnly.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: first.ID})
	if value, ok := deniedDelete.(*hs.HelpdeskLabelDeleteForbidden); err != nil || !ok || value.Code != "permission_denied" {
		return fail("generated Label delete permission")
	}
	for _, id := range []int64{first.ID, second.ID} {
		patch, err := client.HelpdeskLabelPatch(ctx, &hs.LabelPatch{Name: hs.NewOptString(first.Name)}, hs.HelpdeskLabelPatchParams{ID: id})
		if id == first.ID {
			if value, ok := patch.(*hs.Label); err != nil || !ok || *value != *first {
				return fail("generated Label self update")
			}
		} else if value, ok := patch.(*hs.HelpdeskLabelPatchBadRequest); err != nil || !ok || len(value.Errors) != 1 || value.Errors[0].Code != "unique_together" || value.Errors[0].Field != "__all__" {
			return fail("generated Label partial update lost omitted category")
		}
	}
	unchanged, err := client.HelpdeskLabelPatch(ctx, &hs.LabelPatch{}, hs.HelpdeskLabelPatchParams{ID: second.ID})
	if value, ok := unchanged.(*hs.Label); err != nil || !ok || *value != *second {
		return fail("generated Label PATCH omission")
	}
	foreignPatch, err := client.HelpdeskLabelPatch(ctx, &hs.LabelPatch{Name: hs.NewOptString("foreign mutation")}, hs.HelpdeskLabelPatchParams{ID: outside})
	if value, ok := foreignPatch.(*hs.HelpdeskLabelPatchNotFound); err != nil || !ok || value.Code != "not_found" {
		return fail("generated Label update scope")
	}
	state.setInvalid(true)
	csrf, err := client.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: second.ID})
	state.setInvalid(false)
	if value, ok := csrf.(*hs.HelpdeskLabelDeleteForbidden); err != nil || !ok || value.Code != "csrf_rejected" {
		return fail("generated Label deletion requires CSRF")
	}
	updated, err := client.HelpdeskLabelUpdate(ctx, &hs.LabelUpdate{Name: "Client retained label"}, hs.HelpdeskLabelUpdateParams{ID: first.ID})
	if value, ok := updated.(*hs.Label); err != nil || !ok || value.ID != first.ID || value.Category != category || value.Name != "Client retained label" {
		return fail("generated Label PUT")
	}
	removed, err := client.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: second.ID})
	if _, ok := removed.(*hs.HelpdeskLabelDeleteNoContent); err != nil || !ok || transport.lastStatus() != 204 {
		return fail("generated Label DELETE no content")
	}
	removed, err = client.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: outside})
	if value, ok := removed.(*hs.HelpdeskLabelDeleteNotFound); err != nil || !ok || value.Code != "not_found" {
		return fail("generated Label delete scope")
	}
	page, err = client.HelpdeskLabelList(ctx, hs.HelpdeskLabelListParams{})
	if value, ok := page.(*hs.LabelListHeaders); err != nil || !ok || value.Response.Count != 1 || value.Response.Limit != 20 || value.Response.Offset != 0 || len(value.Response.Items) != 1 || value.Response.Items[0].ID != first.ID || value.Response.Items[0].Name != "Client retained label" || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return fail("generated Label final page")
	}
	return nil
}
