package helpdesk

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) bulkTicketOperation(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error), appendAudit appendTicketAudit, input, output, failure openapi.Schema) (openapi.Operation, error) {
	request, err := openapi.ArrayRange(input, 1, ticketBulkMaximum)
	if err != nil {
		return openapi.Operation{}, err
	}
	response, err := openapi.ArrayRange(output, 1, ticketBulkMaximum)
	if err != nil {
		return openapi.Operation{}, err
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: ticketBulkBodyBytes, JSONLimits: serializers.Limits{MaxValues: maximumJSONListValues}})
	if err != nil {
		return openapi.Operation{}, err
	}
	return protect(openapi.Operation{
		Route:      web.Route{Name: "helpdesk:ticket-bulk-create", Method: http.MethodPost, Path: "/api/tickets/bulk/"},
		Summary:    "Create several tickets",
		Permission: AddTicket, AdditionalPermissions: []auth.Permission{ViewLabel},
		Description: "Creates every ticket, its selected label links and one add audit per ticket in one transaction. Authentication, required CSRF and add/label-view admission precede parsing. Category and labels are rechecked in the write transaction; all candidate uniqueness checks precede the native bulk INSERTs. A rejection or failed audit rolls back the entire request. Results retain input order and contain stored JSON with its server-owned digest. There is no conflict-ignore, partial success or automatic retry. An uncertain commit remains an execution error. Query parameters are rejected. Field diagnostics carry a zero-based index parameter; a concurrent storage conflict uses __all__/unique without guessing a row. If diagnostics exceed the bounded response budget, __all__/too_many_errors rejects the complete request without listing partial diagnostics.",
		RequestBody: &openapi.RequestBody{Required: true, Schema: request, Description: fmt.Sprintf("A nonempty array of at most %d TicketCreate objects; the whole body is limited to %d bytes, depth 16 and 65536 values. Every item's compact JSON is limited to %d bytes and the ordinary TicketCreate field policy. The single-request limits on duplicate keys, exact numbers, Unicode, NUL, read-only/unknown fields and external JSON still apply. Blank arrays and non-object items are rejected. Omitted fields use the same defaults and SQL nulls as single creation.", ticketBulkMaximum, ticketBulkBodyBytes, maximumJSONBodyBytes)},
		Responses: []openapi.Response{
			helpdeskJSONResponse(http.StatusCreated, "All created tickets in input order; shared 1 MiB, depth 16 and 65536-value response budget.", response),
			helpdeskJSONResponse(http.StatusBadRequest, "Malformed input, invalid count, indexed validation or uniqueness rejection; nothing is created.", failure),
			helpdeskJSONResponse(http.StatusNotFound, "The selected category no longer exists.", failure),
			helpdeskJSONResponse(http.StatusRequestEntityTooLarge, "The whole request exceeds its byte limit.", failure),
			helpdeskJSONResponse(http.StatusUnsupportedMediaType, "Exactly one supported application/json Content-Type is required.", failure),
		},
	}, a.apiBulkTickets(parser, appendAudit))
}

func bulkValidationResponse(failures validation.Errors) (web.Response, error) {
	response, err := api.ErrorResponseWithLimits(http.StatusBadRequest, api.CodeValidationError, failures, serializers.Limits{MaxValues: maximumJSONListValues, MaxArrayItems: 1 << 14})
	if errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "too_many_errors", validation.NewParam("count", strconv.Itoa(failures.Len())))))
	}
	return response, err
}

func (a *Application) apiBulkTickets(parser api.Parser, appendAudit appendTicketAudit) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if request.HTTP().URL.RawQuery != "" {
			return bulkValidationResponse(validation.NewErrors(validation.New(validation.NonField, "invalid")))
		}
		objects, err := parser.ParseListFor(request, a.input)
		if err != nil {
			if response, handled, responseErr := api.RequestErrorResponse(err); handled {
				return response, responseErr
			}
			return web.Response{}, err
		}
		if len(objects) < 1 || len(objects) > ticketBulkMaximum {
			return objectFailure(bulkCountFailure())
		}
		candidates := make([]ticketBulkCandidate, len(objects))
		var failures validation.Errors
		for index, object := range objects {
			if _, err := serializers.Encode(object.Value(), serializers.Limits{MaxDocumentBytes: maximumJSONBodyBytes}); err != nil {
				if !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
					return web.Response{}, err
				}
				failures = validation.Join(failures, bulkRowFailures(index, validation.NewErrors(validation.New(validation.NonField, "limit_exceeded"))))
				continue
			}
			bound, err := a.input.Bind(object, serializers.ModeFull)
			if err != nil {
				return web.Response{}, err
			}
			if !bound.Valid() {
				failures = validation.Join(failures, bulkRowFailures(index, bound.Errors()))
				continue
			}
			input := ticketInputFromValues(bound.Values())
			if input.externalPayload != nil {
				failures = validation.Join(failures, bulkRowFailures(index, externalPayloadErrors(*input.externalPayload)))
			}
			candidates[index] = ticketBulkCandidate{index: index, value: input.model(a.categoryID), labels: input.labels}
		}
		if !failures.Empty() {
			return bulkValidationResponse(failures)
		}
		created, err := a.createTickets(request.Context(), actor, candidates, appendAudit)
		if diagnostics, rejected := validation.Rejected(err); rejected {
			return bulkValidationResponse(diagnostics)
		}
		if err != nil {
			return objectFailure(err)
		}
		return created.response, nil
	}
}
