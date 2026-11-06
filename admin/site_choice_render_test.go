package admin

import (
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

func TestChoiceSearchRenderingKeepsSelectionSeparateFromSearchMembership(t *testing.T) {
	const large = int64(9007199254740993)
	field, err := forms.ModelMultipleChoiceField("members", forms.WithRequired(false), forms.WithChoices(
		forms.Choice{Value: forms.Integer(large), Label: "Selected <member>"},
		forms.Choice{Value: forms.Integer(88), Label: "Resolved but not selected"},
	))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	form, err := spec.Unbound(map[string]forms.Value{"members": forms.Integers(large)})
	if err != nil {
		t.Fatal(err)
	}
	view := choiceFieldView{URL: "/admin/team/change/choices/members/?id=1", Limit: 2, More: true, Items: []forms.Choice{
		{Value: forms.Integer(0), Label: "Zero"},
		{Value: forms.Integer(-4), Label: `<img src=x onerror="alert(1)">`},
	}}
	fields, err := formFieldContext(spec.Fields(), form, nil, "rows-2-", false, map[string]choiceFieldView{"members": view})
	if err != nil {
		t.Fatal(err)
	}
	context, err := templates.NewContext(map[string]templates.Value{"field": fields[0]})
	if err != nil {
		t.Fatal(err)
	}
	site := newSiteApplicationHarness(t, 2).site
	output, err := site.templates.Render(t.Context(), "field.html", context, templates.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	body := string(output)
	for _, expected := range []string{
		`<select name="rows-2-members" id="rows-2-members-choices" aria-describedby="rows-2-members-choice-status" multiple>`,
		`<option value="9007199254740993" selected>Selected &lt;member&gt;</option>`,
		`<option value="0" data-choice-page>Zero</option>`,
		`<option value="-4" data-choice-page>&lt;img`,
		`data-choice-url="/admin/team/change/choices/members/?id=1"`,
		`data-choice-controls hidden`, `data-choice-clear`, `aria-live="polite"`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("missing %q in %s", expected, body)
		}
	}
	for _, forbidden := range []string{`<img`, `value="88"`, `name="rows-2-members-choice-query"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("unexpected %q in %s", forbidden, body)
		}
	}
	// A searched choice is visible but cannot become a valid submitted member
	// until the caller resolves it for this request's server-side form.
	bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"members": {"0"}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Valid() || len(field.Choices()) != 2 || len(spec.Fields()[0].Choices()) != 2 {
		t.Fatal("rendering widened or changed resolved membership")
	}
}

func TestChoiceSearchRenderingRetainsRejectedValuesAndReadOnlyIdentifiers(t *testing.T) {
	field, err := forms.ModelChoiceField("owner", forms.WithRequired(false), forms.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	site := newSiteApplicationHarness(t, 2).site
	for _, readonly := range []bool{false, true} {
		name := "rejected"
		if readonly {
			name = "readonly"
		}
		t.Run(name, func(t *testing.T) {
			raw := url.Values{"owner": {`<bad & "key">`}}
			form, err := spec.Bind(t.Context(), forms.NewData(raw), nil)
			if readonly {
				form, err = spec.Unbound(map[string]forms.Value{"owner": forms.Integer(-7)})
			}
			if err != nil {
				t.Fatal(err)
			}
			view := choiceFieldView{Limit: 25, Disabled: readonly}
			if !readonly {
				view.URL = "/admin/team/add/choices/owner/"
			}
			fields, err := formFieldContext(spec.Fields(), form, raw, "", true, map[string]choiceFieldView{"owner": view})
			if err != nil {
				t.Fatal(err)
			}
			context, err := templates.NewContext(map[string]templates.Value{"field": fields[0]})
			if err != nil {
				t.Fatal(err)
			}
			output, err := site.templates.Render(t.Context(), "field.html", context, templates.Capabilities{})
			if err != nil {
				t.Fatal(err)
			}
			body := string(output)
			if strings.Contains(body, "<bad") {
				t.Fatal("unescaped rejected value", body)
			}
			if readonly {
				if !strings.Contains(body, `<option value="-7" selected>-7</option>`) || !strings.Contains(body, `data-choice-disabled`) || !strings.Contains(body, `data-choice-url=""`) || strings.Contains(body, "not a current choice") {
					t.Fatal("readonly identity or search state lost", body)
				}
			} else if !strings.Contains(body, `&lt;bad &amp; &#34;key&#34;&gt;`) || !strings.Contains(body, `data-error-code="invalid_choice"`) {
				t.Fatal("rejected selection was not preserved", body)
			}
		})
	}
}

// A writer rejection happens after the original form was valid. Rendering must
// not reuse either the original resolved label or a prior search-page label.
func TestChoiceSearchRenderingDiscardsLabelsAfterLateMembershipRejection(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		for _, searchable := range []bool{false, true} {
			field, err := forms.ModelChoiceField("target", forms.WithChoices(forms.Choice{Value: forms.Integer(7), Label: "Revoked resolved label"}))
			if multiple {
				field, err = forms.ModelMultipleChoiceField("target", forms.WithChoices(forms.Choice{Value: forms.Integer(7), Label: "Revoked resolved label"}))
			}
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			raw := url.Values{"target": {"7"}}
			form, err := spec.Bind(t.Context(), forms.NewData(raw), nil)
			if err != nil || !form.Valid() {
				t.Fatal("initial resolve should permit key", err)
			}
			form, err = form.WithErrors(validation.NewErrors(validation.New("target", "invalid_choice")))
			if err != nil {
				t.Fatal(err)
			}
			var views map[string]choiceFieldView
			if searchable {
				views = map[string]choiceFieldView{"target": {URL: "/admin/target/add/choices/target/", Limit: 20, More: true, Items: []forms.Choice{{Value: forms.Integer(7), Label: "Revoked page label"}, {Value: forms.Integer(9), Label: "Prior page row"}}}}
			}
			fields, err := formFieldContext(spec.Fields(), form, raw, "", true, views)
			if err != nil {
				t.Fatal(err)
			}
			context, err := templates.NewContext(map[string]templates.Value{"field": fields[0]})
			if err != nil {
				t.Fatal(err)
			}
			output, err := newSiteApplicationHarness(t, 2).site.templates.Render(t.Context(), "field.html", context, templates.Capabilities{})
			if err != nil {
				t.Fatal(err)
			}
			body := string(output)
			if !strings.Contains(body, `<option value="7" selected>7</option>`) || !strings.Contains(body, `data-error-code="invalid_choice"`) || strings.Contains(body, "Revoked") || strings.Contains(body, "Prior page row") || strings.Contains(body, `value="9"`) {
				t.Fatal("late rejection reused stale choice labels", body)
			}
			if got := spec.Fields()[0].Choices(); len(got) != 1 || got[0].Label != "Revoked resolved label" {
				t.Fatal("rendering mutated declared choices")
			}
		}
	}
}
