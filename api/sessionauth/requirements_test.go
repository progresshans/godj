package sessionauth_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type requirementsAuthenticator struct {
	auth.CredentialAuthenticator
	resolves *int
}

func (a requirementsAuthenticator) Resolve(ctx context.Context, id string) (auth.Credential, error) {
	*a.resolves++
	return a.CredentialAuthenticator.Resolve(ctx, id)
}

type requirementsAuthorizer func(context.Context, auth.Principal, auth.Permission) (bool, error)

func (a requirementsAuthorizer) Allowed(ctx context.Context, p auth.Principal, permission auth.Permission) (bool, error) {
	return a(ctx, p, permission)
}

func TestRequireAllPermissionsUsesOneSessionAndCSRFBoundary(t *testing.T) {
	for _, mode := range []string{"accepted", "missing_ticket", "missing_label", "deny_ticket", "deny_label", "error_label", "csrf"} {
		t.Run(mode, func(t *testing.T) {
			resolves := 0
			var checks []auth.Permission
			harness := newAPIAuthHarness(t, func(config *websessionauth.Config) {
				if mode == "missing_ticket" || mode == "missing_label" {
					missing := auth.Permission("links.ticket")
					if mode == "missing_label" {
						missing = "links.label"
					}
					principal := config.Authenticator.(fixedAuthenticator).principal
					permissions := slices.DeleteFunc(principal.Permissions(), func(value auth.Permission) bool { return value == missing })
					principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principal.ID(), Active: true, Permissions: permissions})
					if err != nil {
						t.Fatal(err)
					}
					config.Authenticator = fixedAuthenticator{principal: principal}
				}
				config.Authenticator = requirementsAuthenticator{CredentialAuthenticator: config.Authenticator, resolves: &resolves}
				config.Authorizer = requirementsAuthorizer(func(_ context.Context, _ auth.Principal, permission auth.Permission) (bool, error) {
					checks = append(checks, permission)
					if mode == "error_label" && permission == "links.label" {
						return false, errors.New("injected authorizer failure")
					}
					return !(mode == "deny_ticket" && permission == "links.ticket" || mode == "deny_label" && permission == "links.label"), nil
				})
			})
			safe := harness.request(t, http.MethodGet, "/api/articles/", true, nil, "")
			token := safe.Header.Get(websessionauth.DefaultCSRFHeader)
			cookie := namedCookie(t, safe.Cookies(), websessionauth.DefaultCSRFCookieName)
			_ = safe.Body.Close()
			resolves = 0
			checks = nil
			harness.calls.Store(0)
			if mode == "csrf" {
				token = ""
			}
			response := harness.request(t, http.MethodPost, "/api/all/", true, cookie, token)
			defer response.Body.Close()
			status := http.StatusForbidden
			if mode == "accepted" {
				status = http.StatusNoContent
			}
			if mode == "error_label" {
				status = http.StatusInternalServerError
			}
			if response.StatusCode != status || resolves != 1 {
				t.Fatal("permission/credential boundary", response.StatusCode, resolves)
			}
			expected := []auth.Permission{"articles.view", "links.ticket", "links.label"}
			switch mode {
			case "csrf":
				expected = nil
			case "missing_ticket":
				expected = expected[:1]
			case "missing_label", "deny_ticket":
				expected = expected[:2]
			}
			if !slices.Equal(checks, expected) {
				t.Fatal("authorization order/count", checks)
			}
			count := int64(0)
			if mode == "accepted" {
				count = 1
			}
			if harness.calls.Load() != count || harness.mutations.Load() != count {
				t.Fatal("denied application handler ran")
			}
		})
	}
}

func TestRequireRejectsInvalidAdditionalSessionPermissions(t *testing.T) {
	harness := newAPIAuthHarness(t)
	for _, additional := range [][]auth.Permission{{""}, {"Links.View"}, {"articles.view"}, {"links.ticket", "links.ticket"}} {
		handler, err := harness.adapter.Require("articles.view", func(*web.Request, auth.Principal) (web.Response, error) { return api.NoContent() }, additional...)
		if handler != nil || !errors.Is(err, &apisessionauth.Error{Code: apisessionauth.CodeInvalidConfig, Field: "permission"}) {
			t.Fatal("invalid requirement publication", err)
		}
	}
}

func TestRequireAnyUsesOneSessionBoundaryAndNeverMasksErrors(t *testing.T) {
	for _, mode := range []string{"first", "alternative", "denied", "error", "cancel", "csrf", "anonymous"} {
		t.Run(mode, func(t *testing.T) {
			resolves := 0
			active := false
			var checks []auth.Permission
			harness := newAPIAuthHarness(t, func(config *websessionauth.Config) {
				config.Authenticator = requirementsAuthenticator{CredentialAuthenticator: config.Authenticator, resolves: &resolves}
				config.Authorizer = requirementsAuthorizer(func(_ context.Context, _ auth.Principal, p auth.Permission) (bool, error) {
					if !active {
						return true, nil
					}
					checks = append(checks, p)
					if mode == "error" && p == "articles.view" {
						return false, errors.New("authorization failed")
					}
					if mode == "cancel" && p == "articles.view" {
						return false, context.Canceled
					}
					return mode == "first" || p == "links.ticket" && mode == "alternative", nil
				})
			})
			safe := harness.request(t, http.MethodGet, "/api/articles/", true, nil, "")
			token := safe.Header.Get(websessionauth.DefaultCSRFHeader)
			cookie := namedCookie(t, safe.Cookies(), websessionauth.DefaultCSRFCookieName)
			safe.Body.Close()
			active = true
			resolves = 0
			checks = nil
			harness.calls.Store(0)
			harness.mutations.Store(0)
			if mode == "csrf" {
				token = ""
			}
			response := harness.request(t, http.MethodPost, "/api/any/", mode != "anonymous", cookie, token)
			defer response.Body.Close()
			expected := http.StatusForbidden
			var calls int64
			expectedChecks := []auth.Permission{"articles.view", "links.ticket"}
			expectedResolves := 1
			switch mode {
			case "first":
				expected = 204
				calls = 1
				expectedChecks = expectedChecks[:1]
			case "alternative":
				expected = 204
				calls = 1
			case "error", "cancel":
				expected = 500
				expectedChecks = expectedChecks[:1]
			case "csrf":
				expectedChecks = nil
			case "anonymous":
				expectedChecks = nil
				expectedResolves = 0
			}
			if response.StatusCode != expected || harness.calls.Load() != calls || harness.mutations.Load() != calls || resolves != expectedResolves || !slices.Equal(checks, expectedChecks) {
				t.Fatal("alternative boundary", response.StatusCode, resolves, checks, harness.calls.Load())
			}
		})
	}
	harness := newAPIAuthHarness(t)
	for _, values := range [][]auth.Permission{{""}, {"articles.view"}, {"Bad.View"}, {"links.ticket", "links.ticket"}} {
		handler, err := harness.adapter.RequireAny("articles.view", func(*web.Request, auth.Principal) (web.Response, error) { return api.NoContent() }, values...)
		if handler != nil || err == nil {
			t.Fatal("invalid alternative published")
		}
	}
}
