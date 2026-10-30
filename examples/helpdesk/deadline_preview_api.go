package helpdesk

import (
	"math"
	"net/http"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func (a *Application) deadlinePreviewEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	return endpoint.New(authentication, endpoint.Config[deadlinePreviewQuery, deadlinePreviewPage]{
		Route:       web.Route{Name: "helpdesk:deadline-preview", Method: http.MethodGet, Path: "/api/tickets/deadline-preview/"},
		Summary:     "Preview ticket deadline shifts at an explicit reference time",
		Description: "Read-only preview of due_at + shift and adjusted_due_at - as_of for tickets in the application's category. as_of is required RFC 3339 text with an explicit offset and complete clock; it is normalized to UTC microseconds, truncating additional fractional digits. No server clock is substituted. shift defaults to 00:00:00. shift and optional min_remaining use canonical normalized Duration strings: an optional nonzero signed day count followed by HH:MM:SS and six fractional digits when nonzero. Model day bounds are -999999999 through 999999999. min_remaining filters computed remaining durations before counting and pagination; omission includes unset deadlines. Missing due_at stays null in every computed result, including a zero shift. Results are ordered by remaining duration ascending, nulls last, then ticket ID. p is a canonical positive page number; at most 20 rows are returned, and total_tickets includes all matches before pagination. as_of, shift and min_remaining are returned explicitly. Category, count and rows share one read snapshot. ViewTicket precedes parsing and lookup. Unknown/duplicate parameters, malformed encoding, empty values and raw queries longer than 512 bytes are rejected. A valid input whose native arithmetic exceeds its backend or model range fails the whole response without changing stored data.",
		Admission:   endpoint.All(ViewTicket), Input: endpoint.Query(a.queries.deadlinePreview), Output: a.responses.deadlinePreview,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "The category and deadline preview page at the supplied reference time."}},
		Errors:  []endpoint.Status{{Code: http.StatusForbidden, Description: "The current account no longer permits this operation."}, {Code: http.StatusBadRequest, Description: "Invalid deadline preview parameters, including a missing reference time."}, {Code: http.StatusNotFound, Description: "The application's category no longer exists."}},
		Handle: func(request *web.Request, actor auth.Principal, input deadlinePreviewQuery) (output.Prepared[deadlinePreviewPage], error) {
			page, err := a.readDeadlinePreview(request.Context(), actor, input)
			if err != nil {
				return output.Prepared[deadlinePreviewPage]{}, typedEndpointFailure(err)
			}
			response, err := a.responses.deadlinePreview.Prepare(request.Context(), http.StatusOK, page)
			if err != nil {
				return output.Prepared[deadlinePreviewPage]{}, err
			}
			return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
		},
	})
}

func prepareDeadlinePreviewOutput(category output.Shape[models.Category]) (output.Output[deadlinePreviewPage], error) {
	row := output.Object(
		output.Field("id", output.Int64Range(1, math.MaxInt64), func(row deadlinePreviewRow) int64 { return row.id }),
		output.Field("subject", output.String(), func(row deadlinePreviewRow) string { return row.subject }),
		output.Field("due_at", output.Nullable(output.DateTime()), func(row deadlinePreviewRow) *time.Time { return row.original }),
		output.Field("adjusted_due_at", output.Nullable(output.DateTime()), func(row deadlinePreviewRow) *time.Time { return summaryOptionalPointer(row.adjusted) }),
		output.Field("remaining", output.Nullable(output.Duration()), func(row deadlinePreviewRow) *duration.Duration { return summaryOptionalPointer(row.remaining) }),
	)
	return output.New(output.Named("DeadlinePreview", output.Object(
		output.Field("category", category, func(page deadlinePreviewPage) models.Category { return page.category }),
		output.Field("page", output.Int64Range(1, deadlinePreviewMaximumPage), func(page deadlinePreviewPage) int64 { return int64(page.query.page) }),
		output.Field("page_size", output.Int64Range(deadlinePreviewPageSize, deadlinePreviewPageSize), func(deadlinePreviewPage) int64 { return deadlinePreviewPageSize }),
		output.Field("as_of", output.DateTime(), func(page deadlinePreviewPage) time.Time { return page.query.asOf }),
		output.Field("shift", output.Duration(), func(page deadlinePreviewPage) duration.Duration { return page.query.shift }),
		output.Field("min_remaining", output.Nullable(output.Duration()), func(page deadlinePreviewPage) *duration.Duration { return page.query.minimum }),
		output.Field("total_tickets", output.Int64Range(0, math.MaxInt64), func(page deadlinePreviewPage) int64 { return page.tickets.Total }),
		output.Field("results", output.Array(row, 0, deadlinePreviewPageSize), func(page deadlinePreviewPage) []deadlinePreviewRow { return page.tickets.Rows }),
	)), serializers.Limits{})
}
