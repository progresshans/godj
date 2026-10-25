package auth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestLoginInputRequiresExplicitBoundedMetadata(t *testing.T) {
	for _, test := range []struct {
		name          string
		label         string
		maximum       int
		normalization auth.LoginNormalization
		valid         bool
	}{
		{"exact", "Contact", 100, auth.LoginExact, true},
		{"nfkc", "Username", 256, auth.LoginUnicode16NFKC, true},
		{"envelope", "Login", auth.MaximumUsernameBytes, auth.LoginExact, true},
		{"missing_label", "", 100, auth.LoginExact, false},
		{"control_label", "Login\nvalue", 100, auth.LoginExact, false},
		{"invalid_label", "\xff", 100, auth.LoginExact, false},
		{"long_label", strings.Repeat("x", 129), 100, auth.LoginExact, false},
		{"missing_limit", "Login", 0, auth.LoginExact, false},
		{"negative_limit", "Login", -1, auth.LoginExact, false},
		{"oversized_limit", "Login", auth.MaximumUsernameBytes + 1, auth.LoginExact, false},
		{"missing_policy", "Login", 100, "", false},
		{"unsupported_policy", "Login", 100, "lowercase", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := auth.LoginInputConfig{Label: test.label, MaxLength: test.maximum, Normalization: test.normalization}
			input, err := auth.NewLoginInput(config)
			if test.valid {
				if err != nil || input.Validate() != nil || input.Label() != test.label || input.MaxLength() != test.maximum || input.Normalization() != test.normalization {
					t.Fatal("login input lost explicit declaration", err)
				}
				config.Label = "mutated"
				config.MaxLength = 1
				config.Normalization = ""
				if input.Label() != test.label || input.MaxLength() != test.maximum || input.Normalization() != test.normalization {
					t.Fatal("login input aliases caller configuration")
				}
			} else if err == nil || input != (auth.LoginInput{}) {
				t.Fatal("invalid login metadata published", err)
			}
		})
	}
	if (auth.LoginInput{}).Validate() == nil {
		t.Fatal("zero login input supplied an implicit account")
	}
}

type loginMetadataStore struct {
	t       *testing.T
	input   auth.LoginInput
	failure error
	calls   int
}

func (store *loginMetadataStore) CredentialByUsername(context.Context, string) (auth.Credential, bool, error) {
	store.t.Fatal("metadata queried credentials")
	return auth.Credential{}, false, nil
}
func (store *loginMetadataStore) CredentialByID(context.Context, string) (auth.Credential, bool, error) {
	store.t.Fatal("metadata resolved credentials")
	return auth.Credential{}, false, nil
}
func (store *loginMetadataStore) LoginInput() (auth.LoginInput, error) {
	store.calls++
	return store.input, store.failure
}

type loginMetadataHasher struct {
	auth.PasswordHasher
	calls int
}

func (hasher *loginMetadataHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.calls++
	return hasher.PasswordHasher.Hash(ctx, raw)
}

func TestStoredLoginInputUsesExactCredentialSourceWithoutPasswordWork(t *testing.T) {
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	input, err := auth.NewLoginInput(auth.LoginInputConfig{Label: "Contact", MaxLength: 64, Normalization: auth.LoginExact})
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("private metadata failure")
	for _, mode := range []string{"valid", "missing_provider", "zero", "failure"} {
		t.Run(mode, func(t *testing.T) {
			store := &loginMetadataStore{t: t, input: input}
			if mode == "zero" {
				store.input = auth.LoginInput{}
			}
			if mode == "failure" {
				store.failure = fault
			}
			var source auth.CredentialStore = store
			if mode == "missing_provider" {
				source = struct{ auth.CredentialStore }{store}
			}
			hasher := &loginMetadataHasher{PasswordHasher: inner}
			verifier, err := auth.NewStoredAuthenticator(t.Context(), source, hasher)
			if err != nil {
				t.Fatal(err)
			}
			before := hasher.calls
			got, err := verifier.LoginInput()
			if mode == "valid" {
				if err != nil || got != input {
					t.Fatal("stored login source changed", err)
				}
			} else {
				if err == nil || got != (auth.LoginInput{}) || !errors.Is(err, &auth.Error{Code: auth.CodeInvalidConfig, Field: "login_input"}) {
					t.Fatal("invalid metadata became usable", err)
				}
				if mode == "failure" && !errors.Is(err, fault) {
					t.Fatal("metadata cause lost", err)
				}
				if strings.Contains(fmt.Sprintf("%#v", err), fault.Error()) {
					t.Fatal("private metadata cause leaked")
				}
			}
			wantCalls := 1
			if mode == "missing_provider" {
				wantCalls = 0
			}
			if store.calls != wantCalls || hasher.calls != before {
				t.Fatal("metadata lookup performed work or inferred another provider")
			}
		})
	}
}
