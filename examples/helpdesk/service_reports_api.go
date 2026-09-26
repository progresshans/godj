package helpdesk

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) reportOperations(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error)) ([]openapi.Operation, []openapi.NamedSchema, error) {
	output, err := openapi.ModelResponseSchema(a.reportOutput)
	if err != nil {
		return nil, nil, err
	}
	input, err := openapi.RequestSchema(a.reportInput, serializers.ModeFull)
	if err != nil {
		return nil, nil, err
	}
	partial, err := openapi.RequestSchema(a.reportInput, serializers.ModePartial)
	if err != nil {
		return nil, nil, err
	}
	reportRef, err := openapi.Ref("ServiceReport")
	if err != nil {
		return nil, nil, err
	}
	inputRef, err := openapi.Ref("ServiceReportCreate")
	if err != nil {
		return nil, nil, err
	}
	updateRef, err := openapi.Ref("ServiceReportUpdate")
	if err != nil {
		return nil, nil, err
	}
	patchRef, err := openapi.Ref("ServiceReportPatch")
	if err != nil {
		return nil, nil, err
	}
	optional, err := openapi.Nullable(reportRef)
	if err != nil {
		return nil, nil, err
	}
	list, err := openapi.Array(reportRef)
	if err != nil {
		return nil, nil, err
	}
	errorRef, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return nil, nil, err
	}
	notFound := helpdeskJSONResponse(http.StatusNotFound, "The object does not exist in the selected category.", errorRef)
	badInput := helpdeskJSONResponse(http.StatusBadRequest, "Invalid input, inaccessible/missing ticket, or duplicate report. A preflight duplicate uses ticket/unique; a native conflict uses __all__/unique.", errorRef)
	writeResponses := func(status int) []openapi.Response {
		return []openapi.Response{
			helpdeskJSONResponse(status, "The saved service report.", reportRef), badInput, notFound,
			helpdeskJSONResponse(http.StatusRequestEntityTooLarge, "The JSON body exceeds 4096 bytes.", errorRef),
			helpdeskJSONResponse(http.StatusUnsupportedMediaType, "The body is not supported application/json.", errorRef),
		}
	}
	const writeDescription = "Authentication, permission and CSRF precede parsing. Ticket must identify a positive-ID ticket in the selected category. Existence/scope and uniqueness are rechecked inside the write transaction; self updates exclude their own report. A duplicate rejects the whole write. Summary is required trimmed multiline text. Completed defaults to false for full input; PATCH changes only supplied fields. ID cannot be supplied. PUT requires ticket and summary; PATCH may reassign a ticket. Reading ticket labels in the Admin chooser additionally requires ViewTicket; the API accepts an explicit scoped ticket identifier without exposing the ticket's subject."
	definitions := []struct {
		operation openapi.Operation
		handler   api.AuthenticatedHandler
	}{
		{openapi.Operation{Route: web.Route{Name: "helpdesk:service-report-list", Method: http.MethodGet, Path: "/api/service-reports/"}, Summary: "List service reports", Permission: ViewServiceReport, Description: "Return at most 20 reports in ID order within the selected category. No query parameters are supported.", Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The reports.", list), badInput}}, a.apiReportList},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:service-report-create", Method: http.MethodPost, Path: "/api/service-reports/"}, Summary: "Create a service report", Permission: AddServiceReport, Description: writeDescription, RequestBody: &openapi.RequestBody{Schema: inputRef, Required: true, Description: "One JSON object; 4096-byte body limit. Unknown fields, duplicate members, null required fields and trailing data are rejected."}, Responses: writeResponses(http.StatusCreated)}, a.apiReportCreate},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:service-report-detail", Method: http.MethodGet, Path: "/api/service-reports/<int64:id>/"}, Summary: "Read a service report", Permission: ViewServiceReport, Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The report.", reportRef), notFound}}, a.apiReportDetail},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:service-report-update", Method: http.MethodPut, Path: "/api/service-reports/<int64:id>/"}, Summary: "Update a service report", Permission: ChangeServiceReport, Description: writeDescription, RequestBody: &openapi.RequestBody{Schema: updateRef, Required: true}, Responses: writeResponses(http.StatusOK)}, a.apiReportUpdate},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:service-report-patch", Method: http.MethodPatch, Path: "/api/service-reports/<int64:id>/"}, Summary: "Partially update a service report", Permission: ChangeServiceReport, Description: writeDescription, RequestBody: &openapi.RequestBody{Schema: patchRef, Required: true}, Responses: writeResponses(http.StatusOK)}, a.apiReportPatch},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:service-report-delete", Method: http.MethodDelete, Path: "/api/service-reports/<int64:id>/"}, Summary: "Delete a service report", Permission: DeleteServiceReport, Description: "Delete the scoped report in one transaction, retaining its ticket. Authentication, CSRF and permission checks precede the object query.", Responses: []openapi.Response{{Status: http.StatusNoContent, Description: "The report was deleted."}, notFound}}, a.apiReportDelete},
		{openapi.Operation{Route: web.Route{Name: "helpdesk:ticket-service-report", Method: http.MethodGet, Path: "/api/tickets/<int64:id>/service-report/"}, Summary: "Read a ticket's service report", Permission: ViewServiceReport, Description: "An existing scoped ticket with no report returns JSON null. A missing or out-of-category ticket returns 404. Report view permission covers this relation key and absence; the ticket subject is not exposed.", Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The report, or null when absent.", optional), notFound}}, a.apiTicketReport},
	}
	operations := make([]openapi.Operation, 0, len(definitions))
	for _, definition := range definitions {
		operation, err := protect(definition.operation, definition.handler)
		if err != nil {
			return nil, nil, err
		}
		operations = append(operations, operation)
	}
	return operations, []openapi.NamedSchema{{Name: "ServiceReport", Schema: output}, {Name: "ServiceReportCreate", Schema: input}, {Name: "ServiceReportUpdate", Schema: input}, {Name: "ServiceReportPatch", Schema: partial}}, nil
}

func (a *Application) reportResponse(status int, value models.ServiceReport) (web.Response, error) {
	encoded, err := a.reportEncoder.Encode(value)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(status, encoded)
}

func (a *Application) apiReportList(request *web.Request, _ auth.Principal) (web.Response, error) {
	// The collection has a fixed bounded page. Reject undeclared parameters
	// instead of accepting an accidental filtering or authorization surface.
	raw := request.HTTP().URL.RawQuery
	if len(raw) > 2048 || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "invalid")))
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 0 {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "invalid")))
	}
	page, err := a.listReports(request.Context(), admin.ListRequest{Limit: 20})
	if err != nil {
		return web.Response{}, err
	}
	encoded := make([]serializers.Value, len(page.Items))
	for index, value := range page.Items {
		encoded[index], err = a.reportEncoder.Encode(value)
		if err != nil {
			return web.Response{}, err
		}
	}
	list, err := serializers.NewList(encoded...)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, list)
}
func (a *Application) apiReportCreate(request *web.Request, _ auth.Principal) (web.Response, error) {
	values, response, handled, err := a.bindInputSpec(request, a.reportInput, serializers.ModeFull)
	if handled || err != nil {
		return response, err
	}
	keyValue, _ := values.Get("ticket")
	key, _ := keyValue.AsInteger()
	textValue, _ := values.Get("summary")
	summary, _ := textValue.AsString()
	boolValue, _ := values.Get("completed")
	completed, _ := boolValue.AsBoolean()
	created, err := a.createReport(request.Context(), serviceReportInput{ticketID: key, summary: summary, completed: completed})
	if err != nil {
		return objectFailure(err)
	}
	return a.reportResponse(http.StatusCreated, created)
}
func (a *Application) apiReportDetail(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	value, found, err := a.report(request.Context(), a.backend, id)
	if err != nil {
		return web.Response{}, err
	}
	if !found {
		return objectNotFound()
	}
	return a.reportResponse(http.StatusOK, value)
}
func (a *Application) apiReportUpdate(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiReportUpdateMode(request, serializers.ModeFull)
}
func (a *Application) apiReportPatch(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiReportUpdateMode(request, serializers.ModePartial)
}
func (a *Application) apiReportUpdateMode(request *web.Request, mode serializers.Mode) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	values, response, handled, err := a.bindInputSpec(request, a.reportInput, mode)
	if handled || err != nil {
		return response, err
	}
	patch := models.ServiceReportPatch{}
	for _, entry := range values.All() {
		switch entry.Name() {
		case "ticket":
			key, _ := entry.Value().AsInteger()
			patch = patch.WithTicketID(key)
		case "summary":
			text, _ := entry.Value().AsString()
			patch = patch.WithSummary(text)
		case "completed":
			value, _ := entry.Value().AsBoolean()
			patch = patch.WithCompleted(value)
		}
	}
	updated, _, err := a.updateReport(request.Context(), id, patch)
	if err != nil {
		return objectFailure(err)
	}
	return a.reportResponse(http.StatusOK, updated)
}
func (a *Application) apiReportDelete(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	if _, err := a.deleteReport(request.Context(), id); err != nil {
		return objectFailure(err)
	}
	return web.NewResponse(http.StatusNoContent, nil, nil)
}
func (a *Application) apiTicketReport(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := objectRequestID(request)
	if !valid {
		return objectNotFound()
	}
	owner, found, err := a.objects.ModelsTicket.Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc()).SelectRelated(a.objects.ModelsTicket.Related.ServiceReport).First(request.Context())
	if err != nil {
		return web.Response{}, err
	}
	if !found {
		return objectNotFound()
	}
	child, present, err := owner.ServiceReport(request.Context())
	if err != nil {
		return web.Response{}, err
	}
	if !present {
		return api.JSON(http.StatusOK, serializers.Null())
	}
	raw, err := child.Unwrap()
	if err != nil {
		return web.Response{}, err
	}
	return a.reportResponse(http.StatusOK, raw)
}
