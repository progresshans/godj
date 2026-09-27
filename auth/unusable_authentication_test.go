package auth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

type unusableProbeHasher struct {
	storedProbeHasher
	encodings   []string
	verifyError error
}

func (h *unusableProbeHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	h.encodings = append(h.encodings, encoded)
	if h.verifyError != nil {
		return false, h.verifyError
	}
	return h.storedProbeHasher.Verify(ctx, password, encoded)
}

func TestUnusableCredentialsDenyPasswordsWithDummyWorkButRetainCurrentIdentity(t *testing.T) {
	for _, source := range []string{"memory", "stored"} {
		for _, marker := range []string{"!", "!legacy", "!" + strings.Repeat("a", 2047)} {
			t.Run(source+fmt.Sprint(len(marker)), func(t *testing.T) {
				credential := storedTestCredential(t, "member", "member", marker, true, true, false, "helpdesk.view_ticket")
				if credential.HasUsablePassword() || credential.SessionStamp() == "" {
					t.Fatal("unusable snapshot state lost")
				}
				hasher := &unusableProbeHasher{}
				reads := 0
				var authenticator auth.CredentialAuthenticator
				var err error
				if source == "memory" {
					authenticator, err = auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
				} else {
					authenticator, err = auth.NewStoredAuthenticator(t.Context(), &observedCredentialStore{
						byName: func(context.Context, string) (auth.Credential, bool, error) { return credential, true, nil },
						byID:   func(context.Context, string) (auth.Credential, bool, error) { reads++; return credential, true, nil },
					}, hasher)
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, password := range []string{"", "wrong", marker, "dummy"} {
					value, err := authenticator.Authenticate(t.Context(), "member", password)
					if !errors.Is(err, auth.ErrInvalidCredentials) || value.Principal().ID() != "" {
						t.Fatal("unusable password admitted or returned an execution error", err)
					}
				}
				if reads != 0 || hasher.hashCalls != 1 || hasher.verifyCalls != 4 || len(hasher.encodings) != 4 {
					t.Fatal("dummy work or denial boundary changed")
				}
				for _, encoded := range hasher.encodings {
					if encoded != "hash:dummy" {
						t.Fatal("reserved marker reached password verifier")
					}
				}
				value, err := authenticator.Resolve(t.Context(), "member")
				if err != nil || value.HasUsablePassword() || !value.Principal().Active() || !value.Principal().Staff() || !value.Principal().Has("helpdesk.view_ticket") || !value.MatchesSessionStamp(credential.SessionStamp()) {
					t.Fatal("non-password identity resolution changed", err)
				}
				failure := errors.New("private verifier failure")
				hasher.verifyError = failure
				if _, err := authenticator.Authenticate(t.Context(), "member", "wrong"); !errors.Is(err, failure) || errors.Is(err, auth.ErrInvalidCredentials) {
					t.Fatal("password execution failure downgraded", err)
				}
			})
		}
	}
	if (auth.Credential{}).HasUsablePassword() {
		t.Fatal("zero credential usable")
	}
}

func TestFreshUnusableMarkersRotateStampsWithoutExposingOrChangingAuthority(t *testing.T) {
	previous := storedTestCredential(t, "member", "member", "hash:old", true, true, true, "helpdesk.view_ticket")
	seen := map[string]bool{}
	for range 32 {
		marker, err := auth.MakeUnusablePassword(t.Context())
		if err != nil || len(marker) != 44 || !strings.HasPrefix(marker, "!") || seen[marker] {
			t.Fatal("unusable marker lacks fresh bounded entropy", err)
		}
		seen[marker] = true
		credential, err := auth.NewCredential("member", marker, previous.Principal())
		if err != nil || credential.HasUsablePassword() || credential.MatchesSessionStamp(previous.SessionStamp()) || !credential.Principal().Superuser() {
			t.Fatal("marker rotation lost credential boundary", err)
		}
		if strings.Contains(fmt.Sprintf("%v %+v %#v", credential, credential, credential), marker) {
			t.Fatal("marker leaked")
		}
		previous = credential
	}
	for _, marker := range []string{"!\n", "!\x00", "!" + strings.Repeat("a", 2048)} {
		if _, err := auth.NewCredential("member", marker, previous.Principal()); err == nil {
			t.Fatal("reserved prefix bypassed encoded bounds")
		}
	}
}
