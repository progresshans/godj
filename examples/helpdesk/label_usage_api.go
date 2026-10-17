package helpdesk

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

func (a *Application) labelUsageEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	return endpoint.New(authentication, endpoint.Config[labelUsageQuery, labelUsagePage]{
		Route:       web.Route{Name: "helpdesk:label-usage", Method: http.MethodGet, Path: "/api/labels/usage/"},
		Summary:     "Read label usage",
		Description: "Labels in the application's category, ordered by label ID, including labels with no tickets. Counts and numeric metrics include only linked tickets in the same category. total counts tickets, open counts tickets that are not closed, and closed is total minus open. expected_cost_total and elapsed_total are sums; effort_average is the mean of present effort values. adjusted_cost_total sums expected_cost * factor + delta for each ticket with a cost. Exact Decimal strings preserve every result digit without changing stored values. factor defaults to 1 and accepts up to 2 whole digits and 4 decimal places; delta defaults to 0 and accepts up to 12 whole digits and 2 decimal places. Missing costs stay null even when factor is zero, and delta is never applied to missing costs. Parameters use canonical Decimal strings without exponent, plus sign, leading zeros or fractional trailing zeros; negative zero is accepted. Missing numeric values, including empty relationships, are null. usage accepts all, used or unused, defaulting to all; used means at least one in-category ticket. p is a canonical positive page number. Pages contain at most 20 labels; total_labels counts matching labels before pagination, including empty and past-end pages. Category, count and rows share one read snapshot. Both ViewLabel and ViewTicket are required before parsing or lookup. Unknown/duplicate parameters, malformed encoding, empty values and queries longer than 128 bytes are rejected.",
		Admission:   endpoint.All(ViewLabel, ViewTicket), Input: endpoint.Query(a.queries.labelUsage), Output: a.responses.labelUsage,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "The current category and filtered label usage page."}},
		Errors:  []endpoint.Status{{Code: http.StatusBadRequest, Description: "Invalid label usage query parameters."}, {Code: http.StatusNotFound, Description: "The application's category no longer exists."}},
		Handle: func(request *web.Request, actor auth.Principal, input labelUsageQuery) (output.Prepared[labelUsagePage], error) {
			page, err := a.readLabelUsage(request.Context(), actor, input)
			if err != nil {
				return output.Prepared[labelUsagePage]{}, typedEndpointFailure(err)
			}
			response, err := a.responses.labelUsage.Prepare(request.Context(), http.StatusOK, page)
			if err != nil {
				return output.Prepared[labelUsagePage]{}, err
			}
			return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
		},
	})
}
