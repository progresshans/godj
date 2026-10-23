package helpdesk

import (
	"math"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func (a *Application) costPreviewEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	return endpoint.New(authentication, endpoint.Config[costPreviewQuery, costPreviewPage]{
		Route:       web.Route{Name: "helpdesk:cost-preview", Method: http.MethodGet, Path: "/api/tickets/cost-preview/"},
		Summary:     "Preview expected cost adjustments",
		Description: "Read-only preview of expected_cost * factor + delta for tickets in the application's category. Exact Decimal strings retain every result digit; no rounding or stored value changes occur. Missing costs remain null, including when factor is zero. factor defaults to 1 and accepts up to 6 digits with at most 4 decimal places; delta defaults to 0 and accepts up to 14 digits with at most 2 decimal places. Optional min_cost accepts up to 21 digits with at most 6 decimal places and filters adjusted values before pagination; omission includes null costs. Parameters use canonical Decimal strings without exponent, plus sign, leading zeros or fractional trailing zeros; negative zero is accepted. Results are ordered by adjusted_cost descending, nulls last, then ticket ID ascending. p is a canonical positive page number. Pages contain at most 20 tickets; total_tickets includes all matches before pagination, including empty and past-end pages. Category, count and rows share one read snapshot. ViewTicket is required before parsing or lookup. Unknown/duplicate parameters, malformed encoding, empty values and queries longer than 256 bytes are rejected.",
		Admission:   endpoint.All(ViewTicket), Input: endpoint.Query(a.queries.costPreview), Output: a.responses.costPreview,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "The category and exact cost preview page."}},
		Errors:  []endpoint.Status{{Code: http.StatusBadRequest, Description: "Invalid cost preview query parameters."}, {Code: http.StatusNotFound, Description: "The application's category no longer exists."}},
		Handle: func(request *web.Request, actor auth.Principal, input costPreviewQuery) (output.Prepared[costPreviewPage], error) {
			page, err := a.readCostPreview(request.Context(), actor, input)
			if err != nil {
				return output.Prepared[costPreviewPage]{}, typedEndpointFailure(err)
			}
			response, err := a.responses.costPreview.Prepare(request.Context(), http.StatusOK, page)
			if err != nil {
				return output.Prepared[costPreviewPage]{}, err
			}
			return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
		},
	})
}

func prepareCostPreviewOutput(category output.Shape[models.Category]) (output.Output[costPreviewPage], error) {
	row := output.Object(
		output.Field("id", output.Int64Range(1, math.MaxInt64), func(row costPreviewRow) int64 { return row.id }),
		output.Field("subject", output.String(), func(row costPreviewRow) string { return row.subject }),
		output.Field("expected_cost", output.Nullable(output.Decimal()), func(row costPreviewRow) *decimal.Decimal { return row.original }),
		output.Field("adjusted_cost", output.Nullable(output.Decimal()), func(row costPreviewRow) *decimal.Decimal { return summaryOptionalPointer(row.adjusted) }),
	)
	return output.New(output.Named("CostPreview", output.Object(
		output.Field("category", category, func(page costPreviewPage) models.Category { return page.category }),
		output.Field("page", output.Int64Range(1, costPreviewMaximumPage), func(page costPreviewPage) int64 { return int64(page.query.page) }),
		output.Field("page_size", output.Int64Range(costPreviewPageSize, costPreviewPageSize), func(costPreviewPage) int64 { return costPreviewPageSize }),
		output.Field("factor", output.Decimal(), func(page costPreviewPage) decimal.Decimal { return page.query.factor }),
		output.Field("delta", output.Decimal(), func(page costPreviewPage) decimal.Decimal { return page.query.delta }),
		output.Field("min_cost", output.Nullable(output.Decimal()), func(page costPreviewPage) *decimal.Decimal { return page.query.minimum }),
		output.Field("total_tickets", output.Int64Range(0, math.MaxInt64), func(page costPreviewPage) int64 { return page.tickets.Total }),
		output.Field("results", output.Array(row, 0, costPreviewPageSize), func(page costPreviewPage) []costPreviewRow { return page.tickets.Rows }),
	)), serializers.Limits{})
}
