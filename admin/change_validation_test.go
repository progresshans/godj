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

func TestChangeValidationKeepsCurrentCandidateAndFieldErrors(t *testing.T) {
	for _, mode := range []string{"combined", "nil", "storage", "wrapped", "unknown_field", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client, state, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
				config.ValidateChange = func(ctx context.Context, actor auth.Principal, mutation Mutation, bound formmodel.BoundForm) error {
					calls++
					name, _ := bound.Candidate().String("username")
					key, _ := bound.Candidate().Integer("id")
					revision, _ := bound.Candidate().Integer("revision")
					if !bound.Form().Bound() || bound.Form().Valid() || name != "Original" || key != 1 || revision != 2 || mutation.ID != 1 || mutation.Revision != 2 || actor.ID() != "manager" || bound.Form().Errors().ByField("username").Len() != 1 {
						t.Fatal("read check lost current candidate or invalid field")
					}
					if _, present := bound.Form().Initial().Get("revision"); present {
						t.Fatal("model-only revision entered rendered initial")
					}
					rejected := validation.Reject(validation.NewErrors(validation.New("active", "model_policy")), nil)
					switch mode {
					case "nil":
						return nil
					case "storage":
						return errors.Join(rejected, errors.New("private-read-failure"))
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
			state.row.revision = 2
			client.login(t, "admin", "secret", "/admin/accounts/")
			get := client.do("GET", "/admin/accounts/change/?id=1", nil)
			if get.Code != 200 || calls != 0 {
				t.Fatal("read checks ran on GET")
			}
			response := client.do("POST", "/admin/accounts/change/?id=1", url.Values{"username": {""}, "active": {"on"}, "expected_revision": {"2"}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}})
			want := 500
			if mode == "combined" || mode == "nil" {
				want = 200
			}
			if response.Code != want || calls != 1 || state.writes != 0 {
				t.Fatal("change validation outcome", response.Code, calls, state.writes)
			}
			if want == 200 && !strings.Contains(response.Body.String(), `data-error-code="required"`) {
				t.Fatal("initial field error lost")
			}
			if mode == "combined" && !strings.Contains(response.Body.String(), `data-error-code="model_policy"`) {
				t.Fatal("new model diagnostic lost")
			}
			if strings.Contains(response.Body.String(), "private-") {
				t.Fatal("private execution detail rendered")
			}
		})
	}
}

func TestChangeValidationRunsAfterCSRFPermissionAndObservedRevision(t *testing.T) {
	calls := 0
	client, state, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
		config.ValidateChange = func(context.Context, auth.Principal, Mutation, formmodel.BoundForm) error { calls++; return nil }
	})
	client.login(t, "admin", "secret", "/admin/accounts/")
	data := url.Values{"username": {"Candidate"}, "active": {"on"}, "expected_revision": {"1"}}
	if response := client.do("POST", "/admin/accounts/change/?id=1", data); response.Code != 403 || calls != 0 {
		t.Fatal("CSRF bypass reached check")
	}
	get := client.do("GET", "/admin/accounts/change/?id=1", nil)
	data.Set("csrfmiddlewaretoken", siteCSRFToken(t, get.Body.String()))
	state.row.revision = 2
	if response := client.do("POST", "/admin/accounts/change/?id=1", data); response.Code != 409 || calls != 0 {
		t.Fatal("stale revision reached check")
	}
	viewer := newSiteHTTPClient(client.application)
	viewer.login(t, "viewer", "secret", "/admin/")
	index := viewer.do("GET", "/admin/", nil)
	data.Set("csrfmiddlewaretoken", siteCSRFToken(t, index.Body.String()))
	data.Set("expected_revision", "2")
	if response := viewer.do("POST", "/admin/accounts/change/?id=1", data); response.Code != 403 || calls != 0 || state.writes != 0 {
		t.Fatal("view authority reached change check")
	}
}
