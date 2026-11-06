package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/validation"
)

func TestCreateValidationKeepsFieldErrorsAndExecutionFailures(t *testing.T) {
	for _, mode := range []string{"combined", "nil", "storage", "wrapped", "unknown_field", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client, state, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
				config.ValidateCreate = func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
					form := bound.Form()
					calls++
					if !form.Bound() || form.Valid() || form.Errors().ByField("password2").Len() != 1 || actor.ID() != "manager" {
						t.Fatal("post-clean check lost invalid form/current actor")
					}
					rejected := validation.Reject(validation.NewErrors(validation.New("username", "unique")), nil)
					switch mode {
					case "nil":
						return nil
					case "storage":
						return errors.Join(rejected, errors.New("private-storage-failure"))
					case "wrapped":
						return fmt.Errorf("private-wrapper: %w", rejected)
					case "unknown_field":
						return validation.Reject(validation.NewErrors(validation.New("hidden", "invalid")), nil)
					case "canceled":
						return errors.Join(rejected, context.Canceled)
					default:
						return rejected
					}
				}
			})
			client.login(t, "admin", "secret", "/admin/accounts/")
			get := client.do("GET", "/admin/accounts/add/", nil)
			values := url.Values{"username": {"Candidate"}, "password1": {"private-first"}, "password2": {"private-second"}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
			response := client.do("POST", "/admin/accounts/add/", values)
			want := 500
			if mode == "combined" || mode == "nil" {
				want = 200
			}
			if response.Code != want || calls != 1 || state.writes != 0 {
				t.Fatal("post-clean result or write count", response.Code, calls, state.writes)
			}
			if want == 200 && !strings.Contains(response.Body.String(), `data-error-code="password_mismatch"`) {
				t.Fatal("post-clean erased original error")
			}
			if mode == "combined" && !strings.Contains(response.Body.String(), `data-error-code="unique"`) {
				t.Fatal("post-clean error missing")
			}
			if strings.Contains(response.Body.String(), "private-") {
				t.Fatal("private input or execution detail leaked")
			}
		})
	}
}

func TestCreateValidationRunsAfterCSRFAndAllAddPermissions(t *testing.T) {
	calls := 0
	client, state, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
		config.ValidateCreate = func(context.Context, auth.Principal, formmodel.BoundForm) error { calls++; return nil }
	})
	client.login(t, "admin", "secret", "/admin/accounts/")
	values := url.Values{"username": {"Candidate"}, "password1": {"same"}, "password2": {"same"}}
	if response := client.do("POST", "/admin/accounts/add/", values); response.Code != 403 || calls != 0 {
		t.Fatal("CSRF bypass reached validator")
	}
	client = newSiteHTTPClient(client.application)
	client.login(t, "creator", "secret", "/admin/")
	index := client.do("GET", "/admin/", nil)
	values.Set("csrfmiddlewaretoken", siteCSRFToken(t, index.Body.String()))
	if response := client.do("POST", "/admin/accounts/add/", values); response.Code != 403 || calls != 0 || state.writes != 0 {
		t.Fatal("partial add authority reached validator")
	}
}
