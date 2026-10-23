package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

const textWidgetTestTemplate = `<input data-custom-text name="{{ field.name }}" id="{{ field.control_id }}" value="{{ field.value }}"{% if field.readonly %} disabled{% endif %}{% if field.invalid %} aria-invalid="true" aria-describedby="{{ field.error_id }}"{% endif %}{% if field.required %} required{% endif %}>`

func textWidgetTestConfig() WidgetConfig {
	return WidgetConfig{Templates: fstest.MapFS{"input.html": {Data: []byte(textWidgetTestTemplate)}}, Entries: []WidgetTemplate{{Name: "text_control", Widget: forms.TextInput, Template: "input.html"}}}
}

func fileWidgetTestConfig() WidgetConfig {
	return WidgetConfig{Templates: fstest.MapFS{"file.html": {Data: []byte(`<input type="file" data-custom-file="{{ field.name }}" name="{{ field.name }}" id="{{ field.control_id }}"{% if field.readonly %} disabled{% endif %}{% if field.invalid %} aria-invalid="true" aria-describedby="{{ field.error_id }}"{% endif %}{% if field.required %} required{% endif %}>`)}}, Entries: []WidgetTemplate{{Name: "file_control", Widget: forms.ClearableFileInput, Template: "file.html"}}}
}

func TestAdminWidgetStartupRejectsInvalidDefinitionsAndEveryUnresolvedSurface(t *testing.T) {
	base := siteTestRegistry(t, newSiteModelState())
	paths, err := SiteAllowedNextPaths(base, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := siteTestRuntime(t, base, paths, "/admin/login/", "/admin/", siteTestIdentities(t))
	create := func(registry Registry, widgets WidgetConfig) error {
		_, err := NewSite(SiteConfig{Apps: mustApps(t), Namespace: "godj_conformance", Registry: registry, Auth: runtime, Widgets: widgets})
		return err
	}
	for _, test := range []struct {
		name   string
		change func(*WidgetConfig)
	}{
		{"no_source", func(c *WidgetConfig) { c.Templates = nil }},
		{"typed_nil_source", func(c *WidgetConfig) { c.Templates = (*fstest.MapFS)(nil) }},
		{"no_entries", func(c *WidgetConfig) { c.Entries = nil }},
		{"too_many", func(c *WidgetConfig) { c.Entries = make([]WidgetTemplate, MaximumWidgetTemplates+1) }},
		{"invalid_name", func(c *WidgetConfig) { c.Entries[0].Name = "../input" }},
		{"duplicate", func(c *WidgetConfig) { c.Entries = append(c.Entries, c.Entries[0]) }},
		{"unknown_kind", func(c *WidgetConfig) { c.Entries[0].Widget = forms.Widget(255) }},
		{"password", func(c *WidgetConfig) { c.Entries[0].Widget = forms.PasswordInput }},
		{"hidden", func(c *WidgetConfig) { c.Entries[0].Widget = forms.HiddenInput }},
		{"missing_template", func(c *WidgetConfig) { c.Entries[0].Template = "missing.html" }},
		{"bad_syntax", func(c *WidgetConfig) {
			c.Templates = fstest.MapFS{"input.html": {Data: []byte(`{% if field.value %}`)}}
		}},
		{"include_cycle", func(c *WidgetConfig) {
			c.Templates = fstest.MapFS{"input.html": {Data: []byte(`{% include "input.html" %}`)}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := textWidgetTestConfig()
			test.change(&config)
			if err := create(base, config); err == nil {
				t.Fatal("invalid widget published routes")
			}
		})
	}
	field, err := forms.CharField("title", forms.WithWidgetTemplate("text_control"))
	if err != nil {
		t.Fatal(err)
	}
	form, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"form", "create_form", "command", "collection_command", "inline", "collection_set"} {
		t.Run(surface, func(t *testing.T) {
			registry := base
			registry.models = append([]registeredModel(nil), base.models...)
			model := &registry.models[0]
			switch surface {
			case "form":
				model.form = form
			case "create_form":
				model.createForm = form
			case "command":
				model.commands = []registeredCommand{{name: "command", form: form}}
			case "collection_command":
				model.collectionCommands = []registeredCollectionCommand{{name: "command", form: form}}
			case "inline":
				model.inlines = []Inline{{prefix: "children", fields: form.Fields()}}
			case "collection_set":
				model.collectionFormSets = []CollectionFormSet{{name: "batch", fields: form.Fields()}}
			}
			if err := create(registry, WidgetConfig{}); errorCode(err) != "missing_or_incompatible" {
				t.Fatal("unresolved widget escaped startup", err)
			}
			config := textWidgetTestConfig()
			config.Entries[0].Widget = forms.Textarea
			if err := create(registry, config); errorCode(err) != "missing_or_incompatible" {
				t.Fatal("native widget mismatch escaped startup", err)
			}
		})
	}
}

func TestAdminWidgetHTTPPreservesRawInputComposedValidationAndStoredPolicy(t *testing.T) {
	config := validRegistryConfig(t)
	config.CreateForm = &FormConfig{Definition: formmodel.Definition{Fields: []string{"title", "published", "summary"}, Overrides: []formmodel.Override{
		formmodel.OverrideField("title", formmodel.WithWidgetTemplate("text_control"), formmodel.WithStringNormalizer(strings.ToUpper), formmodel.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
			text, _ := value.AsString()
			if strings.Contains(text, "REJECT") {
				return validation.NewErrors(validation.New("title", "field_rejected"))
			}
			return validation.Errors{}
		}))),
	}, Validators: []forms.CrossValidator{forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		if summary, _ := values.String("summary"); summary == "cross" {
			return validation.NewErrors(validation.New(validation.NonField, "cross_rejected"))
		}
		return validation.Errors{}
	})}, PostClean: formmodel.PostClean{Clean: func(values forms.Values) (forms.Values, validation.Errors) {
		if summary, _ := values.String("summary"); summary == "model" {
			return forms.Values{}, validation.NewErrors(validation.New("summary", "model_rejected"))
		}
		return forms.Values{}, validation.Errors{}
	}}}}
	calls := 0
	config.Create = func(_ context.Context, _ auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (registryArticle, error) {
		calls++
		values, err := bound.Input()
		if err != nil {
			return registryArticle{}, err
		}
		text, _ := values.String("title")
		if text != "SAVED" {
			t.Fatal("widget changed the writer's native cleaned input", text)
		}
		return registryArticle{id: 4, title: text}, nil
	}
	client, _, _ := uploadTestSite(t, config, uploadTestPolicy(t), auth.PrincipalAuthorizer{}, textWidgetTestConfig())
	client.login(t, "admin", "secret", "/admin/")
	page := client.do(http.MethodGet, "/admin/articles/add/", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-custom-text`) {
		t.Fatal("custom control absent", page.Code, page.Body.String())
	}
	token := siteCSRFToken(t, page.Body.String())
	for _, scenario := range []struct {
		name, title, summary string
		codes                []string
	}{
		{"field_and_cross", `  reject <img src=x>  `, "cross", []string{"field_rejected", "cross_rejected"}},
		{"field_and_model", `reject <script>`, "model", []string{"field_rejected", "model_rejected"}},
		{"model", `  valid <img src=x>  `, "model", []string{"model_rejected"}},
		{"required", "", "", []string{"required"}},
		{"ir_length", strings.Repeat("x", 201), "", []string{"max_length"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			response := client.do(http.MethodPost, "/admin/articles/add/", url.Values{"csrfmiddlewaretoken": {token}, "title": {scenario.title}, "summary": {scenario.summary}})
			if response.Code != 200 || calls != 0 {
				t.Fatal("invalid extension input reached the writer", response.Code, calls, response.Body.String())
			}
			body := response.Body.String()
			for _, code := range scenario.codes {
				if !strings.Contains(body, `data-error-code="`+code+`"`) {
					t.Fatal("composed diagnostic missing", code, body)
				}
			}
			if strings.Contains(body, "<img") || strings.Contains(body, "<script>") || !strings.Contains(body, `data-custom-text`) {
				t.Fatal("raw HTML or missing custom redisplay", body)
			}
			if strings.Contains(scenario.title, "reject") && (!strings.Contains(body, `aria-invalid="true" aria-describedby="title-errors"`) || !strings.Contains(body, `id="title-errors"`) || !strings.Contains(body, "reject &lt;")) {
				t.Fatal("widget lost raw spelling, spacing or accessible diagnostics", body)
			}
		})
	}
	response := client.do(http.MethodPost, "/admin/articles/add/", url.Values{"csrfmiddlewaretoken": {token}, "title": {"  saved  "}, "summary": {""}})
	if response.Code != 302 || calls != 1 {
		t.Fatal("valid native input did not reach explicit writer", response.Code, calls, response.Body.String())
	}
}

func TestAdminWidgetSnapshotsAndContextsRetainReadOnlyAndBoundedRendering(t *testing.T) {
	config := textWidgetTestConfig()
	registry, err := prepareWidgets(config, Registry{}, templates.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	config.Entries[0].Template = "changed"
	config.Templates.(fstest.MapFS)["input.html"].Data = []byte(`caller-mutated`)
	field, err := forms.CharField("title", forms.WithWidgetTemplate("text_control"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.ReadOnlyInitial = "rows", true
	setSpec, err := forms.NewSetSpec(spec, policy)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := setSpec.Bind(t.Context(), forms.NewData(map[string][]string{"rows-TOTAL_FORMS": {"2"}, "rows-INITIAL_FORMS": {"1"}, "rows-0-title": {"forged-secret"}, "rows-1-title": {"raw <new>"}}), []map[string]forms.Value{{"title": forms.String("stored <title>")}})
	if err != nil {
		t.Fatal(err)
	}
	values := make([]templates.Value, 0, 2)
	for _, row := range rows.Forms() {
		value, err := formSetRowContext(row, "id", forms.Integer(11), true, 3, false)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	group, err := templates.Object(map[string]templates.Value{"rows": templates.List(values...), "empty": templates.List()})
	if err != nil {
		t.Fatal(err)
	}
	page := map[string]templates.Value{"inlines": templates.List(group), "readonly_fields": templates.List(values...), "principal": templates.String("private principal")}
	if err := registry.apply(t.Context(), page); err != nil {
		t.Fatal(err)
	}
	groups, _ := page["inlines"].Items()
	list, _ := groups[0].Member("rows")
	rendered, _ := list.Items()
	for index, row := range rendered {
		list, _ := row.Member("fields")
		fields, _ := list.Items()
		markup, _ := fields[0].Member("custom_widget")
		html, _ := markup.AsString()
		if !strings.Contains(html, `data-custom-text`) || strings.Contains(html, "forged-secret") || strings.Contains(html, "caller-mutated") || strings.Contains(html, "private principal") {
			t.Fatal("widget context leaked or retained caller state", html)
		}
		if index == 0 && (!strings.Contains(html, ` disabled`) || !strings.Contains(html, "stored &lt;title&gt;")) {
			t.Fatal("readonly context used submitted input", html)
		}
		if index == 1 && (strings.Contains(html, ` disabled`) || !strings.Contains(html, "raw &lt;new&gt;")) {
			t.Fatal("new row did not retain its input", html)
		}
	}
	original, _ := page["readonly_fields"].Items()
	list, _ = original[0].Member("fields")
	fields, _ := list.Items()
	if _, changed := fields[0].Member("custom_widget"); changed {
		t.Fatal("computed presentation acquired a control")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := registry.apply(cancelled, page); !errors.Is(err, context.Canceled) {
		t.Fatal("render swallowed cancellation", err)
	}
	registry.budget = 1
	if err := registry.apply(t.Context(), page); errorCode(err) != "output_limit_exceeded" {
		t.Fatal("per-page widget budget was not enforced", err)
	}
}
