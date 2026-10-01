package auth_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestCredentialAndAuthenticatorsHideSecretsAcrossDiagnosticVerbs(t *testing.T) {
	const marker = "credential-diagnostic-private-marker"
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "diagnostic-user", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential("diagnostic-user", marker, principal)
	if err != nil {
		t.Fatal(err)
	}
	hasher := diagnosticHasher{Secret: marker}
	memory, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := auth.NewStoredAuthenticator(t.Context(), diagnosticStore{Credential: credential, Secret: marker}, hasher)
	if err != nil {
		t.Fatal(err)
	}
	values := []any{credential, &credential, memory, *memory, stored, *stored}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%f", "%o", "%p", "%w", "%020d", "%#[1]v"} {
			rendered := fmt.Sprintf(format, value)
			if strings.Contains(rendered, marker) || strings.Contains(rendered, hex.EncodeToString([]byte(marker))) {
				t.Fatal("diagnostic verb exposed credential/store/hasher material", format)
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), marker) {
			t.Fatal("credential state escaped JSON boundary", err)
		}
	}
}

type diagnosticHasher struct{ Secret string }

func (h diagnosticHasher) Hash(context.Context, string) (string, error)         { return h.Secret, nil }
func (h diagnosticHasher) Verify(context.Context, string, string) (bool, error) { return true, nil }
func (h diagnosticHasher) ValidateEncoded(string) error                         { return nil }

type diagnosticStore struct {
	Credential auth.Credential
	Secret     string
}

func (s diagnosticStore) CredentialByUsername(context.Context, string) (auth.Credential, bool, error) {
	return s.Credential, true, nil
}
func (s diagnosticStore) CredentialByID(context.Context, string) (auth.Credential, bool, error) {
	return s.Credential, true, nil
}
