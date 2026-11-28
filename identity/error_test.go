package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIdentityErrorsKeepDiagnosticFallbackOpaque(t *testing.T) {
	const secret = "private-management-cause"
	cause := errors.New(secret)
	err := identityFailure(CodePersistence, "transaction", cause)
	if !errors.Is(err, cause) {
		t.Fatal("cause lost")
	}
	for _, value := range []any{err, *err.(*Error)} {
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil || strings.Contains(string(data), secret) {
			t.Fatal("JSON leaked private cause")
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%d", "%f", "%p", "%w"} {
			if strings.Contains(fmt.Sprintf(format, value), secret) {
				t.Fatal("format leaked cause", format)
			}
		}
	}
}
