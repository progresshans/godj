package helpdesk

import (
	"errors"
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) apiUpdate(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiUpdateMode(request, serializers.ModeFull)
}

func (a *Application) apiPatch(request *web.Request, _ auth.Principal) (web.Response, error) {
	return a.apiUpdateMode(request, serializers.ModePartial)
}

func (a *Application) apiUpdateMode(request *web.Request, mode serializers.Mode) (web.Response, error) {
	id, valid := request.Int64Parameter("id")
	if !valid || id <= 0 {
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	}
	values, response, handled, err := a.bindInput(request, mode)
	if handled || err != nil {
		return response, err
	}
	patch := models.TicketPatch{}
	for _, entry := range values.All() {
		value := entry.Value()
		switch entry.Name() {
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
	updated, _, err := a.updatePatch(request.Context(), id, patch)
	if errors.Is(err, admin.ErrObjectNotFound) {
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	}
	if err != nil {
		return web.Response{}, err
	}
	value, err := a.encoder.Encode(updated)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, value)
}

func (a *Application) bindInput(request *web.Request, mode serializers.Mode) (serializers.Values, web.Response, bool, error) {
	object, err := a.parser.ParseObject(request)
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
	return bound.Values(), web.Response{}, false, nil
}
