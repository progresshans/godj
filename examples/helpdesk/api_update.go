package helpdesk

import (
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"net/http"
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
	input, response, handled, err := a.bindInput(request, mode)
	if handled || err != nil {
		return response, err
	}
	updated, err := a.updateTicket(request.Context(), principal, id, input)
	if err != nil {
		return objectFailure(err)
	}
	return a.responses.ticket.Response(updated.response)
}

func (a *Application) bindInput(request *web.Request, mode serializers.Mode) (orm.ModelInput[models.Ticket], web.Response, bool, error) {
	value, response, handled, err := bindTypedInput(request, a.ticketInput, mode)
	if handled || err != nil {
		return value, response, handled, err
	}
	failures, err := validateTicketAPIInput(request.Context(), value)
	if err != nil {
		return orm.ModelInput[models.Ticket]{}, web.Response{}, false, err
	}
	if !failures.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, failures)
		return orm.ModelInput[models.Ticket]{}, response, true, err
	}
	return value, web.Response{}, false, nil
}
