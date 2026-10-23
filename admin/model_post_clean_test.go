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
				config.Create = func(_ context.Context, _ auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (registryArticle, error) {
					values, inputErr := bound.Input()
					if inputErr != nil {
						var zero registryArticle
						return zero, inputErr
					}

					return accept(values, 2), nil
				}
				config.Update = func(_ context.Context, _ auth.Principal, mutation Mutation, bound formmodel.BoundForm, _ InlineSubmission) (registryArticle, []string, error) {
					values, inputErr := bound.Input()
					if inputErr != nil {
						var zero registryArticle
						return zero, nil, inputErr
					}

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
				submitted, err := model.form.Bind(t.Context(), forms.NewData(data), map[string]forms.Value{"title": forms.String("forged-initial")})
				if err != nil || !submitted.Valid() {
					t.Fatal("bind", err, submitted.Errors())
				}
				if operation == "create" {
					_, err = model.create(t.Context(), mustPrincipal(t), submitted, InlineSubmission{})
				} else {
					_, _, err = model.update(t.Context(), mustPrincipal(t), Mutation{ID: 1}, submitted, InlineSubmission{})
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
		config.PostClean.Validators = []formmodel.Validator{formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
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
				config.PostClean.Validators = []formmodel.Validator{formmodel.ValidatorFunc(func(forms.Values) validation.Errors { return validation.NewErrors() })}
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
		config.Update = func(ctx context.Context, actor auth.Principal, mutation Mutation, bound formmodel.BoundForm, _ InlineSubmission) (managementFormRow, []string, error) {
			callbacks++
			return original(ctx, actor, mutation, bound, InlineSubmission{})
		}
	})
	model := registry.models[0]
	form, err := model.form.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"Candidate"}, "active": {"on"}}), nil)
	if err != nil || !form.Valid() {
		t.Fatal("valid submission", err)
	}
	state.row.revision = 2
	_, _, err = model.update(t.Context(), sitePrincipal(t, "manager", true, "accounts.change"), Mutation{ID: 1, Revision: 1}, form, InlineSubmission{})
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
			config.CreateForm = &FormConfig{Definition: formmodel.Definition{PostClean: formmodel.PostClean{Validators: []formmodel.Validator{typedNil}}}}
		} else {
			config.PostClean.Validators = []formmodel.Validator{typedNil}
		}
		if err := RegisterModel(NewBuilder(mustApps(t)), config); err == nil {
			t.Fatal("nil model validator entered registry")
		}
	}
}

func TestAdminModelCleanPersistsExplicitHiddenChangesAndKeepsWriteFences(t *testing.T) {
	for _, mode := range []string{"create", "change", "invalid", "unrepresentable", "forged", "csrf", "denied", "stale", "late_conflict"} {
		t.Run(mode, func(t *testing.T) {
			calls, checks := 0, 0
			var state *managementFormState
			var fields []string
			client, owned, _ := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
				config.FormFields = []string{"username"}
				fields = []string{"username", "active"}
				clean := formmodel.PostClean{Fields: fields, Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
					calls++
					name, _ := candidate.String("username")
					active := forms.Boolean(false)
					if mode == "unrepresentable" {
						active = forms.Null()
					}
					return forms.NewValues(map[string]forms.Value{"username": forms.String(strings.ToUpper(name)), "active": active}), validation.Errors{}
				}}
				config.PostClean = clean
				config.CreateForm.Definition.PostClean = clean
				config.ValidateCreate = func(_ context.Context, _ auth.Principal, bound formmodel.BoundForm) error {
					checks++
					if active, ok := bound.Candidate().Boolean("active"); mode != "unrepresentable" && (!ok || active) {
						t.Fatal("read check missed clean candidate")
					}
					return nil
				}
				config.ValidateChange = func(ctx context.Context, actor auth.Principal, _ Mutation, bound formmodel.BoundForm) error {
					return config.ValidateCreate(ctx, actor, bound)
				}
				original := config.Create
				config.Create = func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (managementFormRow, error) {
					values, inputErr := bound.Input()
					if inputErr != nil {
						var zero managementFormRow
						return zero, inputErr
					}

					raw, _ := bound.Form().Submitted().Get("username")
					cleaned, _ := bound.Form().Cleaned().String("username")
					candidate, _ := bound.Candidate().String("username")
					if len(raw) != 1 || raw[0] != "mixed" || cleaned != "mixed" || candidate != "MIXED" {
						t.Fatal("final write callback lost submitted, cleaned or model candidate")
					}
					row, err := original(ctx, actor, bound, InlineSubmission{})
					if err != nil {
						return row, err
					}
					active, ok := values.Boolean("active")
					if !ok {
						t.Fatal("excluded clean change missing from typed create")
					}
					state.mu.Lock()
					defer state.mu.Unlock()
					row.active = active
					state.row = row
					return row, nil
				}
			})
			state = owned
			// Registry configuration owns the allowlist independently of caller edits.
			fields[0] = "revision"
			login := "admin"
			if mode == "denied" {
				login = "viewer"
			}
			client.login(t, login, "secret", "/admin/accounts/")
			path := "/admin/accounts/change/?id=1"
			if mode == "create" {
				path = "/admin/accounts/add/"
			}
			// Obtain a valid CSRF token through an admitted page for the denial control.
			getPath := path
			if mode == "denied" {
				getPath = "/admin/accounts/"
			}
			get := client.do("GET", getPath, nil)
			if get.Code != 200 || calls != 0 || strings.Contains(get.Body.String(), `name="active"`) {
				t.Fatal("GET executed clean or exposed hidden output", get.Code)
			}
			values := url.Values{"username": {"mixed"}, "csrfmiddlewaretoken": {siteCSRFToken(t, get.Body.String())}}
			if mode == "create" {
				values.Set("password1", "private-password")
				values.Set("password2", "private-password")
			} else {
				values.Set("expected_revision", "1")
			}
			switch mode {
			case "invalid":
				values.Set("username", "")
			case "forged":
				values.Set("active", "on")
			case "csrf":
				values.Set("csrfmiddlewaretoken", "forged")
			case "stale":
				state.row.revision = 2
			case "late_conflict":
				state.race = true
			}
			response := client.do("POST", path, values)
			switch mode {
			case "create", "change":
				if response.Code != 302 || state.row.username != "MIXED" || state.row.active || state.writes != 1 || calls != 2 || checks != 1 {
					t.Fatal("clean outputs lost in typed persistence or audit reconciliation", response.Code, state.row, state.writes, calls, checks)
				}
			case "unrepresentable":
				if response.Code != 500 || state.writes != 0 || calls != 2 || checks != 1 || !state.row.active || state.row.username != "Original" {
					t.Fatal("clean NULL became a zero-value write", response.Code, state.row, calls, checks)
				}
			case "invalid":
				if response.Code != 200 || state.writes != 0 || calls != 1 || checks != 1 || !strings.Contains(response.Body.String(), `data-error-code="required"`) {
					t.Fatal("model clean bypassed existing errors", response.Code, calls, checks)
				}
			case "forged":
				if response.Code != 400 || state.writes != 0 || calls != 0 || checks != 0 {
					t.Fatal("hidden input was admitted", response.Code, calls, checks)
				}
			case "csrf", "denied":
				if response.Code != 403 || state.writes != 0 || calls != 0 || checks != 0 {
					t.Fatal("model clean ran before admission", response.Code, calls, checks)
				}
			case "stale":
				if response.Code != 409 || state.writes != 0 || calls != 0 || checks != 0 {
					t.Fatal("model clean ran before observed revision check", response.Code, calls, checks)
				}
			case "late_conflict":
				if response.Code != 409 || state.writes != 0 || state.row.username != "Concurrent" || calls != 2 {
					t.Fatal("clean candidate bypassed final transaction fence", response.Code, calls, state.row)
				}
			}
		})
	}
}

func TestAdminModelCleanProtectsRevisionAndRequiresCompleteCurrentSnapshot(t *testing.T) {
	for _, creation := range []bool{false, true} {
		config := validRegistryConfig(t)
		// Published is not a revision integer, so use a dedicated stored integer.
		config.Model.Fields = append(config.Model.Fields, ir.Field{Name: "revision", GoName: "Revision", Column: "revision", Kind: ir.FieldInteger})
		config.FormFields = []string{"title"}
		config.RevisionField = "revision"
		clean := formmodel.PostClean{Fields: []string{"revision"}, Clean: func(forms.Values) (forms.Values, validation.Errors) { return forms.Values{}, validation.Errors{} }}
		if creation {
			config.CreateForm = &FormConfig{Definition: formmodel.Definition{Fields: []string{"title"}, PostClean: clean}}
		} else {
			config.PostClean = clean
		}
		if err := RegisterModel(NewBuilder(mustApps(t)), config); errorCode(err) != "clean_output" {
			t.Fatal("clean could own revision", err)
		}
	}
	_, _, registry := newManagementFormSite(t, auth.PrincipalAuthorizer{}, func(config *ModelConfig[managementFormRow]) {
		config.PostClean = formmodel.PostClean{Clean: func(forms.Values) (forms.Values, validation.Errors) {
			t.Fatal("read executed clean")
			return forms.Values{}, validation.Errors{}
		}}
		config.Model.Fields = append(config.Model.Fields, ir.Field{Name: "hidden", GoName: "Hidden", Column: "hidden", Kind: ir.FieldInteger})
	})
	if _, found, err := registry.models[0].get(t.Context(), sitePrincipal(t, "manager", true, "accounts.change"), 1); found || errorCode(err) != "missing_model_value" {
		t.Fatal("clean received incomplete current snapshot", found, err)
	}
}
