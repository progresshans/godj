package admin

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
)

func TestActionRequiresOwnedConjunctionBeforeCallingWriter(t *testing.T) {
	additional := []auth.Permission{"accounts.view", "accounts.delete"}
	calls := 0
	actions, err := prepareActions([]ActionConfig{{Name: "close", Label: "Close selected", Permission: "accounts.change", AdditionalPermissions: additional,
		Run: func(_ context.Context, _ auth.Principal, ids []int64) (ActionResult, error) {
			calls++
			return ActionResult{MatchedIDs: ids}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	additional[0] = "forged.permission"
	for _, permissions := range [][]auth.Permission{{"accounts.change"}, {"accounts.change", "accounts.view"}, {"accounts.view", "accounts.delete"}} {
		if _, err := actions[0].run(t.Context(), mustPrincipalWithPermissions(t, permissions...), []int64{1}); err == nil || calls != 0 {
			t.Fatal("incomplete action permission reached writer", err)
		}
	}
	result, err := actions[0].run(t.Context(), mustPrincipalWithPermissions(t, "accounts.change", "accounts.view", "accounts.delete"), []int64{2, 1, 2})
	if err != nil || calls != 1 || !slices.Equal(result.MatchedIDs, []int64{1, 2}) {
		t.Fatal("action lost canonical selection or owned permissions", result, err)
	}
	for _, extra := range [][]auth.Permission{{""}, {"accounts.change"}, {"accounts.view", "accounts.view"}, {"not canonical"}, make([]auth.Permission, auth.MaximumPermissions)} {
		_, err := prepareActions([]ActionConfig{{Name: "close", Label: "Close", Permission: "accounts.change", AdditionalPermissions: extra, Run: func(context.Context, auth.Principal, []int64) (ActionResult, error) {
			t.Fatal("invalid definition called writer")
			return ActionResult{}, nil
		}}})
		if err == nil {
			t.Fatal("invalid permission conjunction registered", extra)
		}
	}
}

func TestActionRendersOnlyDirectConfirmedInputRejection(t *testing.T) {
	for _, mode := range []string{"input", "wrapped", "cleanup_unknown"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client, _, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
				config.Actions = []ActionConfig{{Name: "close", Label: "Close selected", Permission: "accounts.change", Run: func(context.Context, auth.Principal, []int64) (ActionResult, error) {
					calls++
					failure := validation.Reject(validation.NewErrors(validation.New(validation.NonField, "invalid_count")), errors.New("private validation cause"))
					if mode == "wrapped" {
						return ActionResult{}, NewOperationError(OperationOutcomeUnknown, failure)
					}
					if mode == "cleanup_unknown" {
						return ActionResult{}, errors.Join(failure, errors.New("private cleanup failure"))
					}
					return ActionResult{}, failure
				}}}
			})
			client.login(t, "admin", "secret", "/admin/accounts/")
			page := client.do("GET", "/admin/accounts/", nil)
			response := client.do("POST", "/admin/accounts/action/close/", url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "selected": {"1"}})
			status := 400
			if mode == "wrapped" {
				status = 503
			}
			if mode == "cleanup_unknown" {
				status = 500
			}
			if response.Code != status || calls != 1 || strings.Contains(response.Body.String(), "private") {
				t.Fatal("unconfirmed action was treated as input", mode, response.Code, calls)
			}
		})
	}
}

func TestActionPermissionOverlayAppliesToVisibilityAndExecution(t *testing.T) {
	for _, mode := range []string{"allowed", "denied_primary", "denied_additional", "authorizer_error", "csrf"} {
		t.Run(mode, func(t *testing.T) {
			var denied auth.Permission
			failAuthorization := false
			authorizer := managementFormAuthorizer(func(ctx context.Context, p auth.Principal, permission auth.Permission) (bool, error) {
				if permission == denied {
					if failAuthorization {
						return false, errors.New("private action authorizer failure")
					}
					return false, nil
				}
				return (auth.PrincipalAuthorizer{}).Allowed(ctx, p, permission)
			})
			writes := 0
			additional := []auth.Permission{"accounts.delete"}
			client, _, registry := newManagementFormSite(t, authorizer, func(config *ModelConfig[managementFormRow]) {
				config.Actions = []ActionConfig{{Name: "close", Label: "Close selected", Permission: "accounts.change", AdditionalPermissions: additional, Run: func(_ context.Context, _ auth.Principal, ids []int64) (ActionResult, error) {
					writes++
					return ActionResult{MatchedIDs: ids}, nil
				}}}
			})
			additional[0] = "forged.permission"
			descriptor := registry.All()[0].Actions[0]
			if !slices.Equal(descriptor.Permissions, []auth.Permission{"accounts.change", "accounts.delete"}) {
				t.Fatal("action descriptor lost permission conjunction")
			}
			descriptor.Permissions[1] = "forged.permission"
			if registry.All()[0].Actions[0].Permissions[1] != "accounts.delete" {
				t.Fatal("action descriptor aliases registry")
			}
			client.login(t, "admin", "secret", "/admin/accounts/")
			page := client.do("GET", "/admin/accounts/", nil)
			if page.Code != 200 || !strings.Contains(page.Body.String(), "Close selected") {
				t.Fatal("action missing", page.Code)
			}
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "selected": {"1"}}
			switch mode {
			case "denied_primary":
				denied = "accounts.change"
			case "denied_additional", "authorizer_error":
				denied = "accounts.delete"
				failAuthorization = mode == "authorizer_error"
			case "csrf":
				values.Set("csrfmiddlewaretoken", "invalid")
			}
			if denied != "" {
				page = client.do("GET", "/admin/accounts/", nil)
				if mode == "authorizer_error" {
					if page.Code != 500 {
						t.Fatal(page.Code)
					}
				} else if page.Code != 200 || strings.Contains(page.Body.String(), "Close selected") {
					t.Fatal("denied action still visible", page.Code)
				}
			}
			response := client.do("POST", "/admin/accounts/action/close/", values)
			status, wantWrites := 302, 1
			if mode != "allowed" {
				status, wantWrites = 403, 0
			}
			if mode == "authorizer_error" {
				status = 500
			}
			if response.Code != status || writes != wantWrites || strings.Contains(response.Body.String(), "private action") {
				t.Fatal("action admission or error secrecy", response.Code, writes)
			}
			if mode == "allowed" && !siteSignedNoticeLocation(response.Header().Get("Location"), "/admin/accounts/", "action:close", "1") {
				t.Fatal("action result did not produce its signed generic notice")
			}
		})
	}
}
