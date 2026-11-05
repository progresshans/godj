package identitytest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
)

// Captured by the independent, pinned Django runner. Role observations are
// retained in these fixtures but are not a Directory admission assertion.
func assertReferencePermissions(t *testing.T, observation, field string, permissions []auth.Permission) {
	t.Helper()
	normalized := make([]string, len(permissions))
	for index, permission := range permissions {
		if !strings.HasPrefix(string(permission), "helpdesk.ticket.") {
			t.Fatal("unexpected reference permission namespace")
		}
		normalized[index] = strings.TrimPrefix(string(permission), "helpdesk.ticket.")
	}
	for _, backend := range []string{"sqlite", "postgres"} {
		payload, err := identityreference.Identity(backend)
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django       string
			Observations map[string]map[string]json.RawMessage
		}
		if err := json.Unmarshal(payload, &reference); err != nil {
			t.Fatal(err)
		}
		var expected []string
		if err := json.Unmarshal(reference.Observations[observation][field], &expected); err != nil || reference.Django != "6.1" {
			t.Fatal("invalid pinned identity reference", err)
		}
		if !reflect.DeepEqual(normalized, expected) {
			t.Fatalf("identity %s.%s differs from Django %s: %v != %v", observation, field, backend, normalized, expected)
		}
	}
}
