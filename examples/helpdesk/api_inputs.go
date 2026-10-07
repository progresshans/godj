package helpdesk

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

type labelAPIInput struct {
	name input.Presence[string]
}

type reportAPIInput struct {
	ticketID  input.Presence[int64]
	summary   input.Presence[string]
	completed input.Presence[bool]
}

func prepareLabelAPIInput(spec serializers.Spec) (input.Body[labelAPIInput], error) {
	return input.New(spec, api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes},
		input.Field("name", input.String(), func(result *labelAPIInput, value input.Presence[string]) { result.name = value }),
	)
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
