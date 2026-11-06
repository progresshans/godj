package sessionauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

type inspectAuthenticator struct {
	credential auth.Credential
	err        error
	cancel     context.CancelFunc
}

func (a inspectAuthenticator) Authenticate(context.Context, string, string) (auth.Credential, error) {
	return auth.Credential{}, auth.ErrInvalidCredentials
}
func (a inspectAuthenticator) Resolve(context.Context, string) (auth.Credential, error) {
	if a.cancel != nil {
		a.cancel()
	}
	return a.credential, a.err
}

func TestInspectPrincipalDoesNotTouchOrCleanUpAndPreservesExecutionErrors(t *testing.T) {
	for _, inspect := range []bool{true, false} {
		for _, mode := range []string{"active", "incomplete", "stale", "expired", "inactive", "denial", "wrapped_denial", "unknown_denial", "cancel_denial"} {
			t.Run(map[bool]string{true: "inspect", false: "normal"}[inspect]+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
				store, err := sessions.NewMemoryStore(4)
				if err != nil {
					t.Fatal(err)
				}
				manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return now }, IdleTimeout: time.Hour, AbsoluteLifetime: 24 * time.Hour})
				if err != nil {
					t.Fatal(err)
				}
				principal, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "ordinary", Active: mode != "inactive"})
				credential, _ := auth.NewCredential("ordinary", "private-encoding", principal)
				values := map[string]string{auth.SessionPrincipalIDKey: principal.ID(), auth.SessionCredentialStampKey: credential.SessionStamp()}
				if mode == "incomplete" {
					delete(values, auth.SessionCredentialStampKey)
				}
				if mode == "stale" {
					values[auth.SessionCredentialStampKey] = "stale"
				}
				record, err := manager.Create(t.Context(), values)
				if err != nil {
					t.Fatal(err)
				}
				resolver := inspectAuthenticator{credential: credential}
				switch mode {
				case "denial":
					resolver.err = auth.ErrInvalidCredentials
				case "wrapped_denial":
					resolver.err = errors.Join(auth.ErrInvalidCredentials, errors.New("private failure"))
				case "unknown_denial":
					resolver.err = errors.Join(auth.ErrInvalidCredentials, &query.Error{Code: query.CodeCommitOutcomeUnknown})
				case "cancel_denial":
					resolver.err = auth.ErrInvalidCredentials
					resolver.cancel = cancel
				}
				config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
				config.Sessions = manager
				config.Authenticator = resolver
				runtime, err := New(config)
				if err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Minute)
				if mode == "expired" {
					now = now.Add(2 * time.Hour)
				}
				configured, err := settings.New(settings.Definition{ProjectName: "inspect", InstalledApps: []apps.Config{{Name: "example.test/inspect", Label: "inspect"}}})
				if err != nil {
					t.Fatal(err)
				}
				var observed error
				application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "inspect:current", Method: "GET", Path: "/", Handler: func(r *web.Request) (web.Response, error) {
					resolve := runtime.Principal
					if inspect {
						resolve = runtime.InspectPrincipal
					}
					p, e := resolve(r)
					observed = e
					if e != nil {
						return web.Response{}, e
					}
					if p.Authenticated() {
						return web.NewResponse(204, nil, nil)
					}
					return web.NewResponse(403, nil, nil)
				}}}})
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest("GET", "http://example.test/", nil).WithContext(ctx)
				request.AddCookie(&http.Cookie{Name: DefaultSessionCookieName, Value: record.ID().Encoded()})
				writer := httptest.NewRecorder()
				application.ServeHTTP(writer, request)
				execution := mode == "wrapped_denial" || mode == "unknown_denial" || mode == "cancel_denial"
				want := 403
				if mode == "active" {
					want = 204
				}
				if execution {
					want = 500
				}
				if writer.Code != want {
					t.Fatal("resolution outcome", writer.Code, want)
				}
				if execution && !errors.Is(observed, auth.ErrInvalidCredentials) {
					t.Fatal("execution cause lost")
				}
				if mode == "cancel_denial" && !errors.Is(observed, context.Canceled) {
					t.Fatal("cancellation hidden")
				}
				stored, found, err := store.Load(t.Context(), record.ID())
				if err != nil {
					t.Fatal(err)
				}
				if inspect {
					if !found || !reflect.DeepEqual(record, stored) {
						t.Fatal("read-only resolution changed stored record")
					}
				} else {
					retained := mode == "active" || execution
					if found != retained {
						t.Fatal("normal resolution cleanup changed", found)
					}
					if mode == "active" && reflect.DeepEqual(record, stored) {
						t.Fatal("normal resolution no longer touches")
					}
				}
			})
		}
	}
}
