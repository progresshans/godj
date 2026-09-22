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

func (a requirementsAuthenticator) Resolve(ctx context.Context, id string) (auth.Principal, error) {
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
