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
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/timezone"
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
		Summary:     "Group ticket deadlines by a calendar period in an explicit time zone",
		Description: "Read-only deadline groups in the application's current category. period is day, week, month, quarter or year and defaults to day; weeks begin on Monday and quarters on January, April, July or October 1. time_zone is an exact IANA name or alias from the pinned database and defaults to UTC. period_start is the Gregorian start date in that time zone; date bounds use the same local calendar. DST, non-hour offsets and historical rules do not change the meaning of stored instants. first_due_at and last_due_at are the actual earliest and latest complete UTC instants in that period, with microsecond precision. total counts all tickets, open counts closed=false, and overdue counts only open tickets with due_at strictly before the required explicit as_of instant. Deadlines equal to as_of are not overdue. Missing deadlines form one null-start group with null instants and zero overdue; this group is last and is excluded by any date bound. Optional from_date and until_date select period_start inclusively, using canonical YYYY-MM-DD dates in years 0001..9999; they do not clip ticket deadlines within a period. They apply before counting groups and pagination; from_date must not follow until_date. as_of requires complete RFC 3339 text and an explicit offset and is normalized to UTC microseconds; the server clock is never substituted. p is a canonical positive page number, page_size is 20, and total_groups remains available on an empty page. Category, groups and total share one read snapshot. ViewTicket precedes parsing and all model reads. Unknown/duplicate parameters, empty values, malformed encoding and raw queries longer than 512 bytes are rejected. Invalid native temporal data or a local date outside years 0001..9999 fails the whole response, even when an aggregate date bound would hide that row. Tickets, relations, reports and audit remain unchanged.",
		Admission:   endpoint.All(ViewTicket), Input: input, Output: a.responses.deadlineCalendar,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "The current category's deadline periods at the supplied reference time."}},
		Errors: []endpoint.Status{
			{Code: http.StatusBadRequest, Description: "Invalid reference time, period, time zone, calendar date, period-start range or page."},
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
		output.Field("period_start", output.Nullable(output.Date()), func(row deadlineCalendarRow) *calendar.Date { return summaryOptionalPointer(row.start) }),
		output.Field("total", output.Int64Range(1, math.MaxInt64), func(row deadlineCalendarRow) int64 { return row.total }),
		output.Field("open", count, func(row deadlineCalendarRow) int64 { return row.open }),
		output.Field("overdue", count, func(row deadlineCalendarRow) int64 { return row.overdue }),
		output.Field("first_due_at", output.Nullable(output.DateTime()), func(row deadlineCalendarRow) *time.Time { return summaryOptionalPointer(row.first) }),
		output.Field("last_due_at", output.Nullable(output.DateTime()), func(row deadlineCalendarRow) *time.Time { return summaryOptionalPointer(row.last) }),
	)
	return output.New(output.Named("DeadlineCalendar", output.Object(
		output.Field("category", category, func(page deadlineCalendarPage) models.Category { return page.category }),
		output.Field("page", output.Int64Range(1, deadlineCalendarMaximumPage), func(page deadlineCalendarPage) int64 { return int64(page.query.page) }),
		output.Field("page_size", output.Int64Range(deadlineCalendarPageSize, deadlineCalendarPageSize), func(deadlineCalendarPage) int64 { return deadlineCalendarPageSize }),
		output.Field("as_of", output.DateTime(), func(page deadlineCalendarPage) time.Time { return page.query.asOf }),
		output.Field("period", output.StringChoices(deadlineCalendarPeriods()...), func(page deadlineCalendarPage) string { return page.query.period }),
		output.Field("time_zone", output.TimeZone(), func(page deadlineCalendarPage) timezone.Zone { return page.query.zone }),
		output.Field("from_date", output.Nullable(output.Date()), func(page deadlineCalendarPage) *calendar.Date { return page.query.from }),
		output.Field("until_date", output.Nullable(output.Date()), func(page deadlineCalendarPage) *calendar.Date { return page.query.until }),
		output.Field("total_groups", count, func(page deadlineCalendarPage) int64 { return page.groups.Total }),
		output.Field("results", output.Array(row, 0, deadlineCalendarPageSize), func(page deadlineCalendarPage) []deadlineCalendarRow { return page.groups.Rows }),
	)), serializers.Limits{})
}
