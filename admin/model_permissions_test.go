package admin

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
)

func TestModelAdditionalPermissionsGuardEveryDirectOperation(t *testing.T) {
	config := validRegistryConfig(t)
	additional := []auth.Permission{"catalog.manage"}
	config.AdditionalPermissions = additional
	calls := 0
	config.List = func(_ context.Context, _ auth.Principal, request ListRequest) (Page[registryArticle], error) {
		calls++
		return Page[registryArticle]{Offset: request.Offset, Limit: request.Limit}, nil
	}
	config.Get = func(context.Context, auth.Principal, int64) (registryArticle, bool, error) {
		calls++
		return registryArticle{}, false, nil
	}
	config.Create = func(context.Context, auth.Principal, formmodel.BoundForm, InlineSubmission) (MutationResult, error) {
		calls++
		return MutationResult{}, nil
	}
	config.Update = func(context.Context, auth.Principal, Mutation, formmodel.BoundForm, InlineSubmission) (MutationResult, []string, error) {
		calls++
		return MutationResult{}, nil, nil
	}
	config.Delete = func(context.Context, auth.Principal, Mutation) (MutationResult, error) {
		calls++
		return MutationResult{}, nil
	}
	config.History = func(context.Context, auth.Principal, int64, HistoryRequest) ([]AuditEntry, error) {
		calls++
		return nil, nil
	}
	config.Actions[0].Run = func(context.Context, auth.Principal, []int64) (ActionResult, error) {
		calls++
		return ActionResult{}, nil
	}
	config.Commands = []CommandConfig{{Name: "change", Label: "Change", Permission: "articles.change", Form: managementCommandForm(t), Run: func(context.Context, auth.Principal, Mutation, forms.Values) (MutationResult, error) {
		calls++
		return MutationResult{}, nil
	}}}
	config.CollectionCommands = []CollectionCommandConfig{{Name: "ensure", Label: "Ensure", Permission: "articles.add", Form: managementCommandForm(t), Run: func(context.Context, auth.Principal, forms.Values) (CollectionCommandResult, error) {
		calls++
		return CollectionCommandResult{}, nil
	}}}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	additional[0] = "forged.permission"
	descriptor := registry.All()[0]
	if !slices.Equal(descriptor.AdditionalPermissions, []auth.Permission{"catalog.manage"}) || !slices.Contains(descriptor.AddPermissions, "catalog.manage") {
		t.Fatal("model policy lost conjunction")
	}
	descriptor.AdditionalPermissions[0] = "forged.permission"
	if registry.All()[0].AdditionalPermissions[0] != "catalog.manage" {
		t.Fatal("model descriptor aliases owned policy")
	}
	model, actor := registry.models[0], mustPrincipal(t)
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"list", func() error { _, err := model.list(t.Context(), actor, ListRequest{}); return err }},
		{"get", func() error { _, _, err := model.get(t.Context(), actor, 1); return err }},
		{"history", func() error { _, err := model.history(t.Context(), actor, 1); return err }},
		{"create", func() error { _, err := model.create(t.Context(), actor, forms.Form{}, InlineSubmission{}); return err }},
		{"update", func() error {
			_, _, err := model.update(t.Context(), actor, Mutation{ID: 1}, forms.Form{}, InlineSubmission{})
			return err
		}},
		{"delete", func() error { _, err := model.delete(t.Context(), actor, Mutation{ID: 1}); return err }},
		{"action", func() error { _, err := model.actions[0].run(t.Context(), actor, []int64{1}); return err }},
		{"command", func() error {
			_, err := model.commands[0].run(t.Context(), actor, Mutation{ID: 1}, forms.Form{})
			return err
		}},
		{"collection_command", func() error { _, err := model.collectionCommands[0].run(t.Context(), actor, forms.Form{}); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); errorCode(err) != "denied" || calls != 0 {
				t.Fatal("missing model grant reached adapter or input work", err, calls)
			}
		})
	}
	complete := mustPrincipalWithPermissions(t, "articles.view", "catalog.manage")
	if _, err := model.list(t.Context(), complete, ListRequest{}); err != nil || calls != 1 {
		t.Fatal("complete policy did not reach adapter", err, calls)
	}
}

func TestModelAdditionalPermissionsApplyCurrentOverlayBeforeHTTPCallbacks(t *testing.T) {
	for _, mode := range []string{"denial", "error"} {
		t.Run(mode, func(t *testing.T) {
			deny := false
			authorizer := managementFormAuthorizer(func(ctx context.Context, actor auth.Principal, permission auth.Permission) (bool, error) {
				if deny && permission == "accounts.delete" {
					if mode == "error" {
						return false, errors.New("private model policy failure")
					}
					return false, nil
				}
				return (auth.PrincipalAuthorizer{}).Allowed(ctx, actor, permission)
			})
			client, state, _ := newManagementFormSite(t, authorizer, func(config *ModelConfig[managementFormRow]) {
				config.Permissions = Permissions{View: "accounts.view", Add: "accounts.view", Change: "accounts.view", Delete: "accounts.view"}
				config.AdditionalPermissions = []auth.Permission{"accounts.delete"}
				config.History = func(context.Context, auth.Principal, int64, HistoryRequest) ([]AuditEntry, error) {
					t.Fatal("denied history callback")
					return nil, nil
				}
				config.Actions = []ActionConfig{{Name: "close", Label: "Close", Permission: "accounts.change", Run: func(context.Context, auth.Principal, []int64) (ActionResult, error) {
					t.Fatal("denied action callback")
					return ActionResult{}, nil
				}}}
				config.Commands = []CommandConfig{{Name: "reset", Label: "Reset", Permission: "accounts.change", Form: managementCommandForm(t), Run: func(context.Context, auth.Principal, Mutation, forms.Values) (MutationResult, error) {
					t.Fatal("denied command callback")
					return MutationResult{}, nil
				}}}
				config.CollectionCommands = []CollectionCommandConfig{{Name: "ensure", Label: "Ensure", Permission: "accounts.add", Form: managementCommandForm(t), Run: func(context.Context, auth.Principal, forms.Values) (CollectionCommandResult, error) {
					t.Fatal("denied collection callback")
					return CollectionCommandResult{}, nil
				}}}
			})
			client.login(t, "admin", "secret", "/admin/accounts/")
			page := client.do("GET", "/admin/accounts/", nil)
			if page.Code != 200 {
				t.Fatal(page.Code)
			}
			csrf, before := siteCSRFToken(t, page.Body.String()), state.reads
			deny = true
			want := 403
			if mode == "error" {
				want = 500
			}
			for _, path := range []string{"/admin/accounts/", "/admin/accounts/add/", "/admin/accounts/change/?id=1", "/admin/accounts/delete/?id=1", "/admin/accounts/history/?id=1", "/admin/accounts/command/reset/?id=1", "/admin/accounts/collection/ensure/"} {
				response := client.do("GET", path, nil)
				if response.Code != want || strings.Contains(response.Body.String(), "private") {
					t.Fatal("current model overlay failed", path, response.Code)
				}
			}
			for _, path := range []string{"/admin/accounts/add/", "/admin/accounts/change/?id=1", "/admin/accounts/delete/?id=1", "/admin/accounts/action/close/", "/admin/accounts/command/reset/?id=1", "/admin/accounts/collection/ensure/"} {
				data := url.Values{"csrfmiddlewaretoken": {csrf}}
				if strings.Contains(path, "/delete/") {
					data.Set("confirm", "yes")
				}
				if strings.Contains(path, "/action/") {
					data.Set("selected", "1")
				}
				response := client.do("POST", path, data)
				if response.Code != want || strings.Contains(response.Body.String(), "private") {
					t.Fatal("write bypassed current model overlay", path, response.Code)
				}
			}
			if state.reads != before || state.writes != 0 {
				t.Fatal("denied model exposed rows or writes")
			}
			index := client.do("GET", "/admin/", nil)
			if mode == "denial" && (index.Code != 200 || strings.Contains(index.Body.String(), "/admin/accounts/")) {
				t.Fatal("denied model remained visible")
			}
			if mode == "error" && index.Code != 500 {
				t.Fatal("authorization error treated as invisible model")
			}
		})
	}
}

func TestModelAdditionalPermissionsRejectInvalidDeclarations(t *testing.T) {
	for _, additional := range [][]auth.Permission{{""}, {"invalid permission"}, {"catalog.manage", "catalog.manage"}, {"articles.view"}, make([]auth.Permission, auth.MaximumPermissions)} {
		config := validRegistryConfig(t)
		config.AdditionalPermissions = additional
		if err := RegisterModel(NewBuilder(mustApps(t)), config); err == nil {
			t.Fatal("invalid model conjunction registered")
		}
	}
}
