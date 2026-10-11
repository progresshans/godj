package api

import (
	"net/http"

	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// ValidationErrorResponse maps a confirmed input rejection to the standard
// validation envelope. Other errors, including wrapped/joined rejections, are
// not handled; callers retain their execution and transaction failure paths.
func ValidationErrorResponse(err error) (web.Response, bool, error) {
	diagnostics, rejected := validation.Rejected(err)
	if !rejected {
		return web.Response{}, false, nil
	}
	response, responseErr := ErrorResponse(http.StatusBadRequest, CodeValidationError, diagnostics)
	return response, true, responseErr
}
