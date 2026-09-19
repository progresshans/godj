package sessionauth_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestDescribeAuthenticationUsesNormalizedNamesWithoutRuntimeWork(t *testing.T) {
	t.Parallel()
	for _, custom := range []bool{false, true} {
		name := "defaults"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			store, err := sessions.NewMemoryStore(1)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := sessions.NewManager(store, sessions.Config{})
			if err != nil {
				t.Fatal(err)
			}
			entropy := bytes.NewReader(bytes.Repeat([]byte{1}, 64))
			config := websessionauth.Config{
				Sessions: manager, Authenticator: descriptionAuthenticator{t}, Authorizer: descriptionAuthorizer{t},
				Random: entropy, Clock: func() time.Time { t.Fatal("description called the clock"); return time.Time{} },
			}
			want := api.AuthenticationDescription{
				Kind: api.AuthenticationSession, SessionCookieName: websessionauth.DefaultSessionCookieName,
				CSRFCookieName: websessionauth.DefaultCSRFCookieName, CSRFHeader: http.CanonicalHeaderKey(websessionauth.DefaultCSRFHeader),
			}
			if custom {
				config.SessionCookie.Name = "app_session"
				config.CSRFCookie.Name = "app_csrf"
				config.CSRFHeader = "x-app-csrf"
				want.SessionCookieName, want.CSRFCookieName, want.CSRFHeader = "app_session", "app_csrf", "X-App-Csrf"
			}
			runtime, err := websessionauth.New(config)
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := apisessionauth.New(runtime)
			if err != nil {
				t.Fatal(err)
			}
			remaining := entropy.Len()
			for range 2 {
				got, err := adapter.DescribeAuthentication()
				if err != nil || got != want {
					t.Fatalf("description = %#v, %v; want %#v", got, err, want)
				}
				got.SessionCookieName = "caller mutation"
			}
			if entropy.Len() != remaining {
				t.Fatal("description consumed entropy")
			}
		})
	}
}

func TestDescribeAuthenticationRejectsAbsentSessionRuntime(t *testing.T) {
	t.Parallel()
	for _, runtime := range []*apisessionauth.Runtime{nil, {}} {
		if _, err := runtime.DescribeAuthentication(); !errors.Is(err, &apisessionauth.Error{Code: apisessionauth.CodeInvalidConfig, Field: "runtime"}) {
			t.Fatalf("description error = %v", err)
		}
	}
}

type descriptionAuthenticator struct{ t *testing.T }

func (a descriptionAuthenticator) Authenticate(context.Context, string, string) (auth.Principal, error) {
	a.t.Fatal("description authenticated credentials")
	return auth.Principal{}, nil
}

func (a descriptionAuthenticator) Resolve(context.Context, string) (auth.Principal, error) {
	a.t.Fatal("description resolved a principal")
	return auth.Principal{}, nil
}

type descriptionAuthorizer struct{ t *testing.T }

func (a descriptionAuthorizer) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	a.t.Fatal("description authorized a principal")
	return false, nil
}
