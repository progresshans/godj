package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type diagnosticSecretCause struct{ Secret string }

func (cause diagnosticSecretCause) Error() string { return cause.Secret }

func TestErrorDiagnosticsAndJSONNeverExposeCause(t *testing.T) {
	t.Parallel()

	const marker = "api-cause-secret-marker"
	err := &Error{
		Code:   FailureInvalidResponse,
		Field:  "response",
		Detail: "framework-owned safe detail",
		Cause:  diagnosticSecretCause{Secret: marker},
	}
	encoded, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatalf("json.Marshal(Error): %v", jsonErr)
	}
	for _, rendered := range []string{fmt.Sprintf("%#v", err), fmt.Sprintf("%#v", *err), string(encoded)} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("Error diagnostic exposes Cause: %q", rendered)
		}
	}
	if strings.Contains(string(encoded), "Cause") {
		t.Fatalf("Error JSON publishes Cause field: %s", encoded)
	}
	var unwrapped diagnosticSecretCause
	if !errors.As(err, &unwrapped) || unwrapped.Secret != marker {
		t.Fatalf("errors.As(Error) = %#v, want retained private Cause", unwrapped)
	}
}
