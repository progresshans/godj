package helpdesk

import (
	"math"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func (a *Application) ticketSummaryOperation(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error), category, failure openapi.Schema) (openapi.Operation, openapi.NamedSchema, error) {
	priority, err := openapi.Nullable(openapi.Integer())
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	count, err := openapi.IntegerRange(0, math.MaxInt64)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	page, err := openapi.IntegerRange(1, ticketSummaryMaximumPage)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	pageSize, err := openapi.IntegerRange(ticketSummaryPageSize, ticketSummaryPageSize)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	row, err := openapi.Object(
		openapi.Property{Name: "priority", Schema: priority, Required: true},
		openapi.Property{Name: "priority_label", Schema: openapi.String(), Required: true},
		openapi.Property{Name: "total", Schema: count, Required: true},
		openapi.Property{Name: "open", Schema: count, Required: true},
	)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	rows, err := openapi.ArrayRange(row, 0, ticketSummaryPageSize)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	output, err := openapi.Object(
		openapi.Property{Name: "category", Schema: category, Required: true},
		openapi.Property{Name: "page", Schema: page, Required: true},
		openapi.Property{Name: "page_size", Schema: pageSize, Required: true},
		openapi.Property{Name: "min_open", Schema: count, Required: true},
		openapi.Property{Name: "total_groups", Schema: count, Required: true},
		openapi.Property{Name: "results", Schema: rows, Required: true},
	)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	ref, err := openapi.Ref("TicketSummary")
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	operation, err := protect(openapi.Operation{
		Route:   web.Route{Name: "helpdesk:ticket-summary", Method: http.MethodGet, Path: "/api/tickets/summary/"},
		Summary: "Summarize tickets by priority", Permission: ViewTicket,
		Description: "Groups tickets in the application's fixed category by their stored priority, retaining null and every legacy int64 value. Total counts all tickets; open counts those whose closed flag is false. min_open filters groups, then results are ordered by open count descending and priority descending with null last. Pages contain at most 20 groups; total_groups is the count after filtering and before pagination, including on empty or past-end pages. Category identity/name, rows and total share one read snapshot per request. Separate page requests observe current state independently. This is a read-only operation without audit, ticket-body loading or digest repair. ViewTicket is the only business permission, checked before parsing and lookup. Query strings are limited to 128 bytes; unknown/duplicate parameters, empty values, malformed encoding and non-canonical decimal integers are rejected.",
		Parameters: []openapi.Parameter{
			{Name: "p", In: "query", Schema: page, Description: "Page number, default 1; canonical positive decimal integer, at most 50001."},
			{Name: "min_open", In: "query", Schema: count, Description: "Minimum open tickets in each returned group, default 0; canonical nonnegative int64 decimal integer."},
		},
		Responses: []openapi.Response{
			helpdeskJSONResponse(http.StatusOK, "The current category and consistent filtered group page.", ref),
			helpdeskJSONResponse(http.StatusBadRequest, "Invalid summary query parameters.", failure),
			helpdeskJSONResponse(http.StatusNotFound, "The application's current category no longer exists.", failure),
		},
	}, a.apiTicketSummary)
	return operation, openapi.NamedSchema{Name: "TicketSummary", Schema: output}, err
}

func (a *Application) apiTicketSummary(request *web.Request, _ auth.Principal) (web.Response, error) {
	input, diagnostics := parseTicketSummaryQuery(request.HTTP().URL.RawQuery)
	if !diagnostics.Empty() {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
	}
	page, err := a.readTicketSummary(request.Context(), input)
	if err != nil {
		return objectFailure(err)
	}
	rows := make([]serializers.Value, 0, len(page.groups.Rows))
	for _, row := range page.groups.Rows {
		priority := serializers.Null()
		if row.priority != nil {
			priority = serializers.Integer(*row.priority)
		}
		value, err := serializers.NewObject(
			serializers.MemberOf("priority", priority), serializers.MemberOf("priority_label", serializers.String(ticketSummaryPriorityLabel(row.priority))),
			serializers.MemberOf("total", serializers.Integer(row.total)), serializers.MemberOf("open", serializers.Integer(row.open)),
		)
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value.Value())
	}
	results, err := serializers.NewList(rows...)
	if err != nil {
		return web.Response{}, err
	}
	category, err := serializers.NewObject(serializers.MemberOf("id", serializers.Integer(page.category.ID)), serializers.MemberOf("name", serializers.String(page.category.Name)))
	if err != nil {
		return web.Response{}, err
	}
	value, err := serializers.NewObject(
		serializers.MemberOf("category", category.Value()), serializers.MemberOf("page", serializers.Integer(int64(input.page))),
		serializers.MemberOf("page_size", serializers.Integer(ticketSummaryPageSize)), serializers.MemberOf("min_open", serializers.Integer(input.minimumOpen)),
		serializers.MemberOf("total_groups", serializers.Integer(page.groups.Total)), serializers.MemberOf("results", results),
	)
	if err != nil {
		return web.Response{}, err
	}
	response, err := api.JSON(http.StatusOK, value.Value())
	if err != nil {
		return web.Response{}, err
	}
	return response.WithHeaders(ticketEditorHeaders(api.JSONContentType))
}
