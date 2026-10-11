package sessionauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web/sessionauth"
)

type replaceableAuthenticator struct {
	mu      sync.RWMutex
	current auth.CredentialAuthenticator
}

func (a *replaceableAuthenticator) set(current auth.CredentialAuthenticator) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.current = current
}

func (a *replaceableAuthenticator) snapshot() auth.CredentialAuthenticator {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.current
}

func (a *replaceableAuthenticator) Authenticate(ctx context.Context, username, password string) (auth.Credential, error) {
	return a.snapshot().Authenticate(ctx, username, password)
}

func (a *replaceableAuthenticator) Resolve(ctx context.Context, id string) (auth.Credential, error) {
	return a.snapshot().Resolve(ctx, id)
}

type credentialResultAuthenticator struct {
	credential auth.Credential
	err        error
}

func (a credentialResultAuthenticator) Authenticate(context.Context, string, string) (auth.Credential, error) {
	return a.credential, a.err
}

func (a credentialResultAuthenticator) Resolve(context.Context, string) (auth.Credential, error) {
	return a.credential, a.err
}

func credentialForSessionTest(t *testing.T, id, username, encoded string, active bool, permissions ...auth.Permission) auth.Credential {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: active, Permissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential(username, encoded, principal)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func memoryCredentials(t *testing.T, hasher auth.PasswordHasher, credential auth.Credential) *auth.MemoryAuthenticator {
	t.Helper()
	result, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func loginCredentialSession(t *testing.T, h *harness, username, password string) sessions.ID {
	t.Helper()
	token := readBody(t, h.Do(t, http.MethodGet, loginPath, nil))
	response := h.Do(t, http.MethodPost, loginPath, http.Header{
		"X-Test-Username":   []string{username},
		"X-Test-Password":   []string{password},
		"X-Test-Form-Token": []string{token},
	})
	defer closeBody(t, response)
	if response.StatusCode != http.StatusFound {
		t.Fatalf("credential login status = %d", response.StatusCode)
	}
	cookie := namedResponseCookie(t, response.Cookies(), sessionauth.DefaultSessionCookieName)
	id, err := sessions.ParseID(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCredentialChangesInvalidateExistingHTTPSessions(t *testing.T) {
	type referenceObservation struct {
		After struct {
			Authenticated bool `json:"authenticated"`
			HasView       bool `json:"has_view"`
		} `json:"after"`
	}
	reference := map[string]referenceObservation{}
	for _, backend := range []string{"sqlite", "postgres"} {
		payload, err := os.ReadFile("testdata/credential-session-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var capture struct {
			Observations map[string]referenceObservation `json:"observations"`
		}
		if err := json.Unmarshal(payload, &capture); err != nil || len(capture.Observations) != 7 {
			t.Fatalf("invalid %s reference: %v", backend, err)
		}
		for mode, row := range capture.Observations {
			if prior, found := reference[mode]; found && prior != row {
				t.Fatalf("backend reference disagreement: %s", mode)
			}
			reference[mode] = row
		}
	}
	for _, name := range []string{"password", "rehash", "permissions", "username", "inactive", "wrong_identity", "zero_credential", "resolution_error"} {
		t.Run(name, func(t *testing.T) {
			hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := hasher.Hash(t.Context(), "first password")
			if err != nil {
				t.Fatal(err)
			}
			initial := credentialForSessionTest(t, "member", "alice", encoded, true, "article.article.view")
			provider := &replaceableAuthenticator{current: memoryCredentials(t, hasher, initial)}
			h := newCredentialHarness(t, provider)
			defer h.Close()
			id := loginCredentialSession(t, h, "alice", "first password")
			record, found, err := h.store.Load(t.Context(), id)
			if err != nil || !found {
				t.Fatal("login did not publish server session")
			}
			stamp, found := record.Value("_godj_credential_stamp")
			if !found || !initial.MatchesSessionStamp(stamp) {
				t.Fatal("login did not bind credential snapshot")
			}
			response := h.Do(t, http.MethodGet, protectedPath, nil)
			if response.StatusCode != http.StatusOK {
				t.Fatal("initial credential did not authorize request")
			}
			closeBody(t, response)
			wantStatus, wantStored := http.StatusFound, false
			switch name {
			case "password", "rehash":
				password := "replacement password"
				if name == "rehash" {
					password = "first password"
				}
				replacement, err := hasher.Hash(t.Context(), password)
				if err != nil {
					t.Fatal(err)
				}
				provider.set(memoryCredentials(t, hasher, credentialForSessionTest(t, "member", "alice", replacement, true, "article.article.view")))
			case "permissions":
				provider.set(memoryCredentials(t, hasher, credentialForSessionTest(t, "member", "alice", encoded, true)))
				wantStatus, wantStored = http.StatusForbidden, true
			case "username":
				provider.set(memoryCredentials(t, hasher, credentialForSessionTest(t, "member", "renamed", encoded, true, "article.article.view")))
				wantStatus, wantStored = http.StatusOK, true
			case "inactive":
				provider.set(memoryCredentials(t, hasher, credentialForSessionTest(t, "member", "alice", encoded, false)))
			case "wrong_identity":
				provider.set(credentialResultAuthenticator{credential: credentialForSessionTest(t, "another", "alice", encoded, true, "article.article.view")})
			case "zero_credential":
				provider.set(credentialResultAuthenticator{})
			case "resolution_error":
				provider.set(credentialResultAuthenticator{err: context.Canceled})
				wantStatus, wantStored = http.StatusInternalServerError, true
			}
			if observation, found := reference[name]; found {
				wantStatus = http.StatusFound
				if observation.After.Authenticated {
					wantStatus = http.StatusForbidden
					if observation.After.HasView {
						wantStatus = http.StatusOK
					}
				}
			}
			response = h.Do(t, http.MethodGet, protectedPath, nil)
			body := readBody(t, response)
			if response.StatusCode != wantStatus {
				t.Fatalf("after credential change status = %d, want %d", response.StatusCode, wantStatus)
			}
			if strings.Contains(body, encoded) || strings.Contains(body, stamp) || strings.Contains(response.Header.Get("Set-Cookie"), stamp) {
				t.Fatal("credential material leaked into response")
			}
			if _, found, err := h.store.Load(t.Context(), id); err != nil || found != wantStored {
				t.Fatalf("after credential change session found = %v, error = %v", found, err)
			}
			if !initial.Principal().Has("article.article.view") {
				t.Fatal("previously admitted principal snapshot changed")
			}
			if name == "password" || name == "rehash" {
				password := "replacement password"
				if name == "rehash" {
					password = "first password"
				}
				newID := loginCredentialSession(t, h, "alice", password)
				if newID == id {
					t.Fatal("new credential reused revoked session ID")
				}
				response = h.Do(t, http.MethodGet, protectedPath, nil)
				if response.StatusCode != http.StatusOK {
					t.Fatal("new credential login did not authorize")
				}
				closeBody(t, response)
			}
		})
	}
}

func TestIncompleteOrForgedCredentialSessionIsFlushed(t *testing.T) {
	for _, mode := range []string{"legacy_id_only", "missing_id", "empty_stamp", "forged_stamp", "oversized_stamp"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, true)
			defer h.Close()
			values := map[string]string{"_godj_principal_id": "operator", "_godj_credential_stamp": "forged"}
			switch mode {
			case "legacy_id_only":
				delete(values, "_godj_credential_stamp")
			case "missing_id":
				delete(values, "_godj_principal_id")
			case "empty_stamp":
				values["_godj_credential_stamp"] = ""
			case "oversized_stamp":
				values["_godj_credential_stamp"] = strings.Repeat("a", 128)
			}
			record, err := h.manager.Create(t.Context(), values)
			if err != nil {
				t.Fatal(err)
			}
			response := h.Do(t, http.MethodGet, protectedPath, http.Header{"Cookie": []string{sessionauth.DefaultSessionCookieName + "=" + record.ID().Encoded()}})
			if response.StatusCode != http.StatusFound {
				t.Fatal("incomplete or forged session reached protected route")
			}
			closeBody(t, response)
			if _, found, err := h.store.Load(t.Context(), record.ID()); err != nil || found {
				t.Fatal("incomplete or forged server session was retained")
			}
		})
	}
}

type rejectedCredentialFlushStore struct{ sessions.Store }

func TestAnonymousSessionValuesSurvivePrincipalResolution(t *testing.T) {
	h := newHarness(t, true)
	defer h.Close()
	record, err := h.manager.Create(t.Context(), map[string]string{"cart": "retained"})
	if err != nil {
		t.Fatal(err)
	}
	response := h.Do(t, http.MethodGet, protectedPath, http.Header{"Cookie": []string{sessionauth.DefaultSessionCookieName + "=" + record.ID().Encoded()}})
	if response.StatusCode != http.StatusFound {
		t.Fatal("anonymous session authenticated")
	}
	closeBody(t, response)
	stored, found, err := h.store.Load(t.Context(), record.ID())
	if err != nil || !found {
		t.Fatal("anonymous application session was deleted")
	}
	if value, found := stored.Value("cart"); !found || value != "retained" {
		t.Fatal("anonymous application value changed")
	}
}

func (rejectedCredentialFlushStore) Delete(context.Context, sessions.ID) error {
	return errors.New("fixture deletion failure")
}

func TestCredentialSessionFlushFailureDoesNotAuthenticate(t *testing.T) {
	for _, stamped := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "stale"}[stamped], func(t *testing.T) {
			h := newHarness(t, true)
			defer h.Close()
			values := map[string]string{"_godj_principal_id": "operator"}
			if stamped {
				values["_godj_credential_stamp"] = "forged"
			}
			record, err := h.manager.Create(t.Context(), values)
			if err != nil {
				t.Fatal(err)
			}
			h.store.Store = rejectedCredentialFlushStore{Store: h.store.Store}
			response := h.Do(t, http.MethodGet, protectedPath, http.Header{"Cookie": []string{sessionauth.DefaultSessionCookieName + "=" + record.ID().Encoded()}})
			if response.StatusCode != http.StatusInternalServerError {
				t.Fatal("failed session cleanup was silently accepted")
			}
			closeBody(t, response)
			if _, found, err := h.store.Load(t.Context(), record.ID()); err != nil || !found {
				t.Fatal("unconfirmed flush changed server session")
			}
		})
	}
}

type afterAuthentication struct {
	auth.CredentialAuthenticator
	after func()
}

func (a afterAuthentication) Authenticate(ctx context.Context, username, password string) (auth.Credential, error) {
	credential, err := a.CredentialAuthenticator.Authenticate(ctx, username, password)
	if err == nil {
		a.after()
	}
	return credential, err
}

func TestLoginUsesTheVerifiedCredentialSnapshotAcrossAConcurrentChange(t *testing.T) {
	initial := credentialForSessionTest(t, "operator", "admin", "encoded-admin", true, "article.article.view")
	changed := credentialForSessionTest(t, "operator", "admin", "changed-encoded-password", true, "article.article.view")
	provider := &replaceableAuthenticator{current: memoryCredentials(t, plainHasher{}, initial)}
	h := newCredentialHarness(t, afterAuthentication{CredentialAuthenticator: provider, after: func() {
		provider.set(credentialResultAuthenticator{credential: changed})
	}})
	defer h.Close()
	id := loginCredentialSession(t, h, "admin", "correct")
	record, found, err := h.store.Load(t.Context(), id)
	if err != nil || !found {
		t.Fatal("admitted login did not publish")
	}
	stamp, _ := record.Value("_godj_credential_stamp")
	if !initial.MatchesSessionStamp(stamp) || changed.MatchesSessionStamp(stamp) {
		t.Fatal("login mixed credential observations")
	}
	response := h.Do(t, http.MethodGet, protectedPath, nil)
	if response.StatusCode != http.StatusFound {
		t.Fatal("changed credential accepted previously admitted login")
	}
	closeBody(t, response)
	if _, found, err := h.store.Load(t.Context(), id); err != nil || found {
		t.Fatal("stale admitted login survived next request")
	}
}

func TestLoginRotationReplacesBothPrincipalAndCredentialState(t *testing.T) {
	initial := credentialForSessionTest(t, "first", "admin", "encoded-admin", true, "article.article.view")
	changed := credentialForSessionTest(t, "second", "admin", "encoded-admin", true, "article.article.view")
	provider := &replaceableAuthenticator{current: memoryCredentials(t, plainHasher{}, initial)}
	h := newCredentialHarness(t, provider)
	defer h.Close()
	oldID := loginCredentialSession(t, h, "admin", "correct")
	provider.set(memoryCredentials(t, plainHasher{}, changed))
	newID := loginCredentialSession(t, h, "admin", "correct")
	if newID == oldID {
		t.Fatal("login did not rotate session")
	}
	if _, found, err := h.store.Load(t.Context(), oldID); err != nil || found {
		t.Fatal("login retained old session")
	}
	record, found, err := h.store.Load(t.Context(), newID)
	if err != nil || !found {
		t.Fatal("login did not publish rotated session")
	}
	stamp, _ := record.Value("_godj_credential_stamp")
	id, _ := record.Value("_godj_principal_id")
	if id != "second" || !changed.MatchesSessionStamp(stamp) || initial.MatchesSessionStamp(stamp) {
		t.Fatal("rotated session mixed authentication state")
	}
	response := h.Do(t, http.MethodGet, protectedPath, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatal("rotated session was not usable")
	}
	closeBody(t, response)
}
