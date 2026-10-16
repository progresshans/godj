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
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) deadlineCalendarEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	input := endpoint.Resolve(endpoint.Query(a.queries.deadlineCalendar), func(_ *web.Request, _ auth.Principal, value deadlineCalendarQuery) (deadlineCalendarQuery, error) {
		if !value.validRange() {
			return deadlineCalendarQuery{}, endpoint.Reject(http.StatusBadRequest, "validation_error", validation.NewErrors(validation.New(validation.NonField, "invalid_range")))
		}
		return value, nil
	})
	return endpoint.New(authentication, endpoint.Config[deadlineCalendarQuery, deadlineCalendarPage]{
		Route:       web.Route{Name: "helpdesk:deadline-calendar", Method: http.MethodGet, Path: "/api/tickets/deadline-calendar/"},
		Summary:     "Group ticket deadlines by their UTC calendar day",
		Description: "Read-only daily deadline groups in the application's current category. date is the UTC Gregorian day; first_due_time and last_due_time are its earliest and latest UTC clocks with microsecond precision. total counts all tickets, open counts closed=false, and overdue counts only open tickets with due_at strictly before the required explicit as_of instant. Deadlines equal to as_of are not overdue. Missing deadlines form one null-date group with null clocks and zero overdue; this group is last and is excluded by any date bound. Optional from_date and until_date are inclusive canonical YYYY-MM-DD dates in years 0001..9999. They apply to groups before counting and pagination; from_date must not follow until_date. as_of requires complete RFC 3339 text and an explicit offset and is normalized to UTC microseconds; the server clock is never substituted. p is a canonical positive page number, page_size is 20, and total_groups remains available on an empty page. Category, groups and total share one read snapshot. ViewTicket precedes parsing and all model reads. Unknown/duplicate parameters, empty values, malformed encoding and raw queries longer than 512 bytes are rejected. Invalid native temporal data fails the whole response. Tickets, relations, reports and audit remain unchanged.",
		Admission:   endpoint.All(ViewTicket), Input: input, Output: a.responses.deadlineCalendar,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "The current category's daily deadline groups at the supplied reference time."}},
		Errors: []endpoint.Status{
			{Code: http.StatusBadRequest, Description: "Invalid reference time, calendar date, date range or page."},
			{Code: http.StatusNotFound, Description: "The application's category no longer exists."},
		},
		Handle: func(request *web.Request, actor auth.Principal, input deadlineCalendarQuery) (output.Prepared[deadlineCalendarPage], error) {
			page, err := a.readDeadlineCalendar(request.Context(), actor, input)
			if err != nil {
				return output.Prepared[deadlineCalendarPage]{}, typedEndpointFailure(err)
			}
			response, err := a.responses.deadlineCalendar.Prepare(request.Context(), http.StatusOK, page)
			if err != nil {
				return output.Prepared[deadlineCalendarPage]{}, err
			}
			return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
		},
	})
}

func prepareDeadlineCalendarOutput(category output.Shape[models.Category]) (output.Output[deadlineCalendarPage], error) {
	count := output.Int64Range(0, math.MaxInt64)
	row := output.Object(
		output.Field("date", output.Nullable(output.Date()), func(row deadlineCalendarRow) *calendar.Date { return summaryOptionalPointer(row.day) }),
		output.Field("total", output.Int64Range(1, math.MaxInt64), func(row deadlineCalendarRow) int64 { return row.total }),
		output.Field("open", count, func(row deadlineCalendarRow) int64 { return row.open }),
		output.Field("overdue", count, func(row deadlineCalendarRow) int64 { return row.overdue }),
		output.Field("first_due_time", output.Nullable(output.Time()), func(row deadlineCalendarRow) *clock.Time { return summaryOptionalPointer(row.first) }),
		output.Field("last_due_time", output.Nullable(output.Time()), func(row deadlineCalendarRow) *clock.Time { return summaryOptionalPointer(row.last) }),
	)
	return output.New(output.Named("DeadlineCalendar", output.Object(
		output.Field("category", category, func(page deadlineCalendarPage) models.Category { return page.category }),
		output.Field("page", output.Int64Range(1, deadlineCalendarMaximumPage), func(page deadlineCalendarPage) int64 { return int64(page.query.page) }),
		output.Field("page_size", output.Int64Range(deadlineCalendarPageSize, deadlineCalendarPageSize), func(deadlineCalendarPage) int64 { return deadlineCalendarPageSize }),
		output.Field("as_of", output.DateTime(), func(page deadlineCalendarPage) time.Time { return page.query.asOf }),
		output.Field("from_date", output.Nullable(output.Date()), func(page deadlineCalendarPage) *calendar.Date { return page.query.from }),
		output.Field("until_date", output.Nullable(output.Date()), func(page deadlineCalendarPage) *calendar.Date { return page.query.until }),
		output.Field("total_groups", count, func(page deadlineCalendarPage) int64 { return page.groups.Total }),
		output.Field("results", output.Array(row, 0, deadlineCalendarPageSize), func(page deadlineCalendarPage) []deadlineCalendarRow { return page.groups.Rows }),
	)), serializers.Limits{})
}
