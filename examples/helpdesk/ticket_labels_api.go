package helpdesk

import (
	"math"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) ticketLabelOperations(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error)) ([]openapi.Operation, []openapi.NamedSchema, error) {
	output, err := openapi.ModelResponseSchema(a.linkOutput)
	if err != nil {
		return nil, nil, err
	}
	input, err := openapi.RequestSchema(a.linkInput, serializers.ModeFull)
	if err != nil {
		return nil, nil, err
	}
	partial, err := openapi.RequestSchema(a.linkInput, serializers.ModePartial)
	if err != nil {
		return nil, nil, err
	}
	linkRef, err := openapi.Ref("TicketLabel")
	if err != nil {
		return nil, nil, err
	}
	createRef, err := openapi.Ref("TicketLabelCreate")
	if err != nil {
		return nil, nil, err
	}
	updateRef, err := openapi.Ref("TicketLabelUpdate")
	if err != nil {
		return nil, nil, err
	}
	patchRef, err := openapi.Ref("TicketLabelPatch")
	if err != nil {
		return nil, nil, err
	}
	listRef, err := openapi.Ref("TicketLabelList")
	if err != nil {
		return nil, nil, err
	}
	items, err := openapi.Array(linkRef)
	if err != nil {
		return nil, nil, err
	}
	count, err := openapi.IntegerRange(0, math.MaxInt64)
	if err != nil {
		return nil, nil, err
	}
	limit, err := openapi.IntegerRange(1, 100)
	if err != nil {
		return nil, nil, err
	}
	offset, err := openapi.IntegerRange(0, math.MaxInt32)
	if err != nil {
		return nil, nil, err
	}
	list, err := openapi.Object(openapi.Property{Name: "items", Schema: items, Required: true}, openapi.Property{Name: "count", Schema: count, Required: true}, openapi.Property{Name: "limit", Schema: limit, Required: true}, openapi.Property{Name: "offset", Schema: offset, Required: true})
	if err != nil {
		return nil, nil, err
	}
	errorRef, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return nil, nil, err
	}
	notFound := helpdeskJSONResponse(http.StatusNotFound, "The link does not exist with both endpoints in the selected category.", errorRef)
	badInput := helpdeskJSONResponse(http.StatusBadRequest, "Invalid input or inaccessible/missing endpoint. Endpoint failures use ticket/invalid_choice or label/invalid_choice without revealing existence. Duplicate pairs use __all__/unique_together; a concurrent native conflict uses __all__/unique.", errorRef)
	writes := func(status int) []openapi.Response {
		return []openapi.Response{helpdeskJSONResponse(status, "The saved ticket-label link.", linkRef), badInput, notFound, helpdeskJSONResponse(http.StatusRequestEntityTooLarge, "The JSON body exceeds 4096 bytes.", errorRef), helpdeskJSONResponse(http.StatusUnsupportedMediaType, "The body is not application/json.", errorRef)}
	}
	const writePolicy = "Authentication, CSRF, the link mutation permission and both ticket/label view permissions precede parsing or data lookup. Ticket and label must be positive IDs whose server-assigned categories match the application's category. Both complete candidate endpoints and pair uniqueness are checked in the write transaction, including an omitted PATCH key. PUT requires both keys. PATCH preserves omitted keys; an empty or self patch checks scope without an UPDATE. Generated id and category cannot be supplied. Confirmed rejections roll back the entire write; storage errors, cancellation and uncertain outcomes remain errors."
	definitions := []struct {
		operation openapi.Operation
		handler   api.AuthenticatedHandler
	}{
		{openapi.Operation{Route: web.Route{Name: "helpdesk:ticket-label-list", Method: http.MethodGet, Path: "/api/ticket-labels/"}, Summary: "List ticket-label links", Permission: ViewTicketLabel, Description: "Links whose ticket and label both belong to the selected category, ordered by ID. Only endpoint IDs are exposed. Limit defaults to 20 (1..100); offset defaults to 0 (0..2147483647). Count precedes pagination. Unknown/duplicate parameters, invalid encoding, NUL and queries over 2048 bytes return 400. Count and items are separate reads, so concurrent changes can shift pages.", Parameters: []openapi.Parameter{{Name: "limit", In: "query", Schema: limit, Description: "Maximum page size; default 20."}, {Name: "offset", In: "query", Schema: offset, Description: "Rows to skip; default 0."}}, Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The scoped page.", listRef), badInput}}, a.apiTicketLabelList},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:ticket-label-create", Method: http.MethodPost, Path: "/api/ticket-labels/"}, Summary: "Link a label to a ticket", Permission: AddTicketLabel, AdditionalPermissions: []auth.Permission{ViewTicket, ViewLabel}, Description: writePolicy, RequestBody: &openapi.RequestBody{Schema: createRef, Required: true, Description: "One JSON object. Unknown/duplicate members, null keys and trailing data are rejected."}, Responses: writes(http.StatusCreated)}, a.apiTicketLabelCreate},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:ticket-label-detail", Method: http.MethodGet, Path: "/api/ticket-labels/<int64:id>/"}, Summary: "Read a ticket-label link", Permission: ViewTicketLabel, Description: "Link view permission covers its endpoint IDs; endpoint names are not exposed.", Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The scoped link.", linkRef), notFound}}, a.apiTicketLabelDetail},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:ticket-label-update", Method: http.MethodPut, Path: "/api/ticket-labels/<int64:id>/"}, Summary: "Replace a ticket-label link's endpoints", Permission: ChangeTicketLabel, AdditionalPermissions: []auth.Permission{ViewTicket, ViewLabel}, Description: writePolicy, RequestBody: &openapi.RequestBody{Schema: updateRef, Required: true}, Responses: writes(http.StatusOK)}, a.apiTicketLabelUpdate},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:ticket-label-patch", Method: http.MethodPatch, Path: "/api/ticket-labels/<int64:id>/"}, Summary: "Partially update a ticket-label link", Permission: ChangeTicketLabel, AdditionalPermissions: []auth.Permission{ViewTicket, ViewLabel}, Description: writePolicy, RequestBody: &openapi.RequestBody{Schema: patchRef, Required: true}, Responses: writes(http.StatusOK)}, a.apiTicketLabelPatch},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:ticket-label-delete", Method: http.MethodDelete, Path: "/api/ticket-labels/<int64:id>/"}, Summary: "Unlink a label from a ticket", Permission: DeleteTicketLabel, Description: "Deletes only the scoped link in one transaction. Both endpoints and every other link remain. Authentication, CSRF and link delete permission precede lookup.", Responses: []openapi.Response{{Status: http.StatusNoContent, Description: "The link was deleted."}, notFound}}, a.apiTicketLabelDelete},
	}
	operations := make([]openapi.Operation, 0, len(definitions))
	for _, definition := range definitions {
		operation, err := protect(definition.operation, definition.handler)
		if err != nil {
			return nil, nil, err
		}
		operations = append(operations, operation)
	}
	return operations, []openapi.NamedSchema{{Name: "TicketLabel", Schema: output}, {Name: "TicketLabelCreate", Schema: input}, {Name: "TicketLabelUpdate", Schema: input}, {Name: "TicketLabelPatch", Schema: partial}, {Name: "TicketLabelList", Schema: list}}, nil
}

func (a *Application) ticketLabelResponse(status int, value models.TicketLabel) (web.Response, error) {
	encoded, err := a.linkEncoder.Encode(value)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(status, encoded)
}
func (a *Application) apiTicketLabelList(request *web.Request, _ auth.Principal) (web.Response, error) {
	options, valid := pagedListRequest(request.HTTP().URL.RawQuery, false)
	if !valid {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "invalid")))
	}
	page, err := a.listTicketLabels(request.Context(), options)
	if err != nil {
		return web.Response{}, err
	}
	values := make([]serializers.Value, len(page.Items))
	for index, value := range page.Items {
		values[index], err = a.linkEncoder.Encode(value)
		if err != nil {
			return web.Response{}, err
		}
	}
	items, err := serializers.NewList(values...)
	if err != nil {
		return web.Response{}, err
	}
	result, err := serializers.NewObject(serializers.MemberOf("items", items), serializers.MemberOf("count", serializers.Integer(page.Total)), serializers.MemberOf("limit", serializers.Integer(int64(page.Limit))), serializers.MemberOf("offset", serializers.Integer(int64(page.Offset))))
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, result.Value())
}
func (a *Application) apiTicketLabelCreate(request *web.Request, _ auth.Principal) (web.Response, error) {
	values, response, handled, err := a.bindInputSpec(request, a.linkInput, serializers.ModeFull)
	if handled || err != nil {
		return response, err
	}
	ticketValue, _ := values.Get("ticket")
	ticketID, _ := ticketValue.AsInteger()
	labelValue, _ := values.Get("label")
	labelID, _ := labelValue.AsInteger()
	created, err := a.createTicketLabel(request.Context(), ticketID, labelID)
	if err != nil {
		return objectFailure(err)
	}
	return a.ticketLabelResponse(http.StatusCreated, created)
}
func (a *Application) apiTicketLabelDetail(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	value, found, err := a.ticketLabel(request.Context(), a.backend, id)
	if err != nil {
		return web.Response{}, err
	}
	if !found {
		return objectNotFound()
	}
	return a.ticketLabelResponse(http.StatusOK, value)
}
func (a *Application) apiTicketLabelUpdate(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiTicketLabelUpdateMode(request, serializers.ModeFull)
}
func (a *Application) apiTicketLabelPatch(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiTicketLabelUpdateMode(request, serializers.ModePartial)
}
func (a *Application) apiTicketLabelUpdateMode(request *web.Request, mode serializers.Mode) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	values, response, handled, err := a.bindInputSpec(request, a.linkInput, mode)
	if handled || err != nil {
		return response, err
	}
	patch := models.TicketLabelPatch{}
	for _, entry := range values.All() {
		value, _ := entry.Value().AsInteger()
		switch entry.Name() {
		case "ticket":
			patch = patch.WithTicketID(value)
		case "label":
			patch = patch.WithLabelID(value)
		}
	}
	updated, _, err := a.updateTicketLabel(request.Context(), id, patch)
	if err != nil {
		return objectFailure(err)
	}
	return a.ticketLabelResponse(http.StatusOK, updated)
}
func (a *Application) apiTicketLabelDelete(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	if _, err := a.deleteTicketLabel(request.Context(), id); err != nil {
		return objectFailure(err)
	}
	return api.NoContent()
}
func (a *Application) apiTicketDelete(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	if _, err := a.delete(request.Context(), id); err != nil {
		// Only the exact confirmed diagnostic is a normal denial. A joined cleanup
		// error or canceled request must never be published as confirmed protection.
		if protected, ok := err.(*query.ProtectedForeignKeyError); ok && protected.ProtectedSourceRows() > 0 && request.Context().Err() == nil {
			return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "protected")))
		}
		return objectFailure(err)
	}
	return api.NoContent()
}
