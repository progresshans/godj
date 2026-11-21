package systemstate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
)

func TestHistoricalIdentityDiagnosticsKeepFallbackOpaque(t *testing.T) {
	const marker = "identity-diagnostic-private-marker"
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "diagnostic-user", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential("diagnostic-user", marker, principal)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := newIdentityDirectory(identityDiagnosticBackend{Secret: marker})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{credential, &credential, directory, *directory} {
		for _, format := range []string{"%v", "%+v", "%#v", "%d", "%f", "%p", "%w"} {
			if strings.Contains(fmt.Sprintf(format, value), marker) {
				t.Fatal("identity diagnostic exposed private state", format)
			}
		}
	}
}

type identityDiagnosticBackend struct{ Secret string }

func (identityDiagnosticBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	return nil
}
