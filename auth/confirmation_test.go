package auth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestStoredPasswordConfirmationUsesStableIdentityAndCurrentCredential(t *testing.T) {
	for _, mode := range []string{"success", "username", "permissions", "password", "rehash", "unusable", "inactive", "deleted", "wrong_identity", "read_error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			before := storedTestCredential(t, "member-id", "original-name", "hash: password ", true, false, false, "app.item.view")
			current, present := before, true
			failure := errors.New("private confirmation read failure")
			var readError error
			reads := 0
			store := &observedCredentialStore{
				byName: func(context.Context, string) (auth.Credential, bool, error) {
					t.Fatal("confirmation looked up a mutable username")
					return auth.Credential{}, false, nil
				},
				byID: func(_ context.Context, id string) (auth.Credential, bool, error) {
					if id != "member-id" {
						t.Fatal("confirmation changed the requested identity")
					}
					reads++
					return current, present, readError
				},
			}
			hasher := &storedProbeHasher{onVerify: func() {
				if reads != 1 {
					t.Fatal("password verification did not follow a completed observation")
				}
				switch mode {
				case "username":
					current = storedTestCredential(t, "member-id", "renamed", "hash: password ", true, false, false, "app.item.view")
				case "permissions":
					current = storedTestCredential(t, "member-id", "original-name", "hash: password ", true, true, false, "app.item.change")
				case "password":
					current = storedTestCredential(t, "member-id", "original-name", "hash:replacement", true, false, false)
				case "rehash":
					current = storedTestCredential(t, "member-id", "original-name", "rehash: password ", true, false, false)
				case "unusable":
					current = storedTestCredential(t, "member-id", "original-name", "!disabled", true, false, false)
				case "inactive":
					current = storedTestCredential(t, "member-id", "original-name", "hash: password ", false, false, false)
				case "deleted":
					present = false
				case "wrong_identity":
					current = storedTestCredential(t, "another-id", "original-name", "hash: password ", true, false, false)
				case "read_error":
					readError = failure
				case "cancel":
					cancel()
				}
			}}
			prepared, err := auth.NewStoredAuthenticator(ctx, store, hasher)
			if err != nil {
				t.Fatal(err)
			}
			var confirmer auth.PasswordConfirmer = prepared
			result, err := confirmer.ConfirmPassword(ctx, "member-id", " password ")
			success := mode == "success" || mode == "username" || mode == "permissions"
			if success {
				if err != nil || result.Principal().ID() != "member-id" || !result.MatchesSessionStamp(before.SessionStamp()) {
					t.Fatal("stable identity confirmation failed", err)
				}
				if result.Principal().Staff() != (mode == "permissions") || result.Principal().Has("app.item.change") != (mode == "permissions") {
					t.Fatal("confirmation published stale authorization")
				}
			} else if err == nil || result.Principal().ID() != "" {
				t.Fatal("changed credential was confirmed", mode)
			}
			if mode == "read_error" && !errors.Is(err, failure) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("confirmation lost execution failure", err)
			}
			wantReads := 2
			if mode == "cancel" {
				wantReads = 1
			}
			if reads != wantReads || hasher.verifyCalls != 1 || hasher.hashCalls != 1 {
				t.Fatal("confirmation retried password work or skipped reread", reads, hasher.verifyCalls)
			}
			if before.Principal().Staff() || !before.Principal().Has("app.item.view") {
				t.Fatal("confirmation mutated an earlier snapshot")
			}
		})
	}
}

func TestStoredPasswordConfirmationRejectsUnknownInactiveAndBrokenSources(t *testing.T) {
	for _, mode := range []string{"unknown", "malformed_id", "inactive", "unusable", "wrong_password", "stripped_password", "wrong_identity", "invalid_hash", "source_error", "canceled", "nil_context"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			id, password, hash := "member-id", " password ", "hash: password "
			if mode == "malformed_id" {
				id = "bad\x00id"
			}
			if mode == "wrong_password" {
				password = "wrong"
			}
			if mode == "stripped_password" {
				password = "password"
			}
			if mode == "unusable" {
				hash = "!deliberately-disabled"
			}
			if mode == "invalid_hash" {
				hash = "unsupported:hash"
			}
			principalID := "member-id"
			if mode == "wrong_identity" {
				principalID = "another-id"
			}
			credential := storedTestCredential(t, principalID, "member", hash, mode != "inactive", false, false)
			fault := errors.New("private confirmation source")
			reads := 0
			store := &observedCredentialStore{
				byName: func(context.Context, string) (auth.Credential, bool, error) {
					t.Fatal("confirmation used a username")
					return auth.Credential{}, false, nil
				},
				byID: func(context.Context, string) (auth.Credential, bool, error) {
					reads++
					if mode == "source_error" {
						return auth.Credential{}, false, fault
					}
					return credential, mode != "unknown", nil
				},
			}
			hasher := &storedProbeHasher{}
			prepared, err := auth.NewStoredAuthenticator(ctx, store, hasher)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				cancel()
			}
			if mode == "nil_context" {
				ctx = nil
			}
			result, err := prepared.ConfirmPassword(ctx, id, password)
			if err == nil || result.Principal().ID() != "" {
				t.Fatal("invalid confirmation succeeded", mode)
			}
			wantReads, wantWork := 1, 1
			if mode == "wrong_identity" || mode == "invalid_hash" || mode == "source_error" {
				wantWork = 0
			}
			if mode == "malformed_id" {
				wantReads = 0
			}
			if mode == "canceled" || mode == "nil_context" {
				wantReads, wantWork = 0, 0
			}
			if reads != wantReads || hasher.verifyCalls != wantWork {
				t.Fatal("invalid confirmation performed wrong work", mode, reads, hasher.verifyCalls)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) || mode == "source_error" && !errors.Is(err, fault) {
				t.Fatal("confirmation hid a source failure", err)
			}
		})
	}
}

func TestPasswordVerifierDoesNotLowerWrappedExecutionErrorsToCredentialDenials(t *testing.T) {
	input := &auth.Error{Code: auth.CodeInvalidInput, Field: "password"}
	private := errors.New("private verification marker")
	for _, failure := range []error{input, fmt.Errorf("private wrapper: %w", input), errors.Join(input, context.Canceled, private), &auth.Error{Code: auth.CodeInvalidInput, Field: "password", Cause: context.DeadlineExceeded}} {
		for _, mode := range []string{"memory", "stored", "confirmation"} {
			t.Run(mode+"/"+fmt.Sprintf("%T", failure), func(t *testing.T) {
				credential := storedTestCredential(t, "member-id", "member", "hash:password", true, false, false)
				lookup := func(context.Context, string) (auth.Credential, bool, error) { return credential, true, nil }
				hasher := failingStoredHasher{PasswordHasher: &storedProbeHasher{}, verifyErr: failure}
				var result auth.Credential
				var err error
				if mode == "memory" {
					a, e := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
					if e != nil {
						t.Fatal(e)
					}
					result, err = a.Authenticate(t.Context(), "member", "password")
				} else {
					a, e := auth.NewStoredAuthenticator(t.Context(), &observedCredentialStore{byName: lookup, byID: lookup}, hasher)
					if e != nil {
						t.Fatal(e)
					}
					if mode == "confirmation" {
						result, err = a.ConfirmPassword(t.Context(), "member-id", "password")
					} else {
						result, err = a.Authenticate(t.Context(), "member", "password")
					}
				}
				if err == nil || result.Principal().ID() != "" {
					t.Fatal("verifier failure authenticated")
				}
				if failure == input {
					if err != auth.ErrInvalidCredentials {
						t.Fatal("direct input refusal lost uniform denial")
					}
				} else {
					if errors.Is(err, auth.ErrInvalidCredentials) || !errors.Is(err, failure) {
						t.Fatal("wrapped execution failure became a credential denial", err)
					}
				}
				if strings.Contains(fmt.Sprintf("%+v %#v", err, err), "private") {
					t.Fatal("verifier failure exposed private material")
				}
			})
		}
	}
}
