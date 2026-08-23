package admin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

type registryArticle struct {
	id        int64
	title     string
	published bool
	summary   *string
}

func TestRegistrySealsTypedModelAndReturnsDetachedDescriptors(t *testing.T) {
	installed := mustApps(t)
	builder := NewBuilder(installed)
	config := validRegistryConfig(t)
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}

	config.ListFields[0] = "forged"
	config.Model.Fields[0].Name = "forged"
	registry, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := builder.Build(); errorCode(err) != "sealed" {
		t.Fatalf("second Build() error = %v", err)
	}
	if err := RegisterModel(builder, validRegistryConfig(t)); errorCode(err) != "sealed" {
		t.Fatalf("RegisterModel() after Build error = %v", err)
	}

	descriptor, ok := registry.Lookup("godj_conformance", "article")
	if !ok {
		t.Fatal("Lookup() ok = false")
	}
	if descriptor.Slug != "articles" || descriptor.Model.Fields[0].Name != "id" ||
		!reflect.DeepEqual(descriptor.ListFields, []string{"id", "title", "published"}) ||
		len(descriptor.FormFields) != 3 || descriptor.FormFields[0].Name() != "title" {
		t.Fatalf("Lookup() = %#v", descriptor)
	}
	descriptor.Model.Fields[0].Name = "mutated"
	descriptor.ListFields[0] = "mutated"
	descriptor.Actions[0].Name = "mutated"
	again, _ := registry.Lookup("godj_conformance", "article")
	if again.Model.Fields[0].Name != "id" || again.ListFields[0] != "id" || again.Actions[0].Name != "publish" {
		t.Fatalf("registry aliases returned descriptor: %#v", again)
	}
	all := registry.All()
	all[0].SearchFields[0] = "mutated"
	if current, _ := registry.Lookup("godj_conformance", "article"); current.SearchFields[0] != "title" {
		t.Fatalf("All() aliases registry: %#v", current.SearchFields)
	}
}

func TestRegisteredModelValidatesTypedOperationBoundaries(t *testing.T) {
	builder := NewBuilder(mustApps(t))
	config := validRegistryConfig(t)
	var actionInput []int64
	config.Actions[0].Run = func(_ context.Context, _ auth.Principal, ids []int64) (ActionResult, error) {
		actionInput = append([]int64(nil), ids...)
		return ActionResult{MatchedIDs: append([]int64(nil), ids...)}, nil
	}
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	model := registry.models[0]

	principal := mustPrincipal(t)
	page, err := model.list(context.Background(), principal, ListRequest{Search: "go"})
	if err != nil {
		t.Fatalf("list() error = %v", err)
	}
	if page.limit != DefaultListLimit || page.total != 1 || len(page.objects) != 1 || page.objects[0].id != 1 {
		t.Fatalf("list() = %#v", page)
	}
	record, found, err := model.get(context.Background(), principal, 1)
	if err != nil || !found {
		t.Fatalf("get() = %#v, %v, %v", record, found, err)
	}
	if title, ok := record.initial["title"].AsString(); !ok || title != "Go" {
		t.Fatalf("get initial title = %q, %v", title, ok)
	}

	bound, err := model.form.Bind(forms.NewData(map[string][]string{
		"title":     {" Updated "},
		"published": {"on"},
		"summary":   {""},
	}), record.initial)
	if err != nil || !bound.Valid() {
		t.Fatalf("Bind() = %#v, %v", bound, err)
	}
	updated, changed, err := model.update(context.Background(), principal, 1, bound)
	if err != nil || updated.id != 1 || !reflect.DeepEqual(changed, []string{"title", "published", "summary"}) {
		t.Fatalf("update() = %#v, %v, %v", updated, changed, err)
	}
	outcome, err := model.actions[0].run(context.Background(), principal, []int64{3, 1, 3, 2})
	if err != nil || !reflect.DeepEqual(actionInput, []int64{1, 2, 3}) ||
		!reflect.DeepEqual(outcome.MatchedIDs, []int64{1, 2, 3}) {
		t.Fatalf("action() = %#v, input %v, error %v", outcome, actionInput, err)
	}
	outcome.MatchedIDs[0] = 99
	if reflect.DeepEqual(actionInput, outcome.MatchedIDs) {
		t.Fatal("action result aliases callback input")
	}
}

func TestRegisterModelRejectsInvalidStartupDefinitions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ModelConfig[registryArticle])
		code   string
	}{
		{name: "uninstalled app", mutate: func(config *ModelConfig[registryArticle]) { config.AppLabel = "missing" }, code: "not_installed"},
		{name: "invalid slug", mutate: func(config *ModelConfig[registryArticle]) { config.Slug = "Bad/Slug" }, code: "invalid"},
		{name: "unknown list field", mutate: func(config *ModelConfig[registryArticle]) { config.ListFields = []string{"missing"} }, code: "unknown_field"},
		{name: "non-char search field", mutate: func(config *ModelConfig[registryArticle]) { config.SearchFields = []string{"published"} }, code: "not_searchable"},
		{name: "invalid permission", mutate: func(config *ModelConfig[registryArticle]) { config.Permissions.View = "UPPER.view" }, code: "invalid"},
		{name: "missing callback", mutate: func(config *ModelConfig[registryArticle]) { config.Create = nil }, code: "missing"},
		{name: "duplicate action", mutate: func(config *ModelConfig[registryArticle]) { config.Actions = append(config.Actions, config.Actions[0]) }, code: "duplicate"},
		{name: "nil action", mutate: func(config *ModelConfig[registryArticle]) { config.Actions[0].Run = nil }, code: "missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validRegistryConfig(t)
			test.mutate(&config)
			err := RegisterModel(NewBuilder(mustApps(t)), config)
			if got := errorCode(err); got != test.code {
				t.Fatalf("RegisterModel() error = %v, code %q, want %q", err, got, test.code)
			}
		})
	}
}

func TestCopiedBuilderSharesOneModelIndexAndSealState(t *testing.T) {
	builder := NewBuilder(mustApps(t))
	copied := *builder
	article := validRegistryConfig(t)
	if err := RegisterModel(builder, article); err != nil {
		t.Fatalf("RegisterModel(article) error = %v", err)
	}
	note := validRegistryConfig(t)
	note.Slug = "notes"
	note.Model.Name = "note"
	note.Model.GoName = "Note"
	note.Model.DBTable = "godj_conformance_note"
	if err := RegisterModel(&copied, note); err != nil {
		t.Fatalf("RegisterModel(note through copy) error = %v", err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(registry.All()) != 2 {
		t.Fatalf("registry model count = %d", len(registry.All()))
	}
	got, ok := registry.Lookup("godj_conformance", "note")
	if !ok || got.Model.Name != "note" || got.Slug != "notes" {
		t.Fatalf("Lookup(note) = %#v, %v", got, ok)
	}
	if _, err := copied.Build(); errorCode(err) != "sealed" {
		t.Fatalf("copied Build() error = %v", err)
	}
}

func TestRegisteredGetRejectsPartialOrSnapshotDivergentInitial(t *testing.T) {
	tests := []struct {
		name    string
		initial func(registryArticle) (map[string]forms.Value, error)
		code    string
	}{
		{
			name: "partial",
			initial: func(article registryArticle) (map[string]forms.Value, error) {
				return map[string]forms.Value{
					"title":   forms.String(article.title),
					"summary": forms.Null(),
				}, nil
			},
			code: "field_count_mismatch",
		},
		{
			name: "snapshot divergent",
			initial: func(article registryArticle) (map[string]forms.Value, error) {
				return map[string]forms.Value{
					"title":     forms.String(article.title),
					"published": forms.Boolean(false),
					"summary":   forms.Null(),
				}, nil
			},
			code: "snapshot_mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validRegistryConfig(t)
			config.Get = func(_ context.Context, id int64) (registryArticle, bool, error) {
				return registryArticle{id: id, title: "Go", published: true}, true, nil
			}
			config.Initial = test.initial
			builder := NewBuilder(mustApps(t))
			if err := RegisterModel(builder, config); err != nil {
				t.Fatalf("RegisterModel() error = %v", err)
			}
			registry, _ := builder.Build()
			_, _, err := registry.models[0].get(context.Background(), mustPrincipal(t), 1)
			if got := errorCode(err); got != test.code {
				t.Fatalf("get error = %v, code %q", err, got)
			}
		})
	}
}

func TestRegisteredActionRejectsUnselectedOrNoncanonicalResults(t *testing.T) {
	tests := []struct {
		name    string
		matched []int64
		code    string
	}{
		{name: "unselected", matched: []int64{1, 3}, code: "unselected_id"},
		{name: "duplicate", matched: []int64{1, 1}, code: "not_canonical"},
		{name: "descending", matched: []int64{2, 1}, code: "not_canonical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validRegistryConfig(t)
			config.Actions[0].Run = func(context.Context, auth.Principal, []int64) (ActionResult, error) {
				return ActionResult{MatchedIDs: append([]int64(nil), test.matched...)}, nil
			}
			builder := NewBuilder(mustApps(t))
			if err := RegisterModel(builder, config); err != nil {
				t.Fatalf("RegisterModel() error = %v", err)
			}
			registry, _ := builder.Build()
			_, err := registry.models[0].actions[0].run(context.Background(), mustPrincipal(t), []int64{1, 2})
			if got := errorCode(err); got != test.code {
				t.Fatalf("action error = %v, code %q", err, got)
			}
		})
	}
}

func TestRegisteredActionCannotMutateAuthoritativeSelection(t *testing.T) {
	config := validRegistryConfig(t)
	config.Actions[0].Run = func(_ context.Context, _ auth.Principal, ids []int64) (ActionResult, error) {
		ids[0] = 999
		return ActionResult{MatchedIDs: []int64{999}}, nil
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, _ := builder.Build()
	_, err := registry.models[0].actions[0].run(context.Background(), mustPrincipal(t), []int64{1, 2})
	if got := errorCode(err); got != "unselected_id" {
		t.Fatalf("action error = %v, code %q", err, got)
	}
}

func TestRegisteredMutationsEnforcePermissionAndValidFormBeforeCallbacks(t *testing.T) {
	config := validRegistryConfig(t)
	called := 0
	config.Create = func(context.Context, auth.Principal, forms.Values) (registryArticle, error) {
		called++
		return registryArticle{id: 2, title: "Created"}, nil
	}
	config.Update = func(context.Context, auth.Principal, int64, forms.Values) (registryArticle, []string, error) {
		called++
		return registryArticle{id: 1, title: "Updated"}, []string{"title"}, nil
	}
	config.Delete = func(context.Context, auth.Principal, int64) (registryArticle, error) {
		called++
		return registryArticle{id: 1, title: "Deleted"}, nil
	}
	config.Actions[0].Run = func(context.Context, auth.Principal, []int64) (ActionResult, error) {
		called++
		return ActionResult{MatchedIDs: []int64{1}}, nil
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, _ := builder.Build()
	model := registry.models[0]
	valid, err := model.form.Bind(forms.NewData(map[string][]string{
		"title": {"Created"}, "published": {"false"}, "summary": {""},
	}), nil)
	if err != nil || !valid.Valid() {
		t.Fatalf("valid Bind() = %#v, %v", valid, err)
	}
	unpermissioned := mustPrincipalWithPermissions(t)
	if _, err := model.create(context.Background(), unpermissioned, valid); errorCode(err) != "denied" {
		t.Fatalf("create permission error = %v", err)
	}
	if _, _, err := model.update(context.Background(), unpermissioned, 1, valid); errorCode(err) != "denied" {
		t.Fatalf("update permission error = %v", err)
	}
	if _, err := model.delete(context.Background(), unpermissioned, 1); errorCode(err) != "denied" {
		t.Fatalf("delete permission error = %v", err)
	}
	if _, err := model.actions[0].run(context.Background(), unpermissioned, []int64{1}); errorCode(err) != "denied" {
		t.Fatalf("action permission error = %v", err)
	}
	if called != 0 {
		t.Fatalf("callbacks after denied operations = %d", called)
	}

	invalid, err := model.form.Bind(forms.NewData(map[string][]string{
		"published": {"false"}, "summary": {""},
	}), nil)
	if err != nil || invalid.Valid() {
		t.Fatalf("invalid Bind() = %#v, %v", invalid, err)
	}
	if _, err := model.create(context.Background(), mustPrincipal(t), invalid); errorCode(err) != "not_bound_valid" {
		t.Fatalf("create invalid form error = %v", err)
	}
	if _, _, err := model.update(context.Background(), mustPrincipal(t), 1, invalid); errorCode(err) != "not_bound_valid" {
		t.Fatalf("update invalid form error = %v", err)
	}
	if called != 0 {
		t.Fatalf("callbacks after invalid forms = %d", called)
	}
}

func TestRegisteredSnapshotMustMatchCompleteIRShape(t *testing.T) {
	config := validRegistryConfig(t)
	config.Snapshot = func(article registryArticle) (Object, error) {
		return NewObject(article.id, article.title, map[string]templates.Value{
			"id":        templates.Integer(article.id),
			"title":     templates.Bool(true),
			"published": templates.Bool(article.published),
			"summary":   templates.Null(),
		})
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, _ := builder.Build()
	_, err := registry.models[0].list(context.Background(), mustPrincipal(t), ListRequest{})
	if got := errorCode(err); got != "type_or_constraint_mismatch" {
		t.Fatalf("list snapshot error = %v, code %q", err, got)
	}
}

func TestRegisteredMutationRevalidatesAgainstItsOwnFormSpec(t *testing.T) {
	config := validRegistryConfig(t)
	config.FormOverrides = []formmodel.Override{formmodel.OverrideField(
		"title",
		formmodel.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
			text, _ := value.AsString()
			if text == "Blocked" {
				return validation.NewErrors(validation.New(validation.Field("title"), "reserved"))
			}
			return validation.NewErrors()
		})),
	)}
	called := 0
	config.Create = func(context.Context, auth.Principal, forms.Values) (registryArticle, error) {
		called++
		return registryArticle{id: 2, title: "Blocked"}, nil
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, _ := builder.Build()
	alternate, err := formmodel.NewSpec(config.Model)
	if err != nil {
		t.Fatalf("alternate formmodel.NewSpec() error = %v", err)
	}
	foreign, err := alternate.Bind(forms.NewData(map[string][]string{
		"title": {"Blocked"}, "published": {"false"}, "summary": {""},
	}), nil)
	if err != nil || !foreign.Valid() {
		t.Fatalf("alternate Bind() = %#v, %v", foreign, err)
	}
	_, err = registry.models[0].create(context.Background(), mustPrincipal(t), foreign)
	if got := errorCode(err); got != "spec_validation_failed" {
		t.Fatalf("create foreign form error = %v, code %q", err, got)
	}
	if called != 0 {
		t.Fatalf("create callback count = %d", called)
	}
}

func TestRegisteredListRejectsItemsBeyondReportedTotal(t *testing.T) {
	config := validRegistryConfig(t)
	config.List = func(_ context.Context, request ListRequest) (Page[registryArticle], error) {
		return Page[registryArticle]{
			Items:  []registryArticle{{id: 2, title: "Impossible"}},
			Total:  1,
			Offset: request.Offset,
			Limit:  request.Limit,
		}, nil
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	_, err = registry.models[0].list(context.Background(), mustPrincipal(t), ListRequest{Offset: 1})
	if got := errorCode(err); got != "invalid" {
		t.Fatalf("list impossible page error = %v, code %q", err, got)
	}
}

func TestRegistryConcurrentSnapshotsAndOperationsDoNotAlias(t *testing.T) {
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, validRegistryConfig(t)); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, _ := builder.Build()
	principal := mustPrincipal(t)
	const goroutines = 32
	errors := make(chan error, goroutines)
	for index := 0; index < goroutines; index++ {
		go func() {
			all := registry.All()
			all[0].ListFields[0] = "mutated"
			if descriptor, ok := registry.Lookup("godj_conformance", "article"); !ok || descriptor.ListFields[0] != "id" {
				errors <- fmt.Errorf("lookup alias: %#v, %v", descriptor, ok)
				return
			}
			page, err := registry.models[0].list(context.Background(), principal, ListRequest{})
			if err != nil || len(page.objects) != 1 || page.objects[0].id != 1 {
				errors <- fmt.Errorf("list: %#v: %w", page, err)
				return
			}
			errors <- nil
		}()
	}
	for index := 0; index < goroutines; index++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
}

func validRegistryConfig(t *testing.T) ModelConfig[registryArticle] {
	t.Helper()
	model := mustModel(t)
	permissions := Permissions{
		View:   mustPermission(t, "articles.view"),
		Add:    mustPermission(t, "articles.add"),
		Change: mustPermission(t, "articles.change"),
		Delete: mustPermission(t, "articles.delete"),
	}
	return ModelConfig[registryArticle]{
		AppLabel:     "godj_conformance",
		Slug:         "articles",
		Model:        model,
		ListFields:   []string{"id", "title", "published"},
		SearchFields: []string{"title", "summary"},
		Permissions:  permissions,
		List: func(_ context.Context, request ListRequest) (Page[registryArticle], error) {
			return Page[registryArticle]{
				Items:  []registryArticle{{id: 1, title: "Go"}},
				Total:  1,
				Offset: request.Offset,
				Limit:  request.Limit,
			}, nil
		},
		Get: func(_ context.Context, id int64) (registryArticle, bool, error) {
			if id != 1 {
				return registryArticle{}, false, nil
			}
			return registryArticle{id: 1, title: "Go"}, true, nil
		},
		Snapshot: func(article registryArticle) (Object, error) {
			summary := templates.Null()
			if article.summary != nil {
				summary = templates.String(*article.summary)
			}
			return NewObject(article.id, article.title, map[string]templates.Value{
				"id":        templates.Integer(article.id),
				"title":     templates.String(article.title),
				"published": templates.Bool(article.published),
				"summary":   summary,
			})
		},
		Initial: func(article registryArticle) (map[string]forms.Value, error) {
			summary := forms.Null()
			if article.summary != nil {
				summary = forms.String(*article.summary)
			}
			return map[string]forms.Value{
				"title":     forms.String(article.title),
				"published": forms.Boolean(article.published),
				"summary":   summary,
			}, nil
		},
		Create: func(_ context.Context, _ auth.Principal, values forms.Values) (registryArticle, error) {
			title, _ := values.String("title")
			return registryArticle{id: 2, title: title}, nil
		},
		Update: func(_ context.Context, _ auth.Principal, id int64, values forms.Values) (registryArticle, []string, error) {
			title, _ := values.String("title")
			published, _ := values.Boolean("published")
			return registryArticle{id: id, title: title, published: published}, []string{"title", "published", "summary"}, nil
		},
		Delete: func(_ context.Context, _ auth.Principal, id int64) (registryArticle, error) {
			return registryArticle{id: id, title: "Deleted"}, nil
		},
		History: func(context.Context, int64) ([]AuditEntry, error) { return nil, nil },
		Actions: []ActionConfig{{
			Name:       "publish",
			Label:      "Publish selected articles",
			Permission: permissions.Change,
			Run: func(_ context.Context, _ auth.Principal, ids []int64) (ActionResult, error) {
				return ActionResult{MatchedIDs: append([]int64(nil), ids...)}, nil
			},
		}},
	}
}

func mustApps(t *testing.T) apps.Registry {
	t.Helper()
	registry, err := apps.New([]apps.Config{{Name: "example/articles", Label: "godj_conformance"}})
	if err != nil {
		t.Fatalf("apps.New() error = %v", err)
	}
	return registry
}

func mustModel(t *testing.T) ir.Model {
	t.Helper()
	normalized, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "godj_conformance",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "godj_conformance_article",
			Fields: []ir.Field{
				{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
				{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean}},
				{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ir.Normalize() error = %v", err)
	}
	return normalized.Models[0]
}

func mustPermission(t *testing.T, value string) auth.Permission {
	t.Helper()
	permission, err := auth.NewPermission(value)
	if err != nil {
		t.Fatalf("auth.NewPermission(%q) error = %v", value, err)
	}
	return permission
}

func mustPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	permissions := []auth.Permission{
		mustPermission(t, "articles.view"),
		mustPermission(t, "articles.add"),
		mustPermission(t, "articles.change"),
		mustPermission(t, "articles.delete"),
	}
	return mustPrincipalWithPermissions(t, permissions...)
}

func mustPrincipalWithPermissions(t *testing.T, permissions ...auth.Permission) auth.Principal {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "operator",
		Active:      true,
		Permissions: append([]auth.Permission(nil), permissions...),
	})
	if err != nil {
		t.Fatalf("auth.NewPrincipal() error = %v", err)
	}
	return principal
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var config *ConfigError
	if errors.As(err, &config) {
		return config.Code
	}
	return fmt.Sprint(err)
}
