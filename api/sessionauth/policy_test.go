package sessionauth_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestCompoundPermissionsUseOneSessionAndCSRFBoundary(t *testing.T) {
	for _, mode := range []string{"first", "alternative", "missing_common", "missing_first", "missing_alternatives", "deny_common", "deny_alternatives", "common_error", "alternative_error", "cancel", "csrf", "anonymous"} {
		t.Run(mode, func(t *testing.T) {
			resolves, active := 0, false
			var checks []auth.Permission
			harness := newAPIAuthHarness(t, func(config *websessionauth.Config) {
				original := config.Authenticator.(fixedAuthenticator).principal
				permissions := slices.DeleteFunc(original.Permissions(), func(permission auth.Permission) bool {
					return mode == "missing_common" && permission == "links.label" || mode == "missing_first" && permission == "articles.view" || mode == "missing_alternatives" && permission != "links.label"
				})
				principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: original.ID(), Active: true, Permissions: permissions})
				if err != nil {
					t.Fatal(err)
				}
				config.Authenticator = requirementsAuthenticator{CredentialAuthenticator: fixedAuthenticator{principal: principal}, resolves: &resolves}
				config.Authorizer = requirementsAuthorizer(func(_ context.Context, _ auth.Principal, permission auth.Permission) (bool, error) {
					if !active {
						return true, nil
					}
					checks = append(checks, permission)
					if mode == "common_error" && permission == "links.label" || mode == "alternative_error" && permission == "articles.view" {
						return true, errors.New("authority failed")
					}
					if mode == "cancel" && permission == "articles.view" {
						return true, context.Canceled
					}
					return !(mode == "deny_common" && permission == "links.label" || mode == "deny_alternatives" && permission != "links.label" || mode == "alternative" && permission == "articles.view"), nil
				})
			})
			// Acquire CSRF without requiring a model grant, including the
			// missing-view cases whose admission is being tested.
			safe := harness.request(t, http.MethodGet, "/api/csrf/", false, nil, "")
			token := safe.Header.Get(websessionauth.DefaultCSRFHeader)
			cookie := namedCookie(t, safe.Cookies(), websessionauth.DefaultCSRFCookieName)
			_ = safe.Body.Close()
			active, resolves, checks = true, 0, nil
			harness.calls.Store(0)
			harness.mutations.Store(0)
			if mode == "csrf" {
				token = ""
			}
			response := harness.request(t, http.MethodPost, "/api/policy/", mode != "anonymous", cookie, token)
			defer response.Body.Close()
			status, count, resolveCount := 403, int64(0), 1
			want := []auth.Permission{"links.label", "articles.view", "links.ticket"}
			switch mode {
			case "first":
				status, count, want = 204, 1, want[:2]
			case "alternative":
				status, count = 204, 1
			case "missing_first":
				status, count, want = 204, 1, []auth.Permission{"links.label", "links.ticket"}
			case "missing_common":
				want = nil
			case "missing_alternatives", "deny_common":
				want = want[:1]
			case "common_error":
				status, want = 500, want[:1]
			case "alternative_error", "cancel":
				status, want = 500, want[:2]
			case "csrf":
				want = nil
			case "anonymous":
				want, resolveCount = nil, 0
			}
			if response.StatusCode != status || resolves != resolveCount || !slices.Equal(checks, want) || harness.calls.Load() != count || harness.mutations.Load() != count || response.Header.Get("WWW-Authenticate") != "" {
				t.Fatal("compound session boundary", response.StatusCode, resolves, checks, harness.calls.Load())
			}
		})
	}
	harness := newAPIAuthHarness(t)
	if handler, err := harness.adapter.RequirePolicy(api.PermissionPolicy{}, func(*web.Request, auth.Principal) (web.Response, error) { return api.NoContent() }); handler != nil || err == nil {
		t.Fatal("zero policy published")
	}
}
