package helpdesk

import (
	"fmt"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

func (a *Application) ticketSummaryEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	return endpoint.New(authentication, endpoint.Config[ticketSummaryQuery, ticketSummaryPage]{
		Route:       web.Route{Name: "helpdesk:ticket-summary", Method: http.MethodGet, Path: "/api/tickets/summary/"},
		Summary:     "Summarize tickets by priority",
		Description: fmt.Sprintf("Groups tickets in the application's fixed category by their stored priority, retaining null and every legacy int64 value. Total counts all tickets; open counts those whose closed flag is false. min_open filters groups, then results are ordered by open count descending and priority descending with null last. Pages contain at most 20 groups; total_groups is the count after filtering and before pagination, including on empty or past-end pages. Category identity/name, rows and total share one read snapshot per request. Separate page requests observe current state independently. This is a read-only operation without audit, ticket-body loading or digest repair. ViewTicket is the only business permission, checked before parsing and lookup. Query strings are limited to %d bytes; unknown/duplicate parameters, empty values, malformed encoding and non-canonical decimal integers are rejected.", a.queries.summary.MaxBytes()),
		Admission:   endpoint.All(ViewTicket),
		Input:       endpoint.Query(a.queries.summary),
		Output:      a.responses.summary,
		Success:     []endpoint.Status{{Code: http.StatusOK, Description: "The current category and consistent filtered group page."}},
		Errors: []endpoint.Status{
			{Code: http.StatusBadRequest, Description: "Invalid summary query parameters."},
			{Code: http.StatusNotFound, Description: "The application's current category no longer exists."},
		},
		Handle: a.apiTicketSummary,
	})
}

func (a *Application) apiTicketSummary(request *web.Request, _ auth.Principal, query ticketSummaryQuery) (output.Prepared[ticketSummaryPage], error) {
	page, err := a.readTicketSummary(request.Context(), query)
	if err != nil {
		return output.Prepared[ticketSummaryPage]{}, typedEndpointFailure(err)
	}
	response, err := a.responses.summary.Prepare(request.Context(), http.StatusOK, page)
	if err != nil {
		return output.Prepared[ticketSummaryPage]{}, err
	}
	return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
}
