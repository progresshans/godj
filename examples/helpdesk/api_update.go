package helpdesk

import (
	"errors"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) apiUpdate(request *web.Request, principal auth.Principal) (web.Response, error) {
	return a.apiUpdateMode(request, principal, serializers.ModeFull)
}

func (a *Application) apiPatch(request *web.Request, principal auth.Principal) (web.Response, error) {
	return a.apiUpdateMode(request, principal, serializers.ModePartial)
}

func (a *Application) apiUpdateMode(request *web.Request, principal auth.Principal, mode serializers.Mode) (web.Response, error) {
	id, valid := request.Int64Parameter("id")
	if !valid || id <= 0 {
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	}
	values, response, handled, err := a.bindInput(request, mode)
	if handled || err != nil {
		return response, err
	}
	patch := models.TicketPatch{}
	var labels []int64
	for _, entry := range values.All() {
		value := entry.Value()
		switch entry.Name() {
		case "labels":
			labels, _ = value.AsIntegers()
		case "subject":
			text, _ := value.AsString()
			patch = patch.WithSubject(text)
		case "details":
			if value.IsNull() {
				patch = patch.WithDetailsNull()
			} else {
				text, _ := value.AsString()
				patch = patch.WithDetails(text)
			}
		case "closed":
			boolean, _ := value.AsBoolean()
			patch = patch.WithClosed(boolean)
		case "priority":
			if value.IsNull() {
				patch = patch.WithPriorityNull()
			} else {
				integer, _ := value.AsInteger()
				patch = patch.WithPriority(integer)
			}
		case "resolution":
			if value.IsNull() {
				patch = patch.WithResolutionNull()
			} else {
				text, _ := value.AsString()
				patch = patch.WithResolution(text)
			}
		case "due_at":
			if value.IsNull() {
				patch = patch.WithDueAtNull()
			} else {
				instant, _ := value.AsDateTime()
				patch = patch.WithDueAt(instant)
			}
		case "external_reference":
			if value.IsNull() {
				patch = patch.WithExternalReferenceNull()
			} else {
				identifier, _ := value.AsUUID()
				patch = patch.WithExternalReference(identifier)
			}
		case "external_payload":
			if value.IsNull() {
				patch = patch.WithExternalPayloadNull()
			} else {
				document, _ := value.AsJSON()
				patch = patch.WithExternalPayload(document)
			}
		case "expected_cost":
			if value.IsNull() {
				patch = patch.WithExpectedCostNull()
			} else {
				number, _ := value.AsDecimal()
				patch = patch.WithExpectedCost(number)
			}
		case "effort":
			if value.IsNull() {
				patch = patch.WithEffortNull()
			} else {
				floatValue, _ := value.AsFloat()
				patch = patch.WithEffort(floatValue)
			}
		case "elapsed":
			if value.IsNull() {
				patch = patch.WithElapsedNull()
			} else {
				durationValue, _ := value.AsDuration()
				patch = patch.WithElapsed(durationValue)
			}
		case "service_at":
			if value.IsNull() {
				patch = patch.WithServiceAtNull()
			} else {
				clockValue, _ := value.AsTime()
				patch = patch.WithServiceAt(clockValue)
			}
		case "service_on":
			if value.IsNull() {
				patch = patch.WithServiceOnNull()
			} else {
				date, _ := value.AsDate()
				patch = patch.WithServiceOn(date)
			}
		case "reviewed":
			if value.IsNull() {
				patch = patch.WithReviewedNull()
			} else {
				boolean, _ := value.AsBoolean()
				patch = patch.WithReviewed(boolean)
			}
		default:
			return web.Response{}, errors.New("helpdesk: validated update contains an unhandled field")
		}
	}
	updated, _, err := a.updatePatch(request.Context(), principal, id, patch, labels, false)
	if err != nil {
		return objectFailure(err)
	}
	value, err := a.encoder.Encode(updated)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, value)
}

func (a *Application) bindInput(request *web.Request, mode serializers.Mode) (serializers.Values, web.Response, bool, error) {
	object, err := a.parser.ParseObjectFor(request, a.input)
	if err != nil {
		response, handled, responseErr := api.RequestErrorResponse(err)
		if handled {
			return serializers.Values{}, response, true, responseErr
		}
		return serializers.Values{}, web.Response{}, false, err
	}
	bound, err := a.input.Bind(object, mode)
	if err != nil {
		return serializers.Values{}, web.Response{}, false, err
	}
	if !bound.Valid() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, bound.Errors())
		return serializers.Values{}, response, true, err
	}
	if payload, present := bound.Values().Get("external_payload"); present && !payload.IsNull() {
		document, _ := payload.AsJSON()
		if failure := externalPayloadErrors(document); !failure.Empty() {
			response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, failure)
			return serializers.Values{}, response, true, err
		}
	}
	return bound.Values(), web.Response{}, false, nil
}
