package auth_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestCredentialUsernameEnvelopeAcceptsAllUTF8WidthsWithoutChangingIdentity(t *testing.T) {
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "unicode-user", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{strings.Repeat("한", 150), strings.Repeat("\U000105c0", 256), strings.Repeat("x", auth.MaximumUsernameBytes)} {
		if err := auth.ValidateUsername(value); err != nil {
			t.Fatal("valid credential envelope rejected", len(value), err)
		}
		if credential, err := auth.NewCredential(value, "bounded-test-hash", principal); err != nil || credential.Principal().ID() != principal.ID() {
			t.Fatal("credential changed identity", err)
		}
	}
	for _, value := range []string{strings.Repeat("\U000105c0", 257), strings.Repeat("x", auth.MaximumUsernameBytes+1), "\xff", "x\x00y", " padded"} {
		if err := auth.ValidateUsername(value); err == nil {
			t.Fatal("invalid envelope accepted", len(value))
		}
	}
}
