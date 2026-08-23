package auth_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestPBKDF2HashVerifyAndBoundedParser(t *testing.T) {
	t.Parallel()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations:       10_000,
		SaltBytes:        16,
		KeyBytes:         32,
		MaxPasswordBytes: 64,
		MaxEncodedBytes:  256,
		Random:           bytes.NewReader(bytes.Repeat([]byte{7}, 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(context.Background(), "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "pbkdf2_sha256$v1$10000$") || strings.Contains(encoded, "correct horse") {
		t.Fatal("unexpected encoded password shape")
	}
	verified, err := hasher.Verify(context.Background(), "correct horse", encoded)
	if err != nil || !verified {
		t.Fatalf("valid verification: verified=%v err=%v", verified, err)
	}
	verified, err = hasher.Verify(context.Background(), "wrong", encoded)
	if err != nil || verified {
		t.Fatalf("wrong verification: verified=%v err=%v", verified, err)
	}
	if err := hasher.ValidateEncoded(encoded); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(encoded, "$")
	parts[2] = "10001"
	mismatchedProfile := strings.Join(parts, "$")
	if err := hasher.ValidateEncoded(mismatchedProfile); !errors.Is(err, &auth.Error{Code: auth.CodeInvalidHash}) {
		t.Fatalf("accepted mismatched credential work profile: %v", err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "profile-test", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential("profile-test", mismatchedProfile, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher); !errors.Is(err, &auth.Error{Code: auth.CodeInvalidConfig}) {
		t.Fatalf("authenticator accepted timing-distinct stored work profile: %v", err)
	}

	malformed := []string{
		"",
		"PBKDF2$v1$10000$secret$hash",
		"pbkdf2_sha256$v1$09999$secret$hash",
		"pbkdf2_sha256$v1$2000001$secret$hash",
		strings.Repeat("SHOULD_NOT_ESCAPE", 32),
	}
	for _, candidate := range malformed {
		err := hasher.ValidateEncoded(candidate)
		if !errors.Is(err, &auth.Error{Code: auth.CodeInvalidHash}) {
			t.Fatalf("accepted malformed hash: %v", err)
		}
		if strings.Contains(err.Error(), "SHOULD_NOT_ESCAPE") {
			t.Fatalf("encoded hash leaked through diagnostic: %v", err)
		}
	}
	if _, err := hasher.Hash(context.Background(), strings.Repeat("p", 65)); !errors.Is(err, &auth.Error{Code: auth.CodeInvalidInput}) {
		t.Fatalf("expected password cap, got %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hasher.Verify(canceled, "password", encoded); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
}

func TestDefaultPBKDF2ProfileUsesCurrentBoundedParameters(t *testing.T) {
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Random: bytes.NewReader(bytes.Repeat([]byte{9}, 16))})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(context.Background(), "profile-password")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2_sha256" || parts[1] != "v1" || parts[2] != "600000" {
		t.Fatalf("unexpected default encoded profile")
	}
	verified, err := hasher.Verify(context.Background(), "profile-password", encoded)
	if err != nil || !verified {
		t.Fatalf("default profile verify: verified=%v err=%v", verified, err)
	}
}

func TestPBKDF2EntropyFailureDoesNotRenderCause(t *testing.T) {
	t.Parallel()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations: 10_000,
		Random:     failingReader{err: errors.New("SHOULD_NOT_ESCAPE")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hasher.Hash(context.Background(), "password")
	if !errors.Is(err, &auth.Error{Code: auth.CodeEntropy}) {
		t.Fatalf("expected entropy error, got %v", err)
	}
	if strings.Contains(err.Error(), "SHOULD_NOT_ESCAPE") {
		t.Fatalf("entropy cause leaked: %v", err)
	}
}

func TestMemoryAuthenticatorUniformUnknownInactiveAndWrongPassword(t *testing.T) {
	t.Parallel()
	view, _ := auth.NewPermission("article.article.view")
	active, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true, Permissions: []auth.Permission{view}})
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "disabled", Active: false, Permissions: []auth.Permission{view}})
	if err != nil {
		t.Fatal(err)
	}
	activeCredential, err := auth.NewCredential("admin", "hash-active", active)
	if err != nil {
		t.Fatal(err)
	}
	inactiveCredential, err := auth.NewCredential("disabled", "hash-inactive", inactive)
	if err != nil {
		t.Fatal(err)
	}
	hasher := &spyHasher{expected: map[string]string{"hash-active": "correct", "hash-inactive": "correct"}}
	authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{activeCredential, inactiveCredential}, hasher)
	if err != nil {
		t.Fatal(err)
	}
	hasher.ResetVerify()

	principal, err := authenticator.Authenticate(context.Background(), "admin", "correct")
	if err != nil || !principal.Authenticated() || principal.ID() != "operator" {
		t.Fatalf("valid authentication: principal=%v err=%v", principal, err)
	}
	if hasher.VerifyCount() != 1 {
		t.Fatalf("valid authentication performed %d verifies", hasher.VerifyCount())
	}

	for _, attempt := range []struct{ username, password string }{
		{username: "admin", password: "wrong"},
		{username: "unknown", password: "wrong"},
		{username: "disabled", password: "correct"},
	} {
		hasher.ResetVerify()
		principal, err := authenticator.Authenticate(context.Background(), attempt.username, attempt.password)
		if !errors.Is(err, auth.ErrInvalidCredentials) || principal.Authenticated() {
			t.Fatalf("nonuniform invalid result for %q: principal=%v err=%v", attempt.username, principal, err)
		}
		if hasher.VerifyCount() != 1 {
			t.Fatalf("invalid authentication performed %d verifies", hasher.VerifyCount())
		}
		if err.Error() != auth.ErrInvalidCredentials.Error() || strings.Contains(err.Error(), attempt.username) {
			t.Fatalf("identity leaked through invalid diagnostic: %v", err)
		}
	}

	resolved, err := authenticator.Resolve(context.Background(), "operator")
	if err != nil || resolved.ID() != "operator" {
		t.Fatalf("resolve active: principal=%v err=%v", resolved, err)
	}
	for _, id := range []string{"disabled", "missing"} {
		if _, err := authenticator.Resolve(context.Background(), id); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("resolve %q was not uniform: %v", id, err)
		}
	}
	if fmt.Sprint(activeCredential) != "auth.Credential{redacted}" || strings.Contains(fmt.Sprint(active), "operator") {
		t.Fatal("opaque credential or principal formatting exposed state")
	}
}

func TestPrincipalAndAuthorizerAreImmutable(t *testing.T) {
	t.Parallel()
	view, err := auth.NewPermission("article.article.view")
	if err != nil {
		t.Fatal(err)
	}
	permissions := []auth.Permission{view}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true, Permissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	permissions[0] = auth.Permission("article.article.delete")
	returned := principal.Permissions()
	returned[0] = auth.Permission("article.article.delete")
	allowed, err := (auth.PrincipalAuthorizer{}).Allowed(context.Background(), principal, view)
	if err != nil || !allowed {
		t.Fatalf("immutable permission was lost: allowed=%v err=%v", allowed, err)
	}
	deletePermission, _ := auth.NewPermission("article.article.delete")
	allowed, err = (auth.PrincipalAuthorizer{}).Allowed(context.Background(), principal, deletePermission)
	if err != nil || allowed {
		t.Fatalf("unexpected permission: allowed=%v err=%v", allowed, err)
	}
	if auth.Anonymous().Authenticated() || auth.Anonymous().Has(view) {
		t.Fatal("anonymous principal was authenticated or permitted")
	}
}

func TestPermissionAndCredentialConstructionBounds(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "single", "Article.view", "article..view", "article.view!", strings.Repeat("a", 129)} {
		if _, err := auth.NewPermission(value); err == nil {
			t.Fatalf("accepted invalid permission %q", value)
		}
	}
	permission, _ := auth.NewPermission("article.article.view")
	principal, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true, Permissions: []auth.Permission{permission}})
	if _, err := auth.NewCredential(" user ", "encoded", principal); err == nil {
		t.Fatal("accepted padded username")
	}
	if _, err := auth.NewCredential("user", strings.Repeat("x", 2049), principal); err == nil {
		t.Fatal("accepted oversized encoded hash")
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

type spyHasher struct {
	mu       sync.Mutex
	expected map[string]string
	verifies int
}

func (h *spyHasher) Hash(ctx context.Context, password string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "hash-dummy", nil
}

func (h *spyHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.verifies++
	want, ok := h.expected[encoded]
	return ok && password == want, nil
}

func (h *spyHasher) ValidateEncoded(encoded string) error {
	if encoded == "hash-active" || encoded == "hash-inactive" || encoded == "hash-dummy" {
		return nil
	}
	return &auth.Error{Code: auth.CodeInvalidHash, Detail: "invalid test hash"}
}

func (h *spyHasher) VerifyCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.verifies
}

func (h *spyHasher) ResetVerify() {
	h.mu.Lock()
	h.verifies = 0
	h.mu.Unlock()
}
