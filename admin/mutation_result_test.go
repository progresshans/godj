package admin

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
)

func TestRegisteredMutationResultValidatesIdentityRevisionAndChangedFields(t *testing.T) {
	failure := errors.New("private write failure")
	for _, test := range []struct {
		name      string
		operation Action
		versioned bool
		result    MutationResult
		changed   []string
		failure   error
		invalid   bool
	}{
		{"create", ActionAdd, false, MutationResult{ID: 2}, nil, nil, false},
		{"create_zero_id", ActionAdd, false, MutationResult{}, nil, nil, true},
		{"create_negative_id", ActionAdd, false, MutationResult{ID: -1}, nil, nil, true},
		{"create_unexpected_revision", ActionAdd, false, MutationResult{ID: 2, Revision: 1}, nil, nil, true},
		{"create_versioned", ActionAdd, true, MutationResult{ID: 2, Revision: 1}, nil, nil, false},
		{"create_missing_revision", ActionAdd, true, MutationResult{ID: 2}, nil, nil, true},
		{"create_negative_revision", ActionAdd, true, MutationResult{ID: 2, Revision: -1}, nil, nil, true},
		{"create_failed_result", ActionAdd, true, MutationResult{ID: 2, Revision: 1}, nil, failure, false},
		{"update", ActionChange, false, MutationResult{ID: 1}, []string{"username"}, nil, false},
		{"update_wrong_id", ActionChange, false, MutationResult{ID: 2}, []string{"username"}, nil, true},
		{"update_unexpected_revision", ActionChange, false, MutationResult{ID: 1, Revision: 1}, nil, nil, true},
		{"update_versioned", ActionChange, true, MutationResult{ID: 1, Revision: 2}, []string{"username"}, nil, false},
		{"update_last_revision", ActionChange, true, MutationResult{ID: 1, Revision: math.MaxInt64}, []string{"username"}, nil, false},
		{"update_unchanged_revision", ActionChange, true, MutationResult{ID: 1, Revision: 1}, []string{"username"}, nil, true},
		{"update_missing_revision", ActionChange, true, MutationResult{ID: 1}, []string{"username"}, nil, true},
		{"update_noop", ActionChange, true, MutationResult{ID: 1, Revision: 1}, nil, nil, false},
		{"update_noop_advanced_revision", ActionChange, true, MutationResult{ID: 1, Revision: 2}, nil, nil, true},
		{"update_private_changed_field", ActionChange, true, MutationResult{ID: 1, Revision: 2}, []string{"revision"}, nil, true},
		{"update_unknown_changed_field", ActionChange, true, MutationResult{ID: 1, Revision: 2}, []string{"private"}, nil, true},
		{"update_duplicate_changed_field", ActionChange, true, MutationResult{ID: 1, Revision: 2}, []string{"username", "username"}, nil, true},
		{"update_failed_result", ActionChange, true, MutationResult{ID: 1, Revision: 2}, []string{"username"}, failure, false},
		{"delete", ActionDelete, false, MutationResult{ID: 1}, nil, nil, false},
		{"delete_wrong_id", ActionDelete, false, MutationResult{ID: 2}, nil, nil, true},
		{"delete_versioned", ActionDelete, true, MutationResult{ID: 1, Revision: 1}, nil, nil, false},
		{"delete_wrong_revision", ActionDelete, true, MutationResult{ID: 1, Revision: 2}, nil, nil, true},
		{"delete_failed_result", ActionDelete, true, MutationResult{ID: 1, Revision: 1}, nil, failure, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, _, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
				if !test.versioned {
					config.RevisionField = ""
				}
				config.Create = func(context.Context, auth.Principal, formmodel.BoundForm, InlineSubmission) (MutationResult, error) {
					calls++
					return test.result, test.failure
				}
				config.Update = func(context.Context, auth.Principal, Mutation, formmodel.BoundForm, InlineSubmission) (MutationResult, []string, error) {
					calls++
					return test.result, test.changed, test.failure
				}
				config.Delete = func(context.Context, auth.Principal, Mutation) (MutationResult, error) {
					calls++
					return test.result, test.failure
				}
			})
			model := registry.models[0]
			actor := sitePrincipal(t, "manager", true, "accounts.view", "accounts.add", "accounts.change", "accounts.delete")
			mutation := Mutation{ID: 1}
			if test.versioned {
				mutation.Revision = 1
			}
			var result MutationResult
			var changed []string
			var err error
			switch test.operation {
			case ActionAdd:
				form, bindErr := model.createForm.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"Created"}, "password1": {"secret"}, "password2": {"secret"}}), nil)
				if bindErr != nil || !form.Valid() {
					t.Fatal("invalid fixture form", bindErr)
				}
				result, err = model.create(t.Context(), actor, form, InlineSubmission{})
			case ActionChange:
				form, bindErr := model.form.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"Changed"}, "active": {"on"}}), nil)
				if bindErr != nil || !form.Valid() {
					t.Fatal("invalid fixture form", bindErr)
				}
				result, changed, err = model.update(t.Context(), actor, mutation, form, InlineSubmission{})
			case ActionDelete:
				result, err = model.delete(t.Context(), actor, mutation)
			}
			if calls != 1 {
				t.Fatal("callback did not run exactly once", calls, err)
			}
			if test.failure != nil {
				if err != failure || result != (MutationResult{}) || changed != nil {
					t.Fatal("failed write published a result or changed its error", result, changed, err)
				}
			} else if test.invalid {
				if !errors.Is(err, ErrReconciliationRequired) || result != (MutationResult{}) || changed != nil {
					t.Fatal("invalid confirmed metadata did not require reconciliation", result, changed, err)
				}
			} else {
				if err != nil || result != test.result || !reflect.DeepEqual(changed, test.changed) {
					t.Fatal("valid receipt rejected", result, changed, err)
				}
				if len(changed) > 0 {
					changed[0] = "forged"
					if test.changed[0] != "username" {
						t.Fatal("result aliases the callback's changed fields")
					}
				}
			}
		})
	}
}

func TestAdminCRUDKeepsConfirmedSuccessAfterActorLosesReadAccess(t *testing.T) {
	for _, action := range []Action{ActionAdd, ActionChange, ActionDelete} {
		t.Run(string(action), func(t *testing.T) {
			var committed atomic.Bool
			var lateReads atomic.Int64
			authorizer := managementFormAuthorizer(func(ctx context.Context, actor auth.Principal, permission auth.Permission) (bool, error) {
				if committed.Load() {
					return false, nil
				}
				return (auth.PrincipalAuthorizer{}).Allowed(ctx, actor, permission)
			})
			client, state, _ := newManagementFormSite(t, authorizer, func(config *ModelConfig[managementFormRow]) {
				get, snapshot, initial := config.Get, config.Snapshot, config.Initial
				config.Get = func(ctx context.Context, actor auth.Principal, id int64) (managementFormRow, bool, error) {
					if committed.Load() {
						lateReads.Add(1)
						return managementFormRow{}, false, NewOperationError(OperationDenied, nil)
					}
					return get(ctx, actor, id)
				}
				config.Snapshot = func(row managementFormRow) (Object, error) {
					if committed.Load() {
						lateReads.Add(1)
						return Object{}, errors.New("display unavailable after commit")
					}
					return snapshot(row)
				}
				config.Initial = func(row managementFormRow) (map[string]forms.Value, error) {
					if committed.Load() {
						lateReads.Add(1)
						return nil, errors.New("form unavailable after commit")
					}
					return initial(row)
				}
				create, update, remove := config.Create, config.Update, config.Delete
				config.Create = func(ctx context.Context, actor auth.Principal, form formmodel.BoundForm, inlines InlineSubmission) (MutationResult, error) {
					result, err := create(ctx, actor, form, inlines)
					committed.Store(err == nil)
					return result, err
				}
				config.Update = func(ctx context.Context, actor auth.Principal, mutation Mutation, form formmodel.BoundForm, inlines InlineSubmission) (MutationResult, []string, error) {
					result, changed, err := update(ctx, actor, mutation, form, inlines)
					committed.Store(err == nil)
					return result, changed, err
				}
				config.Delete = func(ctx context.Context, actor auth.Principal, mutation Mutation) (MutationResult, error) {
					result, err := remove(ctx, actor, mutation)
					committed.Store(err == nil)
					return result, err
				}
			})
			client.login(t, "admin", "secret", "/admin/")
			path := "/admin/accounts/" + string(action) + "/?id=1"
			data := url.Values{"username": {"Committed"}, "expected_revision": {"1"}}
			switch action {
			case ActionAdd:
				path = "/admin/accounts/add/"
				data = url.Values{"username": {"Created"}, "password1": {"secret"}, "password2": {"secret"}}
			case ActionDelete:
				data = url.Values{"confirm": {"yes"}, "expected_revision": {"1"}}
			}
			page := client.do(http.MethodGet, path, nil)
			if page.Code != http.StatusOK {
				t.Fatal("mutation form unavailable", page.Code)
			}
			data.Set("csrfmiddlewaretoken", siteCSRFToken(t, page.Body.String()))
			response := client.do(http.MethodPost, path, data)
			if response.Code != http.StatusFound || !strings.HasPrefix(response.Header().Get("Location"), "/admin/accounts/?") || !committed.Load() || state.writes != 1 || lateReads.Load() != 0 {
				t.Fatal("confirmed write required display/read authority", response.Code, state.writes, lateReads.Load())
			}
			if next := client.do(http.MethodGet, "/admin/accounts/", nil); next.Code != http.StatusForbidden || state.writes != 1 || lateReads.Load() != 0 {
				t.Fatal("next request reused lost authority or retried the write", next.Code)
			}
		})
	}
}
