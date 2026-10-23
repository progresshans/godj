package helpdesk

import (
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) bindInputSpec(request *web.Request, spec serializers.Spec, mode serializers.Mode) (serializers.Values, web.Response, bool, error) {
	object, err := a.parser.ParseObjectFor(request, spec)
	if err != nil {
		response, handled, responseErr := api.RequestErrorResponse(err)
		if handled {
			return serializers.Values{}, response, true, responseErr
		}
		return serializers.Values{}, web.Response{}, false, err
	}
	bound, err := spec.Bind(object, mode)
	if err != nil {
		return serializers.Values{}, web.Response{}, false, err
	}
	if !bound.Valid() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, bound.Errors())
		return serializers.Values{}, response, true, err
	}
	return bound.Values(), web.Response{}, false, nil
}

func objectRequestID(request *web.Request) (int64, bool) {
	id, valid := request.Int64Parameter("id")
	return id, valid && id > 0
}

func objectNotFound() (web.Response, error) {
	return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
}

func objectFailure(err error) (web.Response, error) {
	if err == admin.ErrObjectNotFound {
		return objectNotFound()
	}
	if response, handled, responseErr := api.ValidationErrorResponse(err); handled {
		return response, responseErr
	}
	return web.Response{}, err
}
