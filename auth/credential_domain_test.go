package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestCredentialDomainSeparatesOtherwiseIdenticalAccounts(t *testing.T) {
	t.Parallel()
	first, err := auth.NewCredentialDomain(strings.Repeat("12", 32))
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.NewCredentialDomain(strings.Repeat("34", 32))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "same-principal", Active: true, Permissions: []auth.Permission{"ticket.view"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := auth.NewCredentialForDomain(first, "same-login", "same-salted-hash", principal)
	if err != nil {
		t.Fatal(err)
	}
	after, err := auth.NewCredentialForDomain(second, "same-login", "same-salted-hash", principal)
	if err != nil {
		t.Fatal(err)
	}
	unscoped, err := auth.NewCredential("same-login", "same-salted-hash", principal)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]auth.Credential{{before, after}, {before, unscoped}, {after, unscoped}} {
		if pair[0].MatchesSessionStamp(pair[1].SessionStamp()) || pair[1].MatchesSessionStamp(pair[0].SessionStamp()) {
			t.Fatal("different credential domains inherited authentication state")
		}
	}
	if !first.MatchesPrincipal(before.Principal()) || first.MatchesPrincipal(after.Principal()) || first.MatchesPrincipal(principal) || (auth.CredentialDomain{}).MatchesPrincipal(principal) {
		t.Fatal("actor domain matching accepted an unbound or foreign observation")
	}
	if observed, present := before.Principal().CredentialDomain(); !present || observed.Identifier() != first.Identifier() {
		t.Fatal("credential principal lost its ownership domain")
	}
	if credential, err := auth.NewCredential("same-login", "same-salted-hash", before.Principal()); err == nil || credential.SessionStamp() != "" {
		t.Fatal("unscoped construction stripped the actor domain")
	}
	if credential, err := auth.NewCredentialForDomain(second, "same-login", "same-salted-hash", before.Principal()); err == nil || credential.SessionStamp() != "" {
		t.Fatal("foreign construction replaced the actor domain")
	}
	if credential, err := auth.NewCredentialForDomain(first, "same-login", "replacement-salted-hash", before.Principal()); err != nil || credential.MatchesSessionStamp(before.SessionStamp()) {
		t.Fatal("same-domain credential maintenance failed", err)
	}
	reopened, err := auth.NewCredentialDomain(first.Identifier())
	if err != nil {
		t.Fatal(err)
	}
	currentPrincipal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principal.ID(), Active: true, Staff: true, Permissions: []auth.Permission{"ticket.change"}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := auth.NewCredentialForDomain(reopened, "renamed-login", "same-salted-hash", currentPrincipal)
	if err != nil || !current.MatchesSessionStamp(before.SessionStamp()) || before.Principal().Staff() {
		t.Fatal("reopening a domain or changing current authorization changed its identity", err)
	}
}

func TestCredentialDomainRejectsAbsentAndNoncanonicalIdentifiers(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", strings.Repeat("0", 64), strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63) + "\x00"} {
		if domain, err := auth.NewCredentialDomain(input); err == nil || domain.Identifier() != "" {
			t.Fatal("invalid domain was accepted")
		}
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "member", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredentialForDomain(auth.CredentialDomain{}, "member", "private-hash", principal)
	if !errors.Is(err, &auth.Error{Code: auth.CodeInvalidInput, Field: "credential_domain"}) || credential.SessionStamp() != "" || strings.Contains(err.Error(), "private-hash") {
		t.Fatal("zero domain supplied a credential or leaked input", err)
	}
}

func TestStoredAuthenticationRejectsOwnershipChangeDuringPasswordWork(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		name := "authenticate"
		if confirm {
			name = "confirm"
		}
		t.Run(name, func(t *testing.T) {
			first, err := auth.NewCredentialDomain(strings.Repeat("12", 32))
			if err != nil {
				t.Fatal(err)
			}
			second, err := auth.NewCredentialDomain(strings.Repeat("34", 32))
			if err != nil {
				t.Fatal(err)
			}
			principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "member", Active: true})
			if err != nil {
				t.Fatal(err)
			}
			before, err := auth.NewCredentialForDomain(first, "member", "hash:password", principal)
			if err != nil {
				t.Fatal(err)
			}
			after, err := auth.NewCredentialForDomain(second, "member", "hash:password", principal)
			if err != nil {
				t.Fatal(err)
			}
			current, reads := before, 0
			store := &observedCredentialStore{
				byName: func(context.Context, string) (auth.Credential, bool, error) { reads++; return current, true, nil },
				byID:   func(context.Context, string) (auth.Credential, bool, error) { reads++; return current, true, nil },
			}
			hasher := &storedProbeHasher{}
			authenticator, err := auth.NewStoredAuthenticator(t.Context(), store, hasher)
			if err != nil {
				t.Fatal(err)
			}
			hasher.onVerify = func() { current = after }
			var got auth.Credential
			if confirm {
				got, err = authenticator.ConfirmPassword(t.Context(), "member", "password")
			} else {
				got, err = authenticator.Authenticate(t.Context(), "member", "password")
			}
			if !errors.Is(err, auth.ErrInvalidCredentials) || got.SessionStamp() != "" || reads != 2 || hasher.verifyCalls != 1 {
				t.Fatal("ownership change reused an old password observation", err, reads, hasher.verifyCalls)
			}
			hasher.onVerify = nil
			got, err = authenticator.Authenticate(t.Context(), "member", "password")
			if err != nil || !got.MatchesSessionStamp(after.SessionStamp()) || got.MatchesSessionStamp(before.SessionStamp()) {
				t.Fatal("fresh authentication did not use the new domain", err)
			}
		})
	}
}
