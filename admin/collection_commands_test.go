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
	"github.com/progresshans/godj/validation"
)

func TestAdminCollectionCommandWithoutExistingObject(t *testing.T) {
	for _, mode := range []string{"created", "existing", "empty", "duplicate", "unknown_field", "revision", "query", "csrf", "duplicate_csrf", "denied_add", "denied_view", "authorizer_error", "rejected", "rollback_failure", "unknown_outcome", "invalid_result"} {
		t.Run(mode, func(t *testing.T) {
			field, err := forms.CharField("name", forms.WithMaxLength(64))
			if err != nil {
				t.Fatal(err)
			}
			form, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			calls, reads := 0, 0
			var denied auth.Permission
			var authFailure bool
			authorizer := managementFormAuthorizer(func(ctx context.Context, p auth.Principal, permission auth.Permission) (bool, error) {
				if permission == denied {
					if authFailure {
						return false, errors.New("private authorizer cause")
					}
					return false, nil
				}
				return (auth.PrincipalAuthorizer{}).Allowed(ctx, p, permission)
			})
			permissions := []auth.Permission{"accounts.view"}
			client, _, registry := newManagementFormSite(t, authorizer, func(c *ModelConfig[managementFormRow]) {
				c.List = func(_ context.Context, _ auth.Principal, request ListRequest) (Page[managementFormRow], error) {
					reads++
					return Page[managementFormRow]{Offset: request.Offset, Limit: request.Limit}, nil
				}
				c.Get = func(context.Context, auth.Principal, int64) (managementFormRow, bool, error) {
					t.Fatal("collection command required an existing selected object")
					return managementFormRow{}, false, nil
				}
				c.CollectionCommands = []CollectionCommandConfig{{Name: "ensure", Label: "Find or create", Permission: "accounts.add", AdditionalPermissions: permissions, Form: form,
					Run: func(_ context.Context, actor auth.Principal, values forms.Values) (CollectionCommandResult, error) {
						calls++
						if name, ok := values.String("name"); !ok || name != "Name <&>" || actor.ID() != "manager" {
							t.Fatal("command lost normalized input or actor")
						}
						switch mode {
						case "rejected":
							return CollectionCommandResult{}, validation.Reject(validation.NewErrors(validation.New("name", "invalid")), errors.New("private input cause"))
						case "rollback_failure":
							return CollectionCommandResult{}, errors.Join(validation.Reject(validation.NewErrors(validation.New("name", "invalid")), nil), errors.New("private rollback failure"))
						case "unknown_outcome":
							return CollectionCommandResult{}, NewOperationError(OperationOutcomeUnknown, errors.New("private outcome cause"))
						case "invalid_result":
							return CollectionCommandResult{}, nil
						}
						return CollectionCommandResult{ID: 45, Created: mode == "created"}, nil
					},
				}}
			})
			// Caller and descriptor mutation must not change admission.
			permissions[0] = "accounts.delete"
			descriptor := registry.All()[0].CollectionCommands[0]
			if !slices.Equal(descriptor.Permissions, []auth.Permission{"accounts.add", "accounts.view"}) {
				t.Fatal("collection admission was not declared")
			}
			descriptor.Permissions[1] = "accounts.delete"
			descriptor.FormFields[0] = cField(t, "forged")
			client.login(t, "admin", "secret", "/admin/accounts/")
			path := "/admin/accounts/collection/ensure/"
			list := client.do("GET", "/admin/accounts/", nil)
			if list.Code != 200 || !strings.Contains(list.Body.String(), path) || !strings.Contains(list.Body.String(), "No results.") {
				t.Fatal("empty list did not discover collection command")
			}
			before := reads
			get := client.do("GET", path, nil)
			if get.Code != 200 || !strings.Contains(get.Body.String(), `name="name"`) || strings.Contains(get.Body.String(), `name="expected_revision"`) || strings.Contains(get.Body.String(), `name="username"`) || reads != before {
				t.Fatal("collection form inherited model fields, revision or lookup")
			}
			values := url.Values{"name": {"  Name <&>  "}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
			want, wantCalls := 302, 1
			switch mode {
			case "empty":
				values.Set("name", " ")
				want, wantCalls = 200, 0
			case "duplicate":
				values.Add("name", "other")
				want, wantCalls = 200, 0
			case "unknown_field":
				values.Set("id", "1")
				want, wantCalls = 400, 0
			case "revision":
				values.Set("expected_revision", "1")
				want, wantCalls = 400, 0
			case "query":
				path += "?id=1"
				want, wantCalls = 400, 0
			case "csrf":
				values.Del("csrfmiddlewaretoken")
				want, wantCalls = 403, 0
			case "duplicate_csrf":
				values.Add("csrfmiddlewaretoken", values.Get("csrfmiddlewaretoken"))
				want, wantCalls = 403, 0
			case "denied_add", "denied_view", "authorizer_error":
				denied = "accounts.view"
				if mode == "denied_add" {
					denied = "accounts.add"
				}
				authFailure = mode == "authorizer_error"
				want, wantCalls = 403, 0
				if authFailure {
					want = 500
				}
				if response := client.do("GET", path, nil); response.Code != want {
					t.Fatal("GET bypassed collection permission", response.Code)
				}
			case "rejected":
				want = 200
			case "rollback_failure", "invalid_result":
				want = 500
			case "unknown_outcome":
				want = 503
			}
			response := client.do("POST", path, values)
			if response.Code != want || calls != wantCalls || reads != before || strings.Contains(response.Body.String(), "private ") {
				t.Fatalf("collection command: status=%d calls=%d reads=%d", response.Code, calls, reads-before)
			}
			if want != 302 && response.Header().Get("Location") != "" || response.Header().Get("Retry-After") != "" {
				t.Fatal("failed operation advertised success or retry")
			}
			if want == 302 {
				notice := "existing"
				if mode == "created" {
					notice = "added"
				}
				if !siteSignedNoticeLocation(response.Header().Get("Location"), "/admin/accounts/", notice, "") {
					t.Fatal("collection result notice was not signed")
				}
				follow := client.do("GET", response.Header().Get("Location"), nil)
				if follow.Code != 200 || !strings.Contains(follow.Body.String(), `data-admin-message="`+notice+`"`) {
					t.Fatal("collection result did not survive redirect")
				}
			}
		})
	}
}

func TestAdminCollectionCommandDefinitionAndSubmissionOwnership(t *testing.T) {
	good := CollectionCommandConfig{Name: "ensure", Label: "Ensure", Permission: "accounts.add", AdditionalPermissions: []auth.Permission{"accounts.view"}, Form: managementCommandForm(t), Run: func(context.Context, auth.Principal, forms.Values) (CollectionCommandResult, error) {
		t.Fatal("invalid definition or input reached writer")
		return CollectionCommandResult{}, nil
	}}
	model := registeredModel{permissions: Permissions{View: "accounts.view", Change: "accounts.change"}}
	for _, mutate := range []func(*CollectionCommandConfig){
		func(c *CollectionCommandConfig) { c.Name = "../ensure" },
		func(c *CollectionCommandConfig) { c.Label = "" },
		func(c *CollectionCommandConfig) { c.Permission = "" },
		func(c *CollectionCommandConfig) { c.AdditionalPermissions = []auth.Permission{"bad permission"} },
		func(c *CollectionCommandConfig) { c.Run = nil },
		func(c *CollectionCommandConfig) { c.Form = forms.Spec{} },
		func(c *CollectionCommandConfig) {
			c.Form, _ = forms.NewSpec([]forms.Field{cField(t, "expected_revision")})
		},
		func(c *CollectionCommandConfig) {
			c.Form, _ = forms.NewSpec([]forms.Field{cField(t, "csrfmiddlewaretoken")})
		},
	} {
		config := good
		mutate(&config)
		if _, err := prepareCollectionCommands([]CollectionCommandConfig{config}, model); err == nil {
			t.Fatal("invalid collection command accepted")
		}
	}
	if _, err := prepareCollectionCommands([]CollectionCommandConfig{good, good}, model); err == nil {
		t.Fatal("duplicate collection command accepted")
	}
	commands, err := prepareCollectionCommands([]CollectionCommandConfig{good}, model)
	if err != nil {
		t.Fatal(err)
	}
	forged, _ := forms.NewSpec([]forms.Field{cField(t, "unrelated")})
	bound, _ := forged.Bind(t.Context(), forms.NewData(map[string][]string{"unrelated": {"valid elsewhere"}}), nil)
	if _, err := commands[0].run(t.Context(), mustPrincipalWithPermissions(t, "accounts.add", "accounts.view"), bound); err == nil {
		t.Fatal("foreign form accepted")
	}
	bound, _ = good.Form.Bind(t.Context(), forms.NewData(map[string][]string{"password": {"valid"}}), nil)
	for _, permissions := range [][]auth.Permission{{"accounts.add"}, {"accounts.view"}, {"accounts.add", "accounts.change"}} {
		if _, err := commands[0].run(t.Context(), mustPrincipalWithPermissions(t, permissions...), bound); err == nil {
			t.Fatal("direct command bypassed admission")
		}
	}
}
