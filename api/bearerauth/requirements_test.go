package bearerauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

func TestRequireAllPermissionsVerifiesBearerOnceAndPreservesDenyOverlay(t *testing.T) {
	for _, mode := range []string{"accepted", "missing_ticket", "missing_label", "deny_ticket", "deny_label", "error_label", "cancel_label"} {
		t.Run(mode, func(t *testing.T) {
			permissions := []auth.Permission{"links.add", "links.ticket", "links.label"}
			if mode == "missing_ticket" {
				permissions = []auth.Permission{"links.add", "links.label"}
			}
			if mode == "missing_label" {
				permissions = permissions[:2]
			}
			verifier := &recordingVerifier{principal: mustPrincipal(t, permissions...)}
			var checks []auth.Permission
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runtime, err := New(Config{Verifier: verifier, Authorizer: authorizerFunc(func(_ context.Context, _ auth.Principal, permission auth.Permission) (bool, error) {
				checks = append(checks, permission)
				if mode == "error_label" && permission == "links.label" {
					return false, errors.New("injected authorizer failure")
				}
				if mode == "cancel_label" && permission == "links.label" {
					cancel()
				}
				return !(mode == "deny_ticket" && permission == "links.ticket" || mode == "deny_label" && permission == "links.label"), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			additional := []auth.Permission{"links.ticket", "links.label"}
			application, _ := protectedApplication(t, runtime, "links.add", func(*web.Request, auth.Principal) (web.Response, error) { calls++; return api.NoContent() }, additional...)
			additional[0] = "links.changed_after_binding"
			request := httptest.NewRequest(http.MethodPost, "http://example.test/api/test/", nil).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer a")
			response := serve(t, application, request)
			defer response.Body.Close()
			expectedStatus := http.StatusForbidden
			if mode == "accepted" {
				expectedStatus = http.StatusNoContent
			}
			if mode == "error_label" || mode == "cancel_label" {
				expectedStatus = http.StatusInternalServerError
			}
			if response.StatusCode != expectedStatus || verifier.calls.Load() != 1 {
				t.Fatal("credential/handler boundary", response.StatusCode, verifier.calls.Load())
			}
			expected := []auth.Permission{"links.add", "links.ticket", "links.label"}
			switch mode {
			case "missing_ticket":
				expected = expected[:1]
			case "missing_label", "deny_ticket":
				expected = expected[:2]
			}
			if !slices.Equal(checks, expected) {
				t.Fatal("deny-overlay order/count", checks)
			}
			count := 0
			if mode == "accepted" {
				count = 1
			}
			if calls != count {
				t.Fatal("denied handler ran")
			}
			if expectedStatus == http.StatusForbidden && response.Header.Get("WWW-Authenticate") != challengeInsufficientScope {
				t.Fatal("missing challenge")
			}
		})
	}
}

func TestRequireRejectsInvalidAdditionalBearerPermissions(t *testing.T) {
	runtime, err := New(Config{Verifier: verifierFunc(func(context.Context, Token) (auth.Principal, error) {
		t.Fatal("verification during construction")
		return auth.Principal{}, nil
	}), Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, additional := range [][]auth.Permission{{""}, {"Links.View"}, {"links.add"}, {"links.ticket", "links.ticket"}} {
		handler, err := runtime.Require("links.add", func(*web.Request, auth.Principal) (web.Response, error) { return api.NoContent() }, additional...)
		if handler != nil || !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "permission"}) {
			t.Fatal("invalid requirement publication", err)
		}
	}
}
