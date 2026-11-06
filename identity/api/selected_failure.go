package identityapi

import (
	"net/http"
	"strings"

	"github.com/progresshans/godj/api"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func selectedFailure(err error) (web.Response, error) {
	if diagnostics, rejected := validation.Rejected(err); rejected {
		part := "fields"
		if _, password := accountmodel.PasswordInputDiagnostics(err); password {
			part = "credential"
		}
		items := diagnostics.All()
		for index, item := range items {
			field := strings.ReplaceAll(strings.ReplaceAll(string(item.Field()), "~", "~0"), "/", "~1")
			items[index] = validation.New(validation.Field("/"+part+"/"+field), item.Code(), item.Params()...)
		}
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(items...))
	}
	// Only the owner's outer result is authoritative. Wrapped/joined expected
	// errors and a storage cause shaped like a rejection remain execution errors.
	failure, ok := err.(*accountmodel.Error)
	if !ok || failure == nil {
		return web.Response{}, err
	}
	switch failure.Code {
	case accountmodel.CodePermission:
		return api.ErrorResponse(http.StatusForbidden, api.CodePermissionDenied, validation.Errors{})
	case accountmodel.CodeNotFound:
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.Errors{})
	case accountmodel.CodeConflict:
		return api.ErrorResponse(http.StatusPreconditionFailed, "revision_conflict", validation.Errors{})
	case accountmodel.CodeInvalidInput:
		if failure.Unwrap() == nil {
			return invalidRequest()
		}
	case accountmodel.CodeOutcomeUnknown:
		return web.NewResponse(http.StatusServiceUnavailable, http.Header{"Content-Type": {api.JSONContentType}}, []byte(`{"code":"outcome_unknown","errors":[]}`))
	}
	return web.Response{}, err
}
