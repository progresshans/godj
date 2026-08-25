package sessionauth_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func TestLogoutFlushesEveryCanonicalSessionFromAmbiguousCookieHeader(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "logout.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	backend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatal(err)
	}
	backendOpen := true
	t.Cleanup(func() {
		if backendOpen {
			_ = backend.Close()
		}
	})

	loaded, _, err := migrationdefinition.Load(systemstate.InitialDefinitionSource())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations: 10_000,
		Random:     bytes.NewReader(bytes.Repeat([]byte{0x41}, 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := systemstate.BootstrapConfig{
		Username:       "logout-admin",
		Password:       "logout-password-marker",
		PrincipalID:    "logout-principal",
		Active:         true,
		PasswordHasher: hasher,
		SessionLimits:  sessions.DefaultLimits(),
		MaxSessions:    8,
		AuditCapacity:  8,
	}
	durable, err := systemstate.Open(ctx, backend, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Round(0).UTC()
	manager, err := sessions.NewManager(durable.SessionStore(), sessions.Config{
		AbsoluteLifetime: 2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Limits:           bootstrap.SessionLimits,
		Clock:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(ctx, map[string]string{"owner": "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(ctx, map[string]string{"owner": "second"})
	if err != nil {
		t.Fatal(err)
	}

	runtime, err := sessionauth.New(sessionauth.Config{
		Sessions:      manager,
		Authenticator: durable.Authenticator(),
		Authorizer:    auth.PrincipalAuthorizer{},
		SessionCookie: sessionauth.CookieConfig{AllowInsecure: true},
		CSRFCookie:    sessionauth.CookieConfig{AllowInsecure: true},
		Clock:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "sessionauth_logout_test",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/web/sessionauth_test/logout",
			Label: "logout",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{{
			Name:   "logout:flush",
			Method: http.MethodPost,
			Path:   "/logout/",
			Handler: func(request *web.Request) (web.Response, error) {
				change, err := runtime.Logout(request)
				if err != nil {
					return web.Response{}, err
				}
				response, err := web.NewResponse(http.StatusNoContent, make(http.Header), nil)
				if err != nil {
					return web.Response{}, err
				}
				return change.Apply(response)
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ambiguous := strings.Join([]string{
		sessionauth.DefaultSessionCookieName + "=" + first.ID().Encoded(),
		sessionauth.DefaultSessionCookieName + "=forged",
		sessionauth.DefaultSessionCookieName + "=" + second.ID().Encoded(),
		sessionauth.DefaultSessionCookieName + "=" + first.ID().Encoded(),
	}, "; ")
	serveLogout := func(cookieHeader string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "http://example.test/logout/", nil)
		request.Header.Set("Cookie", cookieHeader)
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("Logout status = %d, want %d", response.Code, http.StatusNoContent)
		}
		deletions := make(map[string]bool)
		for _, cookie := range response.Result().Cookies() {
			if cookie.MaxAge < 0 {
				deletions[cookie.Name] = true
			}
		}
		if !deletions[sessionauth.DefaultSessionCookieName] || !deletions[sessionauth.DefaultCSRFCookieName] {
			t.Fatalf("Logout deletion cookies = %#v", deletions)
		}
		if body := response.Body.String(); strings.Contains(body, first.ID().Encoded()) || strings.Contains(body, second.ID().Encoded()) {
			t.Fatal("Logout response exposed a raw session bearer")
		}
		return response
	}

	serveLogout(ambiguous)
	for _, record := range []sessions.Record{first, second} {
		if _, found, err := durable.SessionStore().Load(ctx, record.ID()); err != nil || found {
			t.Fatalf("durable session remained after ambiguous logout: found=%v err=%v", found, err)
		}
	}
	// Replaying the same ambiguous logout remains a successful idempotent delete.
	serveLogout(ambiguous)

	overLimit, err := manager.Create(ctx, map[string]string{"owner": "over-limit"})
	if err != nil {
		t.Fatal(err)
	}
	overLimitHeader := sessionauth.DefaultSessionCookieName + "=" + overLimit.ID().Encoded() + "; padding=" +
		strings.Repeat("x", sessionauth.DefaultLimits().MaxCookieHeaderBytes)
	serveLogout(overLimitHeader)
	if _, found, err := durable.SessionStore().Load(ctx, overLimit.ID()); err != nil || !found {
		t.Fatalf("over-limit cookie header selected a session: found=%v err=%v", found, err)
	}

	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backendOpen = false
	reopened, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, err := systemstate.Open(ctx, reopened, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []sessions.Record{first, second} {
		if _, found, err := restarted.SessionStore().Load(ctx, record.ID()); err != nil || found {
			t.Fatalf("flushed session survived durable reopen: found=%v err=%v", found, err)
		}
	}
	if _, found, err := restarted.SessionStore().Load(ctx, overLimit.ID()); err != nil || !found {
		t.Fatalf("unselected over-limit session did not survive durable reopen: found=%v err=%v", found, err)
	}
}
