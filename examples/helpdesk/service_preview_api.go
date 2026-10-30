package helpdesk

import (
	"math"
	"net/http"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func (a *Application) servicePreviewEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	return endpoint.New(authentication, endpoint.Config[servicePreviewQuery, servicePreviewPage]{
		Route:       web.Route{Name: "helpdesk:service-preview", Method: http.MethodGet, Path: "/api/tickets/service-preview/"},
		Summary:     "Preview UTC service schedules from ticket dates, times and elapsed durations",
		Description: "Read-only schedule preview for the application's category. service_start combines service_on and service_at in UTC; both must be present. adjusted_start adds shift, finish_at adds the stored elapsed duration, and deadline_slack is due_at minus finish_at. Missing date/time keeps the start and all dependent results null. Missing elapsed keeps finish_at and deadline_slack null; missing due_at keeps only deadline_slack null. Zero durations and midnight remain real values. No server clock, timezone inference or defaults for missing model values are used. shift defaults to 00:00:00 and uses canonical normalized Duration text: an optional nonzero signed day count followed by HH:MM:SS, with six fractional digits when nonzero. Optional min_start filters adjusted_start before counting and pagination; it requires complete RFC 3339 text with an explicit offset and is normalized to UTC microseconds, truncating additional fractional digits. Results sort by adjusted_start ascending, nulls last, then ticket ID. p is a canonical positive page number, page_size is 20 and total_tickets counts all matches. Category, count and rows share one read snapshot. ViewTicket precedes parsing and lookup. Unknown/duplicate parameters, malformed encoding, empty values and raw queries longer than 512 bytes are rejected. Invalid stored operands or arithmetic outside the backend/model range fail the whole response without changing data.",
		Admission:   endpoint.All(ViewTicket), Input: endpoint.Query(a.queries.servicePreview), Output: a.responses.servicePreview,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "The category and service schedule preview page."}},
		Errors:  []endpoint.Status{{Code: http.StatusForbidden, Description: "The current account no longer permits this operation."}, {Code: http.StatusBadRequest, Description: "Invalid service preview parameters."}, {Code: http.StatusNotFound, Description: "The application's category no longer exists."}},
		Handle: func(request *web.Request, actor auth.Principal, input servicePreviewQuery) (output.Prepared[servicePreviewPage], error) {
			page, err := a.readServicePreview(request.Context(), actor, input)
			if err != nil {
				return output.Prepared[servicePreviewPage]{}, typedEndpointFailure(err)
			}
			response, err := a.responses.servicePreview.Prepare(request.Context(), http.StatusOK, page)
			if err != nil {
				return output.Prepared[servicePreviewPage]{}, err
			}
			return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
		},
	})
}

func prepareServicePreviewOutput(category output.Shape[models.Category]) (output.Output[servicePreviewPage], error) {
	row := output.Object(
		output.Field("id", output.Int64Range(1, math.MaxInt64), func(row servicePreviewRow) int64 { return row.id }),
		output.Field("subject", output.String(), func(row servicePreviewRow) string { return row.subject }),
		output.Field("service_on", output.Nullable(output.Date()), func(row servicePreviewRow) *calendar.Date { return row.day }),
		output.Field("service_at", output.Nullable(output.Time()), func(row servicePreviewRow) *clock.Time { return row.clock }),
		output.Field("elapsed", output.Nullable(output.Duration()), func(row servicePreviewRow) *duration.Duration { return row.elapsed }),
		output.Field("due_at", output.Nullable(output.DateTime()), func(row servicePreviewRow) *time.Time { return row.due }),
		output.Field("service_start", output.Nullable(output.DateTime()), func(row servicePreviewRow) *time.Time { return summaryOptionalPointer(row.start) }),
		output.Field("adjusted_start", output.Nullable(output.DateTime()), func(row servicePreviewRow) *time.Time { return summaryOptionalPointer(row.adjusted) }),
		output.Field("finish_at", output.Nullable(output.DateTime()), func(row servicePreviewRow) *time.Time { return summaryOptionalPointer(row.finish) }),
		output.Field("deadline_slack", output.Nullable(output.Duration()), func(row servicePreviewRow) *duration.Duration { return summaryOptionalPointer(row.slack) }),
	)
	return output.New(output.Named("ServicePreview", output.Object(
		output.Field("category", category, func(page servicePreviewPage) models.Category { return page.category }),
		output.Field("page", output.Int64Range(1, servicePreviewMaximumPage), func(page servicePreviewPage) int64 { return int64(page.query.page) }),
		output.Field("page_size", output.Int64Range(servicePreviewPageSize, servicePreviewPageSize), func(servicePreviewPage) int64 { return servicePreviewPageSize }),
		output.Field("shift", output.Duration(), func(page servicePreviewPage) duration.Duration { return page.query.shift }),
		output.Field("min_start", output.Nullable(output.DateTime()), func(page servicePreviewPage) *time.Time { return page.query.minimum }),
		output.Field("total_tickets", output.Int64Range(0, math.MaxInt64), func(page servicePreviewPage) int64 { return page.tickets.Total }),
		output.Field("results", output.Array(row, 0, servicePreviewPageSize), func(page servicePreviewPage) []servicePreviewRow { return page.tickets.Rows }),
	)), serializers.Limits{})
}
