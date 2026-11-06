package bearerauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

func TestCompoundPermissionsVerifyBearerOnceAndFailClosed(t *testing.T) {
	for _, mode := range []string{"first", "alternative", "missing_common", "missing_first", "missing_alternatives", "deny_common", "deny_alternatives", "common_error", "alternative_error", "cancel_common", "cancel_alternative", "anonymous"} {
		t.Run(mode, func(t *testing.T) {
			grants := slices.DeleteFunc([]auth.Permission{"links.scope", "links.view", "links.change"}, func(permission auth.Permission) bool {
				return mode == "missing_common" && permission == "links.scope" || mode == "missing_first" && permission == "links.view" || mode == "missing_alternatives" && permission != "links.scope"
			})
			verifier := &recordingVerifier{principal: mustPrincipal(t, grants...)}
			var checks []auth.Permission
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runtime, err := New(Config{Verifier: verifier, Authorizer: authorizerFunc(func(_ context.Context, _ auth.Principal, permission auth.Permission) (bool, error) {
				checks = append(checks, permission)
				if mode == "common_error" && permission == "links.scope" || mode == "alternative_error" && permission == "links.view" {
					return true, errors.New("authority failed")
				}
				if mode == "cancel_common" && permission == "links.scope" || mode == "cancel_alternative" && permission == "links.view" {
					cancel()
				}
				return !(mode == "deny_common" && permission == "links.scope" || mode == "deny_alternatives" && permission != "links.scope" || mode == "alternative" && permission == "links.view"), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := api.NewPermissionPolicy([]auth.Permission{"links.scope"}, []auth.Permission{"links.view", "links.change"})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			handler, err := runtime.RequirePolicy(policy, func(*web.Request, auth.Principal) (web.Response, error) { calls++; return api.NoContent() })
			if err != nil {
				t.Fatal(err)
			}
			configured, err := settings.New(settings.Definition{ProjectName: "compound", InstalledApps: []apps.Config{{Name: "example.test/bearer", Label: "test"}}})
			if err != nil {
				t.Fatal(err)
			}
			application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "test:policy", Method: http.MethodPost, Path: "/api/test/", Handler: handler}}})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "http://example.test/api/test/", nil).WithContext(ctx)
			if mode != "anonymous" {
				request.Header.Set("Authorization", "Bearer a")
			}
			response := serve(t, application, request)
			defer response.Body.Close()
			status, count, verifications := 403, 0, int64(1)
			want := []auth.Permission{"links.scope", "links.view", "links.change"}
			switch mode {
			case "first":
				status, count, want = 204, 1, want[:2]
			case "alternative":
				status, count = 204, 1
			case "missing_first":
				status, count, want = 204, 1, []auth.Permission{"links.scope", "links.change"}
			case "missing_common":
				want = nil
			case "missing_alternatives", "deny_common":
				want = want[:1]
			case "common_error", "cancel_common":
				status, want = 500, want[:1]
			case "alternative_error", "cancel_alternative":
				status, want = 500, want[:2]
			case "anonymous":
				status, want, verifications = 401, nil, 0
			}
			if response.StatusCode != status || calls != count || verifier.calls.Load() != verifications || !slices.Equal(checks, want) {
				t.Fatal("compound bearer boundary", response.StatusCode, calls, verifier.calls.Load(), checks)
			}
			if status == 403 && response.Header.Get("WWW-Authenticate") != challengeInsufficientScope {
				t.Fatal("denial challenge lost")
			}
		})
	}
	runtime, err := New(Config{Verifier: verifierFunc(func(context.Context, Token) (auth.Principal, error) {
		t.Fatal("construction verifies credentials")
		return auth.Principal{}, nil
	}), Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	if handler, err := runtime.RequirePolicy(api.PermissionPolicy{}, func(*web.Request, auth.Principal) (web.Response, error) { return api.NoContent() }); handler != nil || err == nil {
		t.Fatal("zero policy published")
	}
}
