package helpdesk

import (
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/validation"
)

func typedEndpointFailure(err error) error {
	if err == admin.ErrObjectNotFound {
		return endpoint.Reject(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	}
	if diagnostics, rejected := validation.Rejected(err); rejected {
		return endpoint.Reject(http.StatusBadRequest, api.CodeValidationError, diagnostics)
	}
	return err
}
