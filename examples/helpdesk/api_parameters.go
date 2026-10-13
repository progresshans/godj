package helpdesk

import (
	"math"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api/parameters"
)

type apiQueries struct {
	labelUsage   parameters.Query[labelUsageQuery]
	summary      parameters.Query[ticketSummaryQuery]
	reportStatus parameters.Query[serviceReportStatusQuery]
	labels       parameters.Query[admin.ListRequest]
	links        parameters.Query[admin.ListRequest]
}

func prepareAPIQueries() (apiQueries, error) {
	var result apiQueries
	var err error
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
