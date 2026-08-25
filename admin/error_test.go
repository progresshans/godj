package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type configDiagnosticSecretCause struct{ Secret string }

func (cause configDiagnosticSecretCause) Error() string { return cause.Secret }

func TestConfigErrorDiagnosticsAndJSONNeverExposeCause(t *testing.T) {
	t.Parallel()

	const marker = "admin-config-cause-secret-marker"
	err := &ConfigError{
		Path:  "site.random",
		Code:  "entropy_failure",
		Cause: configDiagnosticSecretCause{Secret: marker},
	}
	encoded, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatalf("json.Marshal(ConfigError): %v", jsonErr)
	}
	for _, rendered := range []string{fmt.Sprintf("%#v", err), fmt.Sprintf("%#v", *err), string(encoded)} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("ConfigError diagnostic exposes Cause: %q", rendered)
		}
	}
	if strings.Contains(string(encoded), "Cause") {
		t.Fatalf("ConfigError JSON publishes Cause field: %s", encoded)
	}
	var unwrapped configDiagnosticSecretCause
	if !errors.As(err, &unwrapped) || unwrapped.Secret != marker {
		t.Fatalf("errors.As(ConfigError) = %#v, want retained private Cause", unwrapped)
	}
}
