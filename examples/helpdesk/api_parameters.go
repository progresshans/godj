package helpdesk

import (
	"math"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
)

type apiQueries struct {
	deadlineCalendar parameters.Query[deadlineCalendarQuery]
	deadlinePreview  parameters.Query[deadlinePreviewQuery]
	costPreview      parameters.Query[costPreviewQuery]
	labelUsage       parameters.Query[labelUsageQuery]
	summary          parameters.Query[ticketSummaryQuery]
	reportStatus     parameters.Query[serviceReportStatusQuery]
	labels           parameters.Query[admin.ListRequest]
	links            parameters.Query[admin.ListRequest]
}

func prepareAPIQueries() (apiQueries, error) {
	var result apiQueries
	var err error
	result.deadlineCalendar, err = parameters.New(512,
		parameters.Field("p", parameters.Default(parameters.CanonicalInt64(1, deadlineCalendarMaximumPage), 1), func(query *deadlineCalendarQuery, value int64) { query.page = int(value) }, "Page number; canonical positive decimal integer."),
		parameters.Field("as_of", parameters.Required(parameters.RFC3339DateTime(64)), func(query *deadlineCalendarQuery, value time.Time) { query.asOf = value }, "Explicit reference instant for counting overdue open tickets; normalized to UTC microseconds."),
		parameters.Field("period", parameters.Default(parameters.StringChoices(deadlineCalendarPeriods()...), "day"), func(query *deadlineCalendarQuery, value string) { query.period = value }, "Calendar period: day, week, month, quarter or year; defaults to day. Weeks begin on Monday; all periods use UTC."),
		parameters.Field("from_date", parameters.Optional(parameters.CanonicalDate()), func(query *deadlineCalendarQuery, value *calendar.Date) { query.from = value }, "Optional inclusive first period start date, exactly YYYY-MM-DD. Excludes unset deadlines."),
		parameters.Field("until_date", parameters.Optional(parameters.CanonicalDate()), func(query *deadlineCalendarQuery, value *calendar.Date) { query.until = value }, "Optional inclusive last period start date, exactly YYYY-MM-DD; must not precede from_date. Excludes unset deadlines."),
	)
	if err != nil {
		return apiQueries{}, err
	}
	result.deadlinePreview, err = parameters.New(512,
		parameters.Field("p", parameters.Default(parameters.CanonicalInt64(1, deadlinePreviewMaximumPage), 1), func(query *deadlinePreviewQuery, value int64) { query.page = int(value) }, "Page number; canonical positive decimal integer."),
		parameters.Field("as_of", parameters.Required(parameters.RFC3339DateTime(64)), func(query *deadlinePreviewQuery, value time.Time) { query.asOf = value }, "Explicit reference time with complete clock and offset; normalized to UTC microseconds."),
		parameters.Field("shift", parameters.Default(parameters.CanonicalDuration(), duration.Duration{}), func(query *deadlinePreviewQuery, value duration.Duration) { query.shift = value }, "Normalized elapsed shift added to each present deadline; defaults to zero."),
		parameters.Field("min_remaining", parameters.Optional(parameters.CanonicalDuration()), func(query *deadlinePreviewQuery, value *duration.Duration) { query.minimum = value }, "Optional minimum adjusted deadline minus reference time. Omission includes unset deadlines."),
	)
	if err != nil {
		return apiQueries{}, err
	}
	result.costPreview, err = parameters.New(256,
		parameters.Field("p", parameters.Default(parameters.CanonicalInt64(1, costPreviewMaximumPage), 1), func(query *costPreviewQuery, value int64) { query.page = int(value) }, "Page number; canonical positive decimal integer."),
		parameters.Field("factor", parameters.Default(parameters.CanonicalDecimal(6, 4), decimal.Decimal{Coefficient: "1"}), func(query *costPreviewQuery, value decimal.Decimal) { query.factor = value }, "Exact multiplier, up to 2 whole digits and 4 decimal places; defaults to 1."),
		parameters.Field("delta", parameters.Default(parameters.CanonicalDecimal(14, 2), decimal.Decimal{}), func(query *costPreviewQuery, value decimal.Decimal) { query.delta = value }, "Exact adjustment, up to 12 whole digits and 2 decimal places; defaults to 0."),
		parameters.Field("min_cost", parameters.Optional(parameters.CanonicalDecimal(21, 6)), func(query *costPreviewQuery, value *decimal.Decimal) { query.minimum = value }, "Optional minimum adjusted cost, up to 15 whole digits and 6 decimal places. Omission includes unset costs."),
	)
	if err != nil {
		return apiQueries{}, err
	}
	result.summary, err = parameters.New(128,
		parameters.Field("p", parameters.Default(parameters.CanonicalInt64(1, ticketSummaryMaximumPage), 1),
			func(query *ticketSummaryQuery, value int64) { query.page = int(value) },
			"Page number; canonical positive decimal integer."),
		parameters.Field("min_open", parameters.Default(parameters.CanonicalInt64(0, math.MaxInt64), 0),
			func(query *ticketSummaryQuery, value int64) { query.minimumOpen = value },
			"Minimum open tickets in each returned group; canonical nonnegative int64 decimal integer."),
		parameters.Field("min_open_basis_points", parameters.Default(parameters.CanonicalInt64(0, 10000), 0),
			func(query *ticketSummaryQuery, value int64) { query.minimumOpenBasisPoints = value },
			"Minimum open share in basis points, from 0 to 10000; 100 basis points equals one percent. Canonical decimal integer."),
	)
	if err != nil {
		return apiQueries{}, err
	}
	result.reportStatus, err = parameters.New(128,
		parameters.Field("p", parameters.Default(parameters.CanonicalInt64(1, serviceReportStatusMaximumPage), 1),
			func(query *serviceReportStatusQuery, value int64) { query.page = int(value) }, "Page number; canonical positive decimal integer."),
		parameters.Field("comparison", parameters.Default(parameters.StringChoices("all", "same", "different"), "all"),
			func(query *serviceReportStatusQuery, value string) { query.comparison = value }, "Whether report completion and ticket closure have the same value; the statuses are independent."),
	)
	if err != nil {
		return apiQueries{}, err
	}
	result.labelUsage, err = parameters.New(128,
		parameters.Field("p", parameters.Default(parameters.CanonicalInt64(1, labelUsageMaximumPage), 1), func(query *labelUsageQuery, value int64) { query.page = int(value) }, "Page number; canonical positive decimal integer."),
		parameters.Field("usage", parameters.Default(parameters.StringChoices("all", "used", "unused"), "all"), func(query *labelUsageQuery, value string) { query.usage = value }, "Include all labels, labels with at least one in-category ticket, or labels without in-category tickets."),
		parameters.Field("factor", parameters.Default(parameters.CanonicalDecimal(6, 4), decimal.Decimal{Coefficient: "1"}), func(query *labelUsageQuery, value decimal.Decimal) { query.factor = value }, "Exact multiplier applied to each present ticket cost before summing; up to 2 whole digits and 4 decimal places. Defaults to 1."),
		parameters.Field("delta", parameters.Default(parameters.CanonicalDecimal(14, 2), decimal.Decimal{}), func(query *labelUsageQuery, value decimal.Decimal) { query.delta = value }, "Exact adjustment applied to each present ticket cost before summing; up to 12 whole digits and 2 decimal places. Defaults to 0."),
		parameters.Field("elapsed_factor", parameters.Default(parameters.CanonicalDecimal(6, 4), decimal.Decimal{Coefficient: "1"}), func(query *labelUsageQuery, value decimal.Decimal) { query.elapsedFactor = value }, "Exact elapsed-time multiplier, up to 2 whole digits and 4 decimal places; defaults to 1. Each present elapsed time is multiplied then divided before summing; only the final total is rounded to the nearest microsecond, with ties to even."),
		parameters.Field("elapsed_divisor", parameters.Default(parameters.CanonicalPositiveDecimal(6, 4), decimal.Decimal{Coefficient: "1"}), func(query *labelUsageQuery, value decimal.Decimal) { query.elapsedDivisor = value }, "Exact positive elapsed-time divisor, up to 2 whole digits and 4 decimal places; defaults to 1. Zero and negative values are rejected."),
	)
	if err != nil {
		return apiQueries{}, err
	}
	page := []parameters.Parameter[admin.ListRequest]{
		parameters.Field("limit", parameters.Default(parameters.DigitsInt64(1, 100), 20),
			func(query *admin.ListRequest, value int64) { query.Limit = int(value) }, "Maximum page size; unsigned decimal digits, including leading zeroes."),
		parameters.Field("offset", parameters.Default(parameters.DigitsInt64(0, math.MaxInt32), 0),
			func(query *admin.ListRequest, value int64) { query.Offset = int(value) }, "Rows to skip; unsigned decimal digits, including leading zeroes."),
	}
	result.links, err = parameters.New(2048, page...)
	if err != nil {
		return apiQueries{}, err
	}
	result.labels, err = parameters.New(2048, append(page,
		parameters.Field("search", parameters.Default(parameters.String(64, true), ""),
			func(query *admin.ListRequest, value string) { query.Search = value }, "Literal name substring; empty means no filter."))...)
	if err != nil {
		return apiQueries{}, err
	}
	return result, nil
}
