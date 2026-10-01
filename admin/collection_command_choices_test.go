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
)

func TestAdminCollectionCommandChoiceDefinition(t *testing.T) {
	field, err := forms.ModelChoiceField("target")
	if err != nil {
		t.Fatal(err)
	}
	form, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	model := registeredModel{permissions: Permissions{View: "accounts.view"}}
	for _, mode := range []string{"valid", "shared_permission", "missing", "duplicate", "unknown", "missing_permission", "missing_loader", "scalar_source"} {
		t.Run(mode, func(t *testing.T) {
			config := CollectionCommandConfig{Name: "save", Label: "Save for target", Permission: "accounts.change", Form: form,
				Run: func(context.Context, auth.Principal, forms.Values) (CollectionCommandResult, error) {
					t.Fatal("registration called the writer")
					return CollectionCommandResult{}, nil
				},
				RelatedChoices: []RelatedChoices{{Field: "target", Permission: "accounts.view", Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
					t.Fatal("registration queried choices")
					return nil, nil
				}}},
			}
			switch mode {
			case "shared_permission":
				config.RelatedChoices[0].Permission = config.Permission
			case "missing":
				config.RelatedChoices = nil
			case "duplicate":
				config.RelatedChoices = append(config.RelatedChoices, config.RelatedChoices[0])
			case "unknown":
				config.RelatedChoices[0].Field = "missing"
			case "missing_permission":
				config.RelatedChoices[0].Permission = ""
			case "missing_loader":
				config.RelatedChoices[0].Load = nil
			case "scalar_source":
				config.Form = managementCommandForm(t)
				config.RelatedChoices[0].Field = "name"
			}
			commands, err := prepareCollectionCommands([]CollectionCommandConfig{config}, model)
			if mode != "valid" && mode != "shared_permission" {
				if err == nil {
					t.Fatal("invalid choice definition accepted")
				}
				return
			}
			if err != nil || len(commands) != 1 {
				t.Fatal(err)
			}
			want := []auth.Permission{"accounts.change", "accounts.view"}
			if mode == "shared_permission" {
				want = want[:1]
			}
			if !slices.Equal(commands[0].permissions, want) {
				t.Fatal("choice admission was lost or duplicated", commands[0].permissions)
			}
		})
	}
}

func TestAdminCollectionCommandCurrentRelatedChoices(t *testing.T) {
	for _, mode := range []string{"saved", "stale_before_bind", "stale_before_run", "load_error", "reload_error", "invalid_choices", "denied_target", "csrf", "duplicate", "unknown_field"} {
		t.Run(mode, func(t *testing.T) {
			field, err := forms.ModelChoiceField("target")
			if err != nil {
				t.Fatal(err)
			}
			form, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			var denied auth.Permission
			authorizer := managementFormAuthorizer(func(ctx context.Context, p auth.Principal, permission auth.Permission) (bool, error) {
				if permission == denied {
					return false, nil
				}
				return (auth.PrincipalAuthorizer{}).Allowed(ctx, p, permission)
			})
			loads, writes := 0, 0
			posting := false
			sources := []RelatedChoices{{Field: "target", Permission: "accounts.delete", Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
				loads++
				if posting {
					if mode == "load_error" || mode == "reload_error" && loads == 2 {
						return nil, errors.New("private choice failure")
					}
					if mode == "stale_before_bind" || mode == "stale_before_run" && loads == 2 {
						return nil, nil
					}
					if mode == "invalid_choices" {
						return []forms.Choice{{Value: forms.String("not an integer key"), Label: "bad key"}}, nil
					}
				}
				return []forms.Choice{{Value: forms.Integer(7), Label: "Visible <&>"}}, nil
			}}}
			client, _, registry := newManagementFormSite(t, authorizer, func(config *ModelConfig[managementFormRow]) {
				config.CollectionCommands = []CollectionCommandConfig{{Name: "save", Label: "Save for target", Permission: "accounts.change", Form: form, RelatedChoices: sources,
					Run: func(_ context.Context, _ auth.Principal, values forms.Values) (CollectionCommandResult, error) {
						writes++
						if id, ok := values.Integer("target"); !ok || id != 7 {
							t.Fatal("incorrect choice reached writer")
						}
						return CollectionCommandResult{ID: 45, Changed: true}, nil
					},
				}}
			})
			if loads != 0 {
				t.Fatal("registration queried related rows")
			}
			sources[0].Permission = "forged.permission"
			sources[0].Load = nil
			description := registry.All()[0].CollectionCommands[0]
			if !slices.Equal(description.Permissions, []auth.Permission{"accounts.change", "accounts.delete"}) {
				t.Fatal("descriptor lost owned target permission")
			}
			client.login(t, "admin", "secret", "/admin/accounts/")
			path := "/admin/accounts/collection/save/"
			get := client.do("GET", path, nil)
			if get.Code != 200 || loads != 1 || !strings.Contains(get.Body.String(), `value="7"`) || !strings.Contains(get.Body.String(), "Visible &lt;&amp;&gt;") {
				t.Fatal("choice form did not load or escape its scoped options", get.Code, loads)
			}
			values := url.Values{"target": {"7"}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
			if mode == "denied_target" {
				denied = "accounts.delete"
				loads = 0
				if response := client.do("GET", path, nil); response.Code != 403 || loads != 0 {
					t.Fatal("denied target GET read related choices")
				}
			}
			switch mode {
			case "csrf":
				values.Del("csrfmiddlewaretoken")
			case "duplicate":
				values.Add("target", "8")
			case "unknown_field":
				values.Set("forged", "1")
			}
			posting = true
			loads = 0
			response := client.do("POST", path, values)
			want, wantWrites, wantLoads := 302, 1, 2
			switch mode {
			case "stale_before_bind", "duplicate":
				want, wantWrites, wantLoads = 200, 0, 1
			case "stale_before_run":
				want, wantWrites, wantLoads = 200, 0, 2
			case "load_error", "invalid_choices":
				want, wantWrites, wantLoads = 500, 0, 1
			case "reload_error":
				want, wantWrites, wantLoads = 500, 0, 2
			case "denied_target", "csrf":
				want, wantWrites, wantLoads = 403, 0, 0
			case "unknown_field":
				want, wantWrites, wantLoads = 400, 0, 0
			}
			if response.Code != want || writes != wantWrites || loads != wantLoads || strings.Contains(response.Body.String(), "private choice") {
				t.Fatalf("choice command status=%d/%d writes=%d/%d loads=%d/%d", response.Code, want, writes, wantWrites, loads, wantLoads)
			}
			if mode == "saved" && !siteSignedNoticeLocation(response.Header().Get("Location"), "/admin/accounts/", "changed", "") {
				t.Fatal("changed result did not retain its signed notice")
			}
			if (mode == "stale_before_bind" || mode == "stale_before_run") && !strings.Contains(response.Body.String(), `data-error-field="target" data-error-code="invalid_choice"`) {
				t.Fatal("stale choice lost its field rejection")
			}
		})
	}
}
