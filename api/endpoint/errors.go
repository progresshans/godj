package endpoint

import (
	"fmt"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// Reject constructs an expected client error using the framework's closed
// JSON error envelope. The endpoint must declare this 4xx status. Return this
// error directly after deciding that the application outcome is an expected
// rejection; wrapped/internal errors remain internal and are not unwrapped into
// client success or rejection. No private cause is serialized or retained here.
func Reject(status int, code api.ResponseCode, diagnostics validation.Errors) error {
	if status < 400 || status >= 500 {
		return responseError("rejection status must be a client error", nil)
	}
	response, err := api.ErrorResponse(status, code, diagnostics)
	if err != nil {
		return err
	}
	return &rejection{response: response}
}

type rejection struct{ response web.Response }

func (failure *rejection) Error() string   { return "api endpoint: expected client rejection" }
func (failure rejection) GoString() string { return "api endpoint: expected client rejection" }

func configError(field, detail string, cause error) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: "endpoint." + field, Detail: detail, Cause: cause}
}

func responseError(detail string, cause error) error {
	return &api.Error{Code: api.FailureInvalidResponse, Field: "endpoint", Detail: detail, Cause: cause}
}

func invalidIndex(index int, detail string) error {
	return configError(fmt.Sprintf("responses[%d]", index), detail, nil)
}
