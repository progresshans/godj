package helpdesk

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

func (a *Application) serviceReportStatusEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	return endpoint.New(authentication, endpoint.Config[serviceReportStatusQuery, serviceReportStatusPage]{
		Route:       web.Route{Name: "helpdesk:service-report-status", Method: http.MethodGet, Path: "/api/service-reports/status/"},
		Summary:     "Compare report and ticket statuses",
		Description: "Reports whose ticket belongs to the application's category, ordered by report ID. Report completion and ticket closure are independent; matched only reports whether their flags are equal. comparison accepts all, same or different, defaulting to all. p is a canonical positive page number. Pages contain at most 20 rows; total_reports counts filtered rows before pagination, including on empty and past-end pages. Category identity/name, rows and count share one read snapshot. Both ViewServiceReport and ViewTicket are required before parsing or lookup. The read performs no writes, audit, ticket-body loading or digest repair. Unknown/duplicate parameters, malformed encoding, empty values and queries longer than 128 bytes are rejected.",
		Admission:   endpoint.All(ViewServiceReport, ViewTicket),
		Input:       endpoint.Query(a.queries.reportStatus),
		Output:      a.responses.reportStatus,
		Success:     []endpoint.Status{{Code: http.StatusOK, Description: "The current category and filtered report status page."}},
		Errors: []endpoint.Status{
			{Code: http.StatusBadRequest, Description: "Invalid report status query parameters."},
			{Code: http.StatusNotFound, Description: "The application's category no longer exists."},
		},
		Handle: func(request *web.Request, actor auth.Principal, input serviceReportStatusQuery) (output.Prepared[serviceReportStatusPage], error) {
			page, err := a.readServiceReportStatus(request.Context(), actor, input)
			if err != nil {
				return output.Prepared[serviceReportStatusPage]{}, typedEndpointFailure(err)
			}
			response, err := a.responses.reportStatus.Prepare(request.Context(), http.StatusOK, page)
			if err != nil {
				return output.Prepared[serviceReportStatusPage]{}, err
			}
			return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
		},
	})
}
