package helpdesk

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) bulkUpdateTicketOperation(protect func(openapi.Operation, api.AuthenticatedHandler) (openapi.Operation, error), appendAudit appendTicketAudit, partial, output, failure openapi.Schema) (openapi.Operation, openapi.NamedSchema, error) {
	key, err := openapi.IntegerRange(1, math.MaxInt64)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	item, err := openapi.ExtendObject(partial, openapi.Property{Name: "id", Schema: key, Required: true})
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	ref, err := openapi.Ref("TicketBulkPatch")
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	request, err := openapi.ArrayRange(ref, 1, ticketBulkMaximum)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	response, err := openapi.ArrayRange(output, 1, ticketBulkMaximum)
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: ticketBulkBodyBytes, JSONLimits: serializers.Limits{MaxValues: maximumJSONListValues}})
	if err != nil {
		return openapi.Operation{}, openapi.NamedSchema{}, err
	}
	operation, err := protect(openapi.Operation{
		Route:      web.Route{Name: "helpdesk:ticket-bulk-update", Method: http.MethodPatch, Path: "/api/tickets/bulk/"},
		Summary:    "Update several tickets",
		Permission: ChangeTicket, AdditionalPermissions: []auth.Permission{ViewLabel},
		Description: "Updates every selected ticket, its supplied label membership and each actual change audit in one transaction. Authentication, required CSRF and change/label-view admission precede parsing. IDs must be unique and positive; every selected ticket must exist in the current category or the complete request returns 404 without identifying a missing or foreign row. Each row supplies its own partial field selection; omission preserves stored values, explicit null clears nullable fields and an empty labels array clears membership. An id-only row returns the existing ticket without an UPDATE or audit. Complete candidate and uniqueness validation precedes native bulk UPDATE groups. Each group retains the category predicate and must match all of its rows. A failure in any later batch, label write, stored output or audit rolls back the entire request. Results retain input order and use stored JSON and its server-owned digest. No partial success, conflict-ignore or automatic retry is supported; uncertain commits remain execution errors. Query parameters and client-supplied category/digest are rejected. Indexed diagnostics use a zero-based index parameter; concurrent storage conflicts use __all__/unique without guessing a row. The input does not provide a revision precondition: supplied fields replace their values at the transaction's current state.",
		RequestBody: &openapi.RequestBody{Required: true, Schema: request, Description: fmt.Sprintf("A nonempty array of at most %d TicketBulkPatch objects. Each object requires one exact positive int64 id and any TicketPatch fields. Duplicate IDs are rejected. The complete body is bounded to %d bytes, depth 16 and 65536 values; each compact object is bounded to %d bytes. TicketPatch's exact-number, Unicode, duplicate-key, external JSON and field validation rules apply.", ticketBulkMaximum, ticketBulkBodyBytes, maximumJSONBodyBytes)},
		Responses: []openapi.Response{
			helpdeskJSONResponse(http.StatusOK, "All selected tickets in input order; one shared 1 MiB, depth 16 and 65536-value response budget.", response),
			helpdeskJSONResponse(http.StatusBadRequest, "Malformed input, duplicate or invalid IDs, indexed validation or uniqueness rejection; nothing is changed.", failure),
			helpdeskJSONResponse(http.StatusNotFound, "At least one selected ticket is absent from the current category.", failure),
			helpdeskJSONResponse(http.StatusRequestEntityTooLarge, "The whole request exceeds its byte limit.", failure),
			helpdeskJSONResponse(http.StatusUnsupportedMediaType, "Exactly one supported application/json Content-Type is required.", failure),
		},
	}, a.apiBulkUpdateTickets(parser, appendAudit))
	return operation, openapi.NamedSchema{Name: "TicketBulkPatch", Schema: item}, err
}

func (a *Application) apiBulkUpdateTickets(parser api.Parser, appendAudit appendTicketAudit) api.AuthenticatedHandler {
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
		changes := make([]ticketBulkChange, len(objects))
		seen := make(map[int64]bool)
		var failures validation.Errors
		for index, object := range objects {
			if _, err := serializers.Encode(object.Value(), serializers.Limits{MaxDocumentBytes: maximumJSONBodyBytes}); err != nil {
				if !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
					return web.Response{}, err
				}
				failures = validation.Join(failures, bulkRowFailures(index, validation.NewErrors(validation.New(validation.NonField, "limit_exceeded"))))
				continue
			}
			identity, present := object.Get("id")
			if !present {
				failures = validation.Join(failures, bulkRowFailures(index, validation.NewErrors(validation.New("id", serializers.CodeRequired))))
				continue
			}
			id, valid := identity.AsInteger()
			if !valid || id <= 0 || seen[id] {
				failures = validation.Join(failures, bulkRowFailures(index, validation.NewErrors(validation.New("id", "invalid_choice"))))
				continue
			}
			seen[id] = true
			members := make([]serializers.Member, 0, object.Len()-1)
			for _, member := range object.Members() {
				if member.Name() != "id" {
					members = append(members, member)
				}
			}
			patchObject, err := serializers.NewObject(members...)
			if err != nil {
				return web.Response{}, err
			}
			bound, err := a.input.Bind(patchObject, serializers.ModePartial)
			if err != nil {
				return web.Response{}, err
			}
			if !bound.Valid() {
				failures = validation.Join(failures, bulkRowFailures(index, bound.Errors()))
				continue
			}
			if payload, present := bound.Values().Get("external_payload"); present && !payload.IsNull() {
				document, _ := payload.AsJSON()
				failures = validation.Join(failures, bulkRowFailures(index, externalPayloadErrors(document)))
			}
			patch, labels, err := ticketPatchFromValues(bound.Values())
			if err != nil {
				return web.Response{}, err
			}
			changes[index] = ticketBulkChange{index: index, id: id, patch: patch, labels: labels}
		}
		if !failures.Empty() {
			return bulkValidationResponse(failures)
		}
		updated, err := a.updateTickets(request.Context(), actor, changes, appendAudit)
		if rejected, ok := validation.Rejected(err); ok {
			return bulkValidationResponse(rejected)
		}
		if err != nil {
			return objectFailure(err)
		}
		return updated.response, nil
	}
}
