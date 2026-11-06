package helpdesk

import (
	"context"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func prepareTicketAPIInput(resource crud.Resource[models.Ticket], spec serializers.Spec) (input.Body[orm.ModelInput[models.Ticket]], error) {
	return input.NewResource(resource, spec, api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes})
}

func validateTicketAPIInput(ctx context.Context, value orm.ModelInput[models.Ticket]) (validation.Errors, error) {
	if err := ctx.Err(); err != nil {
		return validation.Errors{}, err
	}
	if payload, supplied := value.Get("external_payload"); supplied && !payload.IsNull() {
		document, _ := payload.JSON()
		return externalPayloadErrors(document), nil
	}
	return validation.Errors{}, nil
}

type reportAPIInput struct {
	ticketID  input.Presence[int64]
	summary   input.Presence[string]
	completed input.Presence[bool]
}

func prepareLabelAPIInput(resource crud.Resource[models.Label], spec serializers.Spec) (input.Body[orm.ModelInput[models.Label]], error) {
	return input.NewResource(resource, spec, api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes})
}

func prepareReportAPIInput(spec serializers.Spec, withTicket bool) (input.Body[reportAPIInput], error) {
	properties := []input.Property[reportAPIInput]{
		input.Field("summary", input.String(), func(result *reportAPIInput, value input.Presence[string]) { result.summary = value }),
		input.Field("completed", input.Boolean(), func(result *reportAPIInput, value input.Presence[bool]) { result.completed = value }),
	}
	if withTicket {
		properties = append(properties, input.Field("ticket", input.Integer(), func(result *reportAPIInput, value input.Presence[int64]) { result.ticketID = value }))
	}
	return input.New(spec, api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes}, properties...)
}

func bindTypedInput[T any](request *web.Request, body input.Body[T], mode serializers.Mode) (T, web.Response, bool, error) {
	value, diagnostics, err := body.Parse(request, mode)
	if err != nil {
		response, handled, responseErr := api.RequestErrorResponse(err)
		if handled {
			return value, response, true, responseErr
		}
		return value, web.Response{}, false, err
	}
	if !diagnostics.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		return value, response, true, err
	}
	return value, web.Response{}, false, nil
}
