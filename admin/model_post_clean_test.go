package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

func TestAdminModelCandidatePreservesOmissionAndOriginalNormalization(t *testing.T) {
	for _, operation := range []string{"create", "change"} {
		for _, input := range []string{"omitted", "empty", "normalized"} {
			t.Run(operation+"/"+input, func(t *testing.T) {
				config := validRegistryConfig(t)
				config.Snapshot = func(row registryArticle) (Object, error) {
					return NewObject(row.id, "Article", map[string]templates.Value{
						"id": templates.Integer(row.id), "title": templates.String(row.title),
						"published": templates.Bool(row.published), "summary": templates.Null(),
					})
				}
				for i := range config.Model.Fields {
					if config.Model.Fields[i].Name == "title" {
						config.Model.Fields[i].Blank = true
						config.Model.Fields[i].Default = &ir.Scalar{Kind: ir.ScalarString, String: "server-default"}
					}
				}
				config.FormOverrides = []formmodel.Override{formmodel.OverrideField("title", formmodel.WithStringNormalizer(func(value string) string {
					if value == "" {
						return value
					}
					return value + "!"
				}))}
				var got string
				writes := 0
				accept := func(values forms.Values, id int64) registryArticle {
					writes++
					var ok bool
					got, ok = values.String("title")
					if !ok {
						t.Fatal("model title missing")
					}
					if _, exists := values.Get("id"); exists {
						t.Fatal("private candidate key entered mutation input")
					}
					return registryArticle{id: id, title: got}
				}
				config.Create = func(_ context.Context, _ auth.Principal, values forms.Values) (registryArticle, error) {
					return accept(values, 2), nil
				}
				config.Update = func(_ context.Context, _ auth.Principal, mutation Mutation, values forms.Values) (registryArticle, []string, error) {
					return accept(values, mutation.ID), []string{"title"}, nil
				}
				builder := NewBuilder(mustApps(t))
				if err := RegisterModel(builder, config); err != nil {
					t.Fatal(err)
				}
				registry, err := builder.Build()
				if err != nil {
					t.Fatal(err)
				}
				model := registry.models[0]
				data := map[string][]string{}
				want := "server-default"
				if operation == "change" {
					want = "Go"
				}
				switch input {
				case "empty":
					data["title"] = []string{""}
					want = ""
				case "normalized":
					data["title"] = []string{"candidate"}
					want = "candidate!"
				}
				// A caller-owned initial must never supply the value retained by a
				// omitted defaulted input; change reads the authorized current row.
				submitted, err := model.form.Bind(forms.NewData(data), map[string]forms.Value{"title": forms.String("forged-initial")})
				if err != nil || !submitted.Valid() {
					t.Fatal("bind", err, submitted.Errors())
				}
				if operation == "create" {
					_, err = model.create(t.Context(), mustPrincipal(t), submitted)
				} else {
					_, _, err = model.update(t.Context(), mustPrincipal(t), Mutation{ID: 1}, submitted)
				}
				if err != nil || writes != 1 || got != want {
					t.Fatalf("candidate: got %q, want %q, writes %d, error %v", got, want, writes, err)
				}
			})
		}
	}
}

func TestAdminModelCleanSeesCurrentExcludedValuesAfterFieldFailure(t *testing.T) {
	calls := 0
	client, state, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
		config.Initial = func(row managementFormRow) (map[string]forms.Value, error) {
			return map[string]forms.Value{"username": forms.String(row.username), "active": forms.Boolean(row.active), "revision": forms.Integer(row.revision)}, nil
		}
		config.ModelValidators = []formmodel.Validator{formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
			calls++
			name, a := candidate.String("username")
			id, b := candidate.Integer("id")
			revision, c := candidate.Integer("revision")
			if !a || !b || !c || name != "Original" || id != 1 || revision != 1 {
				t.Fatal("model clean lost current row after invalid selected input")
			}
			return validation.NewErrors(validation.New(validation.NonField, "model_policy"))
		})}
	})
	record, found, err := registry.models[0].get(t.Context(), sitePrincipal(t, "manager", true, "accounts.change"), 1)
	if err != nil || !found {
		t.Fatal("current row", err)
	}
	if _, present := record.initial["revision"]; present {
		t.Fatal("excluded model value leaked into rendered initial")
	}
	client.login(t, "admin", "secret", "/admin/accounts/")
	get := client.do("GET", "/admin/accounts/change/?id=1", nil)
	if get.Code != 200 || calls != 0 || strings.Contains(get.Body.String(), `name="revision"`) {
		t.Fatal("GET executed model clean or exposed hidden input")
	}
	values := url.Values{"username": {""}, "active": {"on"}, "expected_revision": {"1"}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
	response := client.do("POST", "/admin/accounts/change/?id=1", values)
	if response.Code != 200 || calls != 1 || state.writes != 0 || !strings.Contains(response.Body.String(), `data-error-code="required"`) || !strings.Contains(response.Body.String(), `data-error-code="model_policy"`) {
		t.Fatal("model clean ordering or error ownership", response.Code, calls, state.writes)
	}
}

func TestAdminModelInitialRejectsIncompleteOrForgedStoredSnapshots(t *testing.T) {
	for _, mode := range []string{"missing_model_value", "primary_key_mismatch", "revision_mismatch", "unknown_field", "nonnullable", "invalid_model_value"} {
		t.Run(mode, func(t *testing.T) {
			_, _, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
				config.Model.Fields = append(config.Model.Fields, ir.Field{Name: "hidden", GoName: "Hidden", Column: "hidden", Kind: ir.FieldInteger})
				config.ModelValidators = []formmodel.Validator{formmodel.ValidatorFunc(func(forms.Values) validation.Errors { return validation.NewErrors() })}
				config.Initial = func(row managementFormRow) (map[string]forms.Value, error) {
					values := map[string]forms.Value{"username": forms.String(row.username), "active": forms.Boolean(row.active), "hidden": forms.Integer(3)}
					switch mode {
					case "missing_model_value":
						delete(values, "hidden")
					case "primary_key_mismatch":
						values["id"] = forms.Integer(2)
					case "revision_mismatch":
						values["revision"] = forms.Integer(row.revision + 1)
					case "unknown_field":
						values["forged"] = forms.Integer(1)
					case "nonnullable":
						values["hidden"] = forms.Null()
					case "invalid_model_value":
						values["hidden"] = forms.String("private-value")
					}
					return values, nil
				}
			})
			_, found, err := registry.models[0].get(t.Context(), sitePrincipal(t, "manager", true, "accounts.change"), 1)
			if found || errorCode(err) != mode {
				t.Fatalf("invalid current snapshot: found=%t code=%s", found, errorCode(err))
			}
		})
	}
}

func TestAdminChangeRechecksObservedRevisionBeforeCandidateWrite(t *testing.T) {
	callbacks := 0
	_, state, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
		original := config.Update
		config.Update = func(ctx context.Context, actor auth.Principal, mutation Mutation, values forms.Values) (managementFormRow, []string, error) {
			callbacks++
			return original(ctx, actor, mutation, values)
		}
	})
	model := registry.models[0]
	form, err := model.form.Bind(forms.NewData(map[string][]string{"username": {"Candidate"}, "active": {"on"}}), nil)
	if err != nil || !form.Valid() {
		t.Fatal("valid submission", err)
	}
	state.row.revision = 2
	_, _, err = model.update(t.Context(), sitePrincipal(t, "manager", true, "accounts.change"), Mutation{ID: 1, Revision: 1}, form)
	var conflict *OperationError
	if !errors.As(err, &conflict) || conflict.Code != OperationConflict || state.reads != 1 || state.writes != 0 || callbacks != 0 {
		t.Fatal("current revision was not checked before mutation", err, state.reads, state.writes, callbacks)
	}
}

func TestAdminRejectsNilModelValidatorBeforeAnyCallback(t *testing.T) {
	for _, creation := range []bool{false, true} {
		config := validRegistryConfig(t)
		var typedNil formmodel.ValidatorFunc
		if creation {
			config.CreateForm = &FormConfig{Definition: formmodel.Definition{ModelValidators: []formmodel.Validator{typedNil}}}
		} else {
			config.ModelValidators = []formmodel.Validator{typedNil}
		}
		if err := RegisterModel(NewBuilder(mustApps(t)), config); err == nil {
			t.Fatal("nil model validator entered registry")
		}
	}
}
