package helpdesk

import (
	"fmt"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

func (a *Application) ticketSummaryOperation(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error), failure openapi.Schema) (openapi.Operation, error) {
	ref := a.responses.summary.Schema()
	operation, err := protect(openapi.Operation{
		Route:   web.Route{Name: "helpdesk:ticket-summary", Method: http.MethodGet, Path: "/api/tickets/summary/"},
		Summary: "Summarize tickets by priority", Permission: ViewTicket,
		Description: fmt.Sprintf("Groups tickets in the application's fixed category by their stored priority, retaining null and every legacy int64 value. Total counts all tickets; open counts those whose closed flag is false. min_open filters groups, then results are ordered by open count descending and priority descending with null last. Pages contain at most 20 groups; total_groups is the count after filtering and before pagination, including on empty or past-end pages. Category identity/name, rows and total share one read snapshot per request. Separate page requests observe current state independently. This is a read-only operation without audit, ticket-body loading or digest repair. ViewTicket is the only business permission, checked before parsing and lookup. Query strings are limited to %d bytes; unknown/duplicate parameters, empty values, malformed encoding and non-canonical decimal integers are rejected.", a.queries.summary.MaxBytes()),
		Parameters:  a.queries.summary.Parameters(),
		Responses: []openapi.Response{
			helpdeskJSONResponse(http.StatusOK, "The current category and consistent filtered group page.", ref),
			helpdeskJSONResponse(http.StatusBadRequest, "Invalid summary query parameters.", failure),
			helpdeskJSONResponse(http.StatusNotFound, "The application's current category no longer exists.", failure),
		},
	}, a.apiTicketSummary)
	return operation, err
}

func (a *Application) apiTicketSummary(request *web.Request, _ auth.Principal) (web.Response, error) {
	input, diagnostics, err := a.queries.summary.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return web.Response{}, err
	}
	if !diagnostics.Empty() {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
	}
	page, err := a.readTicketSummary(request.Context(), input)
	if err != nil {
		return objectFailure(err)
	}
	response, err := a.responses.summary.JSON(request.Context(), http.StatusOK, page)
	if err != nil {
		return web.Response{}, err
	}
	return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
}
