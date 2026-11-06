package bearerauth

import (
	"context"
	"errors"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"net/http/httptest"
	"testing"
)

func TestRequireAuthenticatedIsExplicitAndPreservesVerifierFailures(t *testing.T) {
	for _, mode := range []string{"ordinary", "inactive", "anonymous", "malformed", "denial", "wrapped_denial", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			calls, verifications := 0, 0
			runtime, err := New(Config{Verifier: verifierFunc(func(context.Context, Token) (auth.Principal, error) {
				verifications++
				switch mode {
				case "denial":
					return auth.Principal{}, auth.ErrInvalidCredentials
				case "wrapped_denial":
					return auth.Principal{}, errors.Join(auth.ErrInvalidCredentials, errors.New("opaque failure"))
				case "unknown":
					return auth.Principal{}, errors.Join(auth.ErrInvalidCredentials, &query.Error{Code: query.CodeCommitOutcomeUnknown})
				}
				return auth.NewPrincipal(auth.PrincipalConfig{ID: "ordinary", Active: mode != "inactive"})
			}), Authorizer: authorizerFunc(func(context.Context, auth.Principal, auth.Permission) (bool, error) {
				t.Fatal("auth-only route invoked model authorization")
				return false, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			handler := func(_ *web.Request, p auth.Principal) (web.Response, error) {
				if len(p.Permissions()) != 0 || p.Staff() {
					t.Fatal("not ordinary")
				}
				calls++
				return api.NoContent()
			}
			if h, e := runtime.Require("", handler); h != nil || e == nil {
				t.Fatal("empty permission accepted")
			}
			if h, e := runtime.RequireAuthenticated(nil); h != nil || e == nil {
				t.Fatal("nil handler accepted")
			}
			protected, err := runtime.RequireAuthenticated(handler)
			if err != nil {
				t.Fatal(err)
			}
			config, err := settings.New(settings.Definition{ProjectName: "onlyauth", InstalledApps: []apps.Config{{Name: "example.test/onlyauth", Label: "onlyauth"}}})
			if err != nil {
				t.Fatal(err)
			}
			application, err := web.NewApplication(web.Config{Settings: config, Routes: []web.Route{{Name: "onlyauth:current", Method: "POST", Path: "/", Handler: protected}}})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "http://example.test/", nil)
			if mode != "anonymous" {
				request.Header.Set("Authorization", "Bearer abc")
			}
			if mode == "malformed" {
				request.Header.Add("Authorization", "Bearer def")
			}
			response := serve(t, application, request)
			defer response.Body.Close()
			want, count, verified := 401, 0, 1
			switch mode {
			case "ordinary":
				want, count = 204, 1
			case "anonymous":
				verified = 0
			case "malformed":
				want, verified = 400, 0
			case "wrapped_denial", "unknown":
				want = 500
			}
			if response.StatusCode != want || calls != count || verifications != verified {
				t.Fatal("auth-only boundary", response.StatusCode, calls, verifications)
			}
			if (want == 401 || want == 400) && response.Header.Get("WWW-Authenticate") == "" {
				t.Fatal("challenge missing")
			}
		})
	}
}
