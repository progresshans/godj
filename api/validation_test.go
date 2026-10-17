package api_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func TestValidationErrorResponseOnlyRendersConfirmedRejection(t *testing.T) {
	cause := errors.New("private stored value")
	rejection := validation.Reject(validation.NewErrors(validation.New("reference", validation.CodeUnique)), cause)
	response, handled, err := api.ValidationErrorResponse(rejection)
	if err != nil || !handled || response.Status() != http.StatusBadRequest || string(requireBufferedBody(t, response)) != `{"code":"validation_error","errors":[{"field":"reference","code":"unique","params":[]}]}` || strings.Contains(string(requireBufferedBody(t, response)), cause.Error()) {
		t.Fatal("unexpected rejection envelope", string(requireBufferedBody(t, response)), err)
	}
	for _, failure := range []error{nil, cause, fmt.Errorf("reconcile: %w", rejection), errors.Join(rejection, cause), &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Cause: rejection}} {
		if _, handled, err := api.ValidationErrorResponse(failure); handled || err != nil {
			t.Fatal("execution failure mapped to input", failure, err)
		}
	}
}
