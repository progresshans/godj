package bearerauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type secretCause struct{ marker string }

func (cause secretCause) Error() string { return cause.marker }

func TestErrorRetainsCauseWithoutPublishingIt(t *testing.T) {
	t.Parallel()

	const marker = "gdj-bearer-error-cause-canary"
	cause := secretCause{marker: marker}
	err := &Error{
		Code:   CodeVerification,
		Field:  "verifier",
		Detail: "framework-owned safe detail",
		Cause:  cause,
	}
	encoded, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatal("Bearer Error JSON serialization failed")
	}
	for _, rendered := range []string{
		err.Error(),
		fmt.Sprintf("%v", err),
		fmt.Sprintf("%+v", err),
		fmt.Sprintf("%#v", err),
		fmt.Sprintf("%#v", *err),
		fmt.Sprintf("%d", err),
		fmt.Sprintf("%x", *err),
		string(encoded),
	} {
		if strings.Contains(rendered, marker) {
			t.Fatal("Error diagnostic exposes private Cause")
		}
	}
	if strings.Contains(string(encoded), "Cause") {
		t.Fatal("Error JSON publishes Cause field")
	}
	var unwrapped secretCause
	if !errors.As(err, &unwrapped) || unwrapped.marker != marker {
		t.Fatal("errors.As did not retain the private cause")
	}
	if !errors.Is(err, &Error{Code: CodeVerification}) {
		t.Fatal("errors.Is did not preserve the verification code")
	}
}
