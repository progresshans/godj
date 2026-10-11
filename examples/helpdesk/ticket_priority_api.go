package helpdesk

import (
	"math"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) raisePriorityOperation(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error), appendAudit appendTicketAudit, output, failure openapi.Schema) (openapi.Operation, openapi.NamedSchema, error) {
	key, err := openapi.IntegerRange(1, math.MaxInt64)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	keys, err := openapi.ArrayRange(key, 1, ticketBulkMaximum)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	input, err := openapi.Object(openapi.Property{Name: "ids", Schema: keys, Required: true})
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	ref, err := openapi.Ref("TicketPriorityRaise")
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	response, err := openapi.ArrayRange(output, 1, ticketBulkMaximum)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes})
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	operation, err := protect(openapi.Operation{
		Route:   web.Route{Name: "helpdesk:ticket-raise-priority", Method: http.MethodPost, Path: "/api/tickets/raise-priority/"},
		Summary: "Raise selected ticket priorities", Permission: ChangeTicket, AdditionalPermissions: []auth.Permission{ViewLabel},
		Description: "Raises Low (-1) to Normal (0) and Normal to Urgent (1) using the current stored value; SQL null becomes Normal. Urgent and legacy values outside these choices are preserved. Authentication, required CSRF and change/label-view permissions precede parsing. All unique positive IDs must exist in the current category. Current selection, category, priority, stored label integrity, output and actual-change audits are checked in one transaction; failure rolls back the complete command. Numeric rows use one native expression UPDATE and null rows use a separate constant UPDATE; the original priority and category stay in each predicate and every intended row must match. Response order follows the input. Unchanged rows produce no write, digest repair or audit. Changed rows synchronize their stored JSON digest. There is no automatic retry or partial success; uncertain outcomes remain errors. Query parameters, duplicate IDs, unknown fields and client-supplied priority values are rejected.",
		RequestBody: &openapi.RequestBody{Required: true, Schema: ref, Description: "A closed object with ids: 1 to 40 unique exact positive int64 JSON integers. The complete body is limited to 4096 bytes; duplicate members, numeric fractions/exponents, nulls, strings and trailing data are rejected. Duplicate/invalid IDs carry a zero-based index parameter."},
		Responses: []openapi.Response{
			helpdeskJSONResponse(http.StatusOK, "All selected tickets in input order, including unchanged priorities; one shared 1 MiB, depth 16 and 65536-value budget.", response),
			helpdeskJSONResponse(http.StatusBadRequest, "Invalid selection; no ticket is changed.", failure),
			helpdeskJSONResponse(http.StatusNotFound, "A selected ticket or its expected state is absent from the current category.", failure),
			helpdeskJSONResponse(http.StatusRequestEntityTooLarge, "The request exceeds 4096 bytes.", failure),
			helpdeskJSONResponse(http.StatusUnsupportedMediaType, "Exactly one supported application/json Content-Type is required.", failure),
		},
	}, a.apiRaisePriority(parser, appendAudit))
	return operation, openapi.NamedSchema{Name: "TicketPriorityRaise", Schema: input}, err
}

func (a *Application) apiRaisePriority(parser api.Parser, appendAudit appendTicketAudit) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if request.HTTP().URL.RawQuery != "" {
			return bulkValidationResponse(validation.NewErrors(validation.New(validation.NonField, "invalid")))
		}
		object, err := parser.ParseObject(request)
		if err != nil {
			if response, handled, responseErr := api.RequestErrorResponse(err); handled {
				return response, responseErr
			}
			return web.Response{}, err
		}
		var failures validation.Errors
		for _, member := range object.Members() {
			if member.Name() != "ids" {
				failures = validation.Join(failures, validation.NewErrors(validation.New(validation.Field(member.Name()), serializers.CodeUnknown)))
			}
		}
		value, present := object.Get("ids")
		if !present {
			failures = validation.Join(failures, validation.NewErrors(validation.New("ids", serializers.CodeRequired)))
		}
		values, valid := value.AsList()
		if present && !valid {
			failures = validation.Join(failures, validation.NewErrors(validation.New("ids", "invalid")))
		}
		if !failures.Empty() {
			return bulkValidationResponse(failures)
		}
		ids := make([]int64, len(values))
		for index, value := range values {
			id, exact := value.AsInteger()
			if !exact {
				return bulkValidationResponse(bulkRowFailures(index, validation.NewErrors(validation.New("ids", "invalid_choice"))))
			}
			ids[index] = id
		}
		updated, err := a.raiseTicketPriority(request.Context(), actor, ids, appendAudit)
		if rejected, ok := validation.Rejected(err); ok {
			return bulkValidationResponse(rejected)
		}
		if err != nil {
			return objectFailure(err)
		}
		return updated.response, nil
	}
}
