package helpdesk

import (
	"math"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api/parameters"
)

type apiQueries struct {
	summary parameters.Query[ticketSummaryQuery]
	labels  parameters.Query[admin.ListRequest]
	links   parameters.Query[admin.ListRequest]
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
