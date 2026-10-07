package helpdesk

import (
	"fmt"
	"math"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) labelOperations(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error)) ([]openapi.Operation, []openapi.NamedSchema, error) {
	input, err := a.labelInput.Schema(serializers.ModeFull)
	if err != nil {
		return nil, nil, err
	}
	partial, err := a.labelInput.Schema(serializers.ModePartial)
	if err != nil {
		return nil, nil, err
	}
	labelRef, err := openapi.Ref("Label")
	if err != nil {
		return nil, nil, err
	}
	inputRef, err := openapi.Ref("LabelCreate")
	if err != nil {
		return nil, nil, err
	}
	updateRef, err := openapi.Ref("LabelUpdate")
	if err != nil {
		return nil, nil, err
	}
	patchRef, err := openapi.Ref("LabelPatch")
	if err != nil {
		return nil, nil, err
	}
	listRef, err := openapi.Ref("LabelList")
	if err != nil {
		return nil, nil, err
	}
	items, err := openapi.Array(labelRef)
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
	notFound := helpdeskJSONResponse(http.StatusNotFound, "The label or assigned category does not exist in the selected scope.", errorRef)
	badInput := helpdeskJSONResponse(http.StatusBadRequest, "Invalid input or duplicate name in this category. Preflight duplicates use __all__/unique_together; a concurrent native conflict uses __all__/unique.", errorRef)
	writes := func(status int) []openapi.Response {
		return []openapi.Response{helpdeskJSONResponse(status, "The saved label.", labelRef), badInput, notFound,
			helpdeskJSONResponse(http.StatusRequestEntityTooLarge, "The JSON body exceeds 4096 bytes.", errorRef),
			helpdeskJSONResponse(http.StatusUnsupportedMediaType, "The body is not application/json.", errorRef)}
	}
	const policy = "Authentication, permission and CSRF precede parsing. Name is trimmed, required and at most 64 Unicode code points after trim. Category is assigned by the server and cannot be supplied, as cannot id. Names are unique only within that category using the database's exact equality. Category membership and the complete (category, name) candidate are checked in the write transaction. A confirmed conflict rolls back the write; execution failures and uncertain outcomes remain errors. PUT requires name; PATCH preserves omitted name and an empty object returns the existing scoped label."
	definitions := []struct {
		operation openapi.Operation
		handler   api.AuthenticatedHandler
	}{
		{openapi.Operation{Route: web.Route{Name: "helpdesk:label-list", Method: http.MethodGet, Path: "/api/labels/"}, Summary: "List category labels", Permission: ViewLabel, Description: fmt.Sprintf("Returns labels in ID order within the assigned category. Count is the matching total before pagination. Search is a literal case-insensitive name substring; empty means no filter. Unknown/duplicate parameters, invalid encoding, NUL and query strings over %d bytes return 400. Authentication and permission precede query parsing. Count and items are separate reads, so concurrent changes can shift pages.", a.queries.labels.MaxBytes()), Parameters: a.queries.labels.Parameters(), Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The scoped page.", listRef), badInput}}, a.apiLabelList},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:label-create", Method: http.MethodPost, Path: "/api/labels/"}, Summary: "Create a category label", Permission: AddLabel, Description: policy, RequestBody: &openapi.RequestBody{Schema: inputRef, Required: true, Description: "One JSON object; unknown/duplicate members, null name and trailing data are rejected."}, Responses: writes(http.StatusCreated)}, a.apiLabelCreate},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:label-detail", Method: http.MethodGet, Path: "/api/labels/<int64:id>/"}, Summary: "Read a category label", Permission: ViewLabel, Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The label.", labelRef), notFound}}, a.apiLabelDetail},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:label-update", Method: http.MethodPut, Path: "/api/labels/<int64:id>/"}, Summary: "Update a category label", Permission: ChangeLabel, Description: policy, RequestBody: &openapi.RequestBody{Schema: updateRef, Required: true}, Responses: writes(http.StatusOK)}, a.apiLabelUpdate},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:label-patch", Method: http.MethodPatch, Path: "/api/labels/<int64:id>/"}, Summary: "Partially update a category label", Permission: ChangeLabel, Description: policy, RequestBody: &openapi.RequestBody{Schema: patchRef, Required: true}, Responses: writes(http.StatusOK)}, a.apiLabelPatch},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:label-delete", Method: http.MethodDelete, Path: "/api/labels/<int64:id>/"}, Summary: "Delete a category label", Permission: DeleteLabel, Description: "Deletes the scoped label and its ticket links in one coordinated transaction. Its category, tickets and links to other labels remain. Authentication, CSRF and permission precede lookup.", Responses: []openapi.Response{{Status: http.StatusNoContent, Description: "The label and its ticket links were deleted."}, notFound}}, a.apiLabelDelete},
	}
	operations := make([]openapi.Operation, 0, len(definitions))
	for _, definition := range definitions {
		operation, err := protect(definition.operation, definition.handler)
		if err != nil {
			return nil, nil, err
		}
		operations = append(operations, operation)
	}
	return operations, []openapi.NamedSchema{{Name: "LabelUpdate", Schema: input}, {Name: "LabelPatch", Schema: partial}, {Name: "LabelList", Schema: list}}, nil
}

func (a *Application) apiLabelList(request *web.Request, _ auth.Principal) (web.Response, error) {
	options, diagnostics, err := a.queries.labels.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "invalid")))
	}
	page, err := a.listLabels(request.Context(), options)
	if err != nil {
		return web.Response{}, err
	}
	values := make([]serializers.Value, len(page.Items))
	for index, value := range page.Items {
		values[index], err = a.labelEncoder.Encode(value)
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

func (a *Application) labelResponse(status int, value models.Label) (web.Response, error) {
	encoded, err := a.labelEncoder.Encode(value)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(status, encoded)
}

func (a *Application) apiLabelCreate(request *web.Request, _ auth.Principal) (web.Response, error) {
	values, response, handled, err := bindTypedInput(request, a.labelInput, serializers.ModeFull)
	if handled || err != nil {
		return response, err
	}
	name, _ := values.name.Get()
	created, err := a.createLabel(request.Context(), name)
	if err != nil {
		return objectFailure(err)
	}
	return a.labelResponse(http.StatusCreated, created)
}

func (a *Application) apiLabelDetail(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	value, found, err := a.label(request.Context(), a.backend, id)
	if err != nil {
		return web.Response{}, err
	}
	if !found {
		return objectNotFound()
	}
	return a.labelResponse(http.StatusOK, value)
}

func (a *Application) apiLabelUpdate(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiLabelUpdateMode(request, serializers.ModeFull)
}
func (a *Application) apiLabelPatch(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiLabelUpdateMode(request, serializers.ModePartial)
}
func (a *Application) apiLabelUpdateMode(request *web.Request, mode serializers.Mode) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	values, response, handled, err := bindTypedInput(request, a.labelInput, mode)
	if handled || err != nil {
		return response, err
	}
	patch := models.LabelPatch{}
	if name, present := values.name.Get(); present {
		patch = patch.WithName(name)
	}
	updated, _, err := a.updateLabel(request.Context(), id, patch)
	if err != nil {
		return objectFailure(err)
	}
	return a.labelResponse(http.StatusOK, updated)
}

func (a *Application) apiLabelDelete(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	if _, err := a.deleteLabel(request.Context(), id); err != nil {
		return objectFailure(err)
	}
	return web.NewResponse(http.StatusNoContent, nil, nil)
}
