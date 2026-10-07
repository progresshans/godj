package helpdesk

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) bulkTicketEndpoint(authentication api.Authentication, appendAudit appendTicketAudit, item endpoint.Input[ticketInput]) (endpoint.Endpoint, error) {
	list := endpoint.JSONListBody(item, input.ListConfig{
		Parser:     api.ParserConfig{MaxBodyBytes: ticketBulkBodyBytes, JSONLimits: serializers.Limits{MaxValues: maximumJSONListValues}},
		ItemLimits: serializers.Limits{MaxDocumentBytes: maximumJSONBodyBytes},
		MinItems:   1, MaxItems: ticketBulkMaximum,
	}, fmt.Sprintf("A nonempty array of at most %d TicketCreate objects; the whole body is limited to %d bytes, depth 16 and 65536 values. Every item's compact JSON is limited to %d bytes and the ordinary TicketCreate field policy. The single-request limits on duplicate keys, exact numbers, Unicode, NUL, read-only/unknown fields and external JSON still apply. Blank arrays and non-object items are rejected. Omitted fields use the same defaults and SQL nulls as single creation.", ticketBulkMaximum, ticketBulkBodyBytes, maximumJSONBodyBytes), validateTicketAPIInput)
	return endpoint.New(authentication, endpoint.Config[[]ticketInput, []ticketRecord]{
		Route:       web.Route{Name: "helpdesk:ticket-bulk-create", Method: http.MethodPost, Path: "/api/tickets/bulk/"},
		Summary:     "Create several tickets",
		Admission:   endpoint.All(AddTicket, ViewLabel),
		Description: "Creates every ticket, its selected label links and one add audit per ticket in one transaction. Authentication, required CSRF and add/label-view admission precede parsing. Category and labels are rechecked in the write transaction; all candidate uniqueness checks precede the native bulk INSERTs. A rejection or failed audit rolls back the entire request. Results retain input order and contain stored JSON with its server-owned digest. There is no conflict-ignore, partial success or automatic retry. An uncertain commit remains an execution error. Query parameters are rejected. Field diagnostics carry a zero-based index parameter; an integer-array field position uses item_index; a concurrent storage conflict uses __all__/unique without guessing a row. If diagnostics exceed the bounded response budget, __all__/too_many_errors rejects the complete request without listing partial diagnostics.",
		Input:       endpoint.NoQuery(list), Output: a.responses.ticketBulk,
		ErrorLimits:               serializers.Limits{MaxValues: maximumJSONListValues, MaxArrayItems: 1 << 14},
		SummarizeValidationErrors: true,
		Success:                   []endpoint.Status{{Code: http.StatusCreated, Description: "All created tickets in input order; shared 1 MiB, depth 16 and 65536-value response budget."}},
		Errors: []endpoint.Status{
			{Code: http.StatusBadRequest, Description: "Malformed input, invalid count, indexed validation or uniqueness rejection; nothing is created."},
			{Code: http.StatusNotFound, Description: "The selected category no longer exists."},
			{Code: http.StatusRequestEntityTooLarge, Description: "The whole request exceeds its byte limit."},
			{Code: http.StatusUnsupportedMediaType, Description: "Exactly one supported application/json Content-Type is required."},
		},
		Handle: func(request *web.Request, actor auth.Principal, rows []ticketInput) (output.Prepared[[]ticketRecord], error) {
			candidates := make([]ticketBulkCandidate, len(rows))
			for index, value := range rows {
				candidates[index] = ticketBulkCandidate{index: index, value: value.model(a.categoryID), labels: value.labels}
			}
			created, err := a.createTickets(request.Context(), actor, candidates, appendAudit)
			if err != nil {
				return output.Prepared[[]ticketRecord]{}, typedEndpointFailure(err)
			}
			return created.response, nil
		},
	})
}

func bulkValidationResponse(failures validation.Errors) (web.Response, error) {
	response, err := api.ErrorResponseWithLimits(http.StatusBadRequest, api.CodeValidationError, failures, serializers.Limits{MaxValues: maximumJSONListValues, MaxArrayItems: 1 << 14})
	if errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "too_many_errors", validation.NewParam("count", strconv.Itoa(failures.Len())))))
	}
	return response, err
}
