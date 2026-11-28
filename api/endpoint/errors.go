package endpoint

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/serializers"
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
	// Validate the public status/code now. Diagnostics are immutable and are
	// rendered by the endpoint using its complete error-representation budget.
	_, err := api.ErrorResponse(status, code, validation.Errors{})
	if err != nil {
		return err
	}
	return &rejection{status: status, code: code, diagnostics: diagnostics}
}

type rejection struct {
	status      int
	code        api.ResponseCode
	diagnostics validation.Errors
}

type errorPolicy struct {
	limits    serializers.Limits
	summarize bool
}

func (policy errorPolicy) response(status int, code api.ResponseCode, diagnostics validation.Errors) (web.Response, error) {
	response, err := api.ErrorResponseWithLimits(status, code, diagnostics, policy.limits)
	if policy.summarize && code == api.CodeValidationError && errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		// This is a complete rejection with the original diagnostic count, not a
		// truncated prefix. The small summary uses the standard envelope budget.
		return api.ErrorResponse(status, code, validation.NewErrors(validation.New(validation.NonField, "too_many_errors", validation.NewParam("count", strconv.Itoa(diagnostics.Len())))))
	}
	return response, err
}

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
