package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

type observedCredentialStore struct {
	byName func(context.Context, string) (auth.Credential, bool, error)
	byID   func(context.Context, string) (auth.Credential, bool, error)
}

func (store *observedCredentialStore) CredentialByUsername(ctx context.Context, name string) (auth.Credential, bool, error) {
	return store.byName(ctx, name)
}
func (store *observedCredentialStore) CredentialByID(ctx context.Context, id string) (auth.Credential, bool, error) {
	return store.byID(ctx, id)
}

type storedProbeHasher struct {
	verifyCalls, hashCalls int
	onVerify               func()
	onValidate             func(string)
}

func (hasher *storedProbeHasher) Hash(context.Context, string) (string, error) {
	hasher.hashCalls++
	return "hash:dummy", nil
}
func (hasher *storedProbeHasher) ValidateEncoded(encoded string) error {
	if hasher.onValidate != nil {
		hasher.onValidate(encoded)
	}
	if !strings.HasPrefix(encoded, "hash:") && !strings.HasPrefix(encoded, "rehash:") {
		return errors.New("private invalid encoded hash")
	}
	return nil
}
func (hasher *storedProbeHasher) Verify(_ context.Context, password, encoded string) (bool, error) {
	hasher.verifyCalls++
	if hasher.onVerify != nil {
		hasher.onVerify()
	}
	if len(password) > 1024 {
		return false, &auth.Error{Code: auth.CodeInvalidInput, Field: "password"}
	}
	return encoded == "hash:"+password || encoded == "rehash:"+password, nil
}
func storedTestCredential(t *testing.T, id, name, hash string, active, staff, superuser bool, permissions ...auth.Permission) auth.Credential {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: active, Staff: staff, Superuser: superuser, Permissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential(name, hash, principal)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func TestStoredAuthenticatorUniformFailuresAndBoundedPasswordWork(t *testing.T) {
	for _, mode := range []string{"unknown", "malformed", "inactive", "wrong", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			credential := storedTestCredential(t, "member", "member", "hash:password", mode != "inactive", false, false)
			names, ids := 0, 0
			store := &observedCredentialStore{
				byName: func(context.Context, string) (auth.Credential, bool, error) {
					names++
					return credential, mode != "unknown", nil
				},
				byID: func(context.Context, string) (auth.Credential, bool, error) { ids++; return credential, true, nil },
			}
			hasher := &storedProbeHasher{}
			authenticator, err := auth.NewStoredAuthenticator(t.Context(), store, hasher)
			if err != nil {
				t.Fatal(err)
			}
			username, password := "member", "password"
			if mode == "malformed" {
				username = "bad\x00name"
			}
			if mode == "wrong" {
				password = "wrong"
			}
			if mode == "oversized" {
				password = strings.Repeat("x", 1025)
			}
			got, err := authenticator.Authenticate(t.Context(), username, password)
			if !errors.Is(err, auth.ErrInvalidCredentials) || got.Principal().ID() != "" || ids != 0 || hasher.verifyCalls != 1 || hasher.hashCalls != 1 {
				t.Fatal("failed authentication performed unexpected work or published a credential", err, names, ids, hasher.verifyCalls, hasher.hashCalls)
			}
			if mode == "malformed" && names != 0 {
				t.Fatal("malformed username reached store")
			}
		})
	}
}

func TestStoredAuthenticatorRechecksCredentialAndUsesCurrentAuthorization(t *testing.T) {
	for _, mode := range []string{"password", "rehash", "username", "identity", "inactive", "deleted", "authorization", "read_error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			before := storedTestCredential(t, "member", "member", "hash:password", true, false, false, "helpdesk.ticket.view")
			current := before
			present := true
			var readErr error
			reads := 0
			store := &observedCredentialStore{
				byName: func(context.Context, string) (auth.Credential, bool, error) { return before, true, nil },
				byID: func(context.Context, string) (auth.Credential, bool, error) {
					reads++
					return current, present, readErr
				},
			}
			hasher := &storedProbeHasher{onVerify: func() {
				switch mode {
				case "password":
					current = storedTestCredential(t, "member", "member", "hash:new", true, false, false)
				case "rehash":
					current = storedTestCredential(t, "member", "member", "rehash:password", true, false, false)
				case "username":
					current = storedTestCredential(t, "member", "renamed", "hash:password", true, false, false)
				case "identity":
					current = storedTestCredential(t, "other", "member", "hash:password", true, false, false)
				case "inactive":
					current = storedTestCredential(t, "member", "member", "hash:password", false, true, true)
				case "deleted":
					present = false
				case "authorization":
					current = storedTestCredential(t, "member", "member", "hash:password", true, true, true, "helpdesk.ticket.change")
				case "read_error":
					readErr = errors.New("private source failure")
				case "cancel":
					cancel()
				}
			}}
			authenticator, err := auth.NewStoredAuthenticator(ctx, store, hasher)
			if err != nil {
				t.Fatal(err)
			}
			got, err := authenticator.Authenticate(ctx, "member", "password")
			if mode == "authorization" {
				if err != nil || !got.Principal().Staff() || !got.Principal().Superuser() || got.Principal().Permissions()[0] != "helpdesk.ticket.change" || got.SessionStamp() != before.SessionStamp() {
					t.Fatal("authentication did not use current authorization", err)
				}
			} else if err == nil || got.Principal().ID() != "" {
				t.Fatal("changed credential authenticated", mode)
			}
			if mode == "read_error" && !errors.Is(err, readErr) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost source/cancellation cause", err)
			}
			if mode != "cancel" && reads != 1 || hasher.verifyCalls != 1 {
				t.Fatal("credential check retried or skipped re-read", reads, hasher.verifyCalls)
			}
			if before.Principal().Staff() || before.Principal().Superuser() || before.Principal().Permissions()[0] != "helpdesk.ticket.view" {
				t.Fatal("older credential changed")
			}
		})
	}
}

func TestStoredAuthenticatorRejectsBrokenSourcesAndHonorsValidationCancellation(t *testing.T) {
	for _, mode := range []string{"wrong_username", "wrong_id", "zero", "bad_profile", "source_error", "canceled_source", "canceled_validation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			credential := storedTestCredential(t, "member", "member", "hash:password", true, false, false)
			fault := errors.New("private source or hash material")
			var sourceErr error
			if mode == "wrong_username" {
				credential = storedTestCredential(t, "member", "other", "hash:password", true, false, false)
			}
			if mode == "wrong_id" {
				credential = storedTestCredential(t, "other", "member", "hash:password", true, false, false)
			}
			if mode == "zero" {
				credential = auth.Credential{}
			}
			if mode == "bad_profile" {
				credential = storedTestCredential(t, "member", "member", "invalid", true, false, false)
			}
			if mode == "source_error" {
				sourceErr = fault
			}
			lookup := func(context.Context, string) (auth.Credential, bool, error) {
				if mode == "canceled_source" {
					cancel()
				}
				return credential, true, sourceErr
			}
			hasher := &storedProbeHasher{}
			authenticator, err := auth.NewStoredAuthenticator(ctx, &observedCredentialStore{byName: lookup, byID: lookup}, hasher)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "canceled_validation" {
				hasher.onValidate = func(string) { cancel() }
			}
			var got auth.Credential
			if mode == "wrong_username" {
				got, err = authenticator.Authenticate(ctx, "member", "password")
			} else {
				got, err = authenticator.Resolve(ctx, "member")
			}
			if err == nil || got.Principal().ID() != "" {
				t.Fatal("broken source published credential")
			}
			if mode == "source_error" && !errors.Is(err, fault) || strings.HasPrefix(mode, "canceled") && !errors.Is(err, context.Canceled) {
				t.Fatal("lost failure cause", err)
			}
			encoded, jsonErr := json.Marshal(err)
			if jsonErr != nil {
				t.Fatal(jsonErr)
			}
			for _, display := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), string(encoded)} {
				if strings.Contains(display, "private") || strings.Contains(display, "hash:password") {
					t.Fatal("secret escaped source failure")
				}
			}
		})
	}
	var store *observedCredentialStore
	var hasher *storedProbeHasher
	if _, err := auth.NewStoredAuthenticator(t.Context(), store, &storedProbeHasher{}); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := auth.NewStoredAuthenticator(t.Context(), &observedCredentialStore{}, hasher); err == nil {
		t.Fatal("nil hasher accepted")
	}
	probe := &storedProbeHasher{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if _, err := auth.NewStoredAuthenticator(ctx, &observedCredentialStore{}, probe); err == nil || probe.hashCalls != 0 {
			t.Fatal("invalid context performed password work")
		}
	}
	var uninitialized auth.StoredAuthenticator
	if _, err := uninitialized.Resolve(t.Context(), "member"); err == nil {
		t.Fatal("uninitialized authenticator resolved")
	}
}

func TestPrincipalRoleFlagsPreserveActiveAndCanonicalPermissionBoundaries(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, staff := range []bool{false, true} {
			for _, superuser := range []bool{false, true} {
				principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "member", Active: active, Staff: staff, Superuser: superuser, Permissions: []auth.Permission{"helpdesk.ticket.view"}})
				if err != nil {
					t.Fatal(err)
				}
				if principal.Active() != active || principal.Staff() != staff || principal.Superuser() != superuser || principal.Has("helpdesk.ticket.view") != active || principal.Has("unregistered.permission") != (active && superuser) || principal.Has("not-a-permission") {
					t.Fatal("role or permission boundary changed", active, staff, superuser)
				}
				if len(principal.Permissions()) != 1 {
					t.Fatal("implicit superuser grants became a fabricated catalog")
				}
			}
		}
	}
}

type failingStoredHasher struct {
	auth.PasswordHasher
	hashErr, verifyErr error
}

func (hasher failingStoredHasher) Hash(ctx context.Context, password string) (string, error) {
	if hasher.hashErr != nil {
		return "", hasher.hashErr
	}
	return hasher.PasswordHasher.Hash(ctx, password)
}
func (hasher failingStoredHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	if hasher.verifyErr != nil {
		return false, hasher.verifyErr
	}
	return hasher.PasswordHasher.Verify(ctx, password, encoded)
}
func TestCredentialHasherWrappedCancellationKeepsSecretsPrivate(t *testing.T) {
	private := errors.New("private hash and password cause")
	wrapped := errors.Join(context.Canceled, private)
	credential := storedTestCredential(t, "member", "member", "hash:password", true, false, false)
	lookup := func(context.Context, string) (auth.Credential, bool, error) { return credential, true, nil }
	store := &observedCredentialStore{byName: lookup, byID: lookup}
	for _, mode := range []string{"stored_hash", "stored_verify", "memory_hash", "memory_verify"} {
		t.Run(mode, func(t *testing.T) {
			hasher := failingStoredHasher{PasswordHasher: &storedProbeHasher{}}
			if strings.HasSuffix(mode, "hash") {
				hasher.hashErr = wrapped
			} else {
				hasher.verifyErr = wrapped
			}
			var failure error
			if strings.HasPrefix(mode, "stored") {
				authenticator, err := auth.NewStoredAuthenticator(t.Context(), store, hasher)
				failure = err
				if err == nil {
					_, failure = authenticator.Authenticate(t.Context(), "member", "password")
				}
			} else {
				authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
				failure = err
				if err == nil {
					_, failure = authenticator.Authenticate(t.Context(), "member", "password")
				}
			}
			if !errors.Is(failure, context.Canceled) || !errors.Is(failure, private) {
				t.Fatal("wrapped password failure lost causes", failure)
			}
			encoded, err := json.Marshal(failure)
			if err != nil {
				t.Fatal(err)
			}
			for _, display := range []string{fmt.Sprint(failure), fmt.Sprintf("%+v", failure), fmt.Sprintf("%#v", failure), string(encoded)} {
				if strings.Contains(display, "private hash") {
					t.Fatal("wrapped cancellation exposed password material")
				}
			}
		})
	}
}
