package systemstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type secretValueCause struct{ Secret string }

func (cause secretValueCause) Error() string { return cause.Secret }

func TestErrorRenderingAndJSONNeverExposeCause(t *testing.T) {
	t.Parallel()

	const marker = "database-url-secret-marker"
	cause := secretValueCause{Secret: marker}
	err := &Error{
		Code:   CodePersistence,
		Field:  "session_payload",
		Detail: "framework-owned safe detail",
		Cause:  cause,
	}

	encoded, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatalf("json.Marshal(Error): %v", jsonErr)
	}
	rendered := []string{
		err.Error(),
		fmt.Sprint(err),
		fmt.Sprintf("%+v", err),
		fmt.Sprintf("%#v", err),
		fmt.Sprintf("%#v", *err),
		string(encoded),
	}
	for _, value := range rendered {
		if strings.Contains(value, marker) {
			t.Fatalf("rendered Error exposes Cause: %q", value)
		}
	}
	if strings.Contains(string(encoded), "Cause") {
		t.Fatalf("JSON publishes Cause field: %s", encoded)
	}
	var unwrapped secretValueCause
	if !errors.As(err, &unwrapped) || unwrapped.Secret != marker {
		t.Fatalf("errors.As(Error) = %#v, want retained private Cause", unwrapped)
	}
}
