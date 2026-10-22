package model_test

import (
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	helpdmodels "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/validation"
)

func TestModelWidgetTemplateKeepsIRAndModelCleanOwnership(t *testing.T) {
	definition := formmodel.Definition{Fields: []string{"title"}, Overrides: []formmodel.Override{
		formmodel.OverrideField("title", formmodel.WithWidgetTemplate("title_control"), formmodel.WithMaxLength(8), formmodel.WithStringNormalizer(func(string) string { return "CLEAN" })),
	}, PostClean: formmodel.PostClean{Fields: []string{"summary"}, Clean: func(values forms.Values) (forms.Values, validation.Errors) {
		if value, _ := values.String("title"); value != "CLEAN" {
			t.Fatal("model clean did not receive the normalized native value")
		}
		return forms.NewValues(map[string]forms.Value{"summary": forms.String("model-owned")}), validation.Errors{}
	}}}
	spec, err := definition.Spec(models.ArticleDescriptor{}.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	current := models.NewArticleWithID(3)
	current.Title = "initial"
	bound, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {" raw < "}}), &current, definition.PostClean)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	model, err := prepared.Model()
	if err != nil || model.Title != "CLEAN" || model.Summary == nil || *model.Summary != "model-owned" || model.ID != 3 || current.Title != "initial" {
		t.Fatal("presentation changed typed preparation", model, err)
	}
	field := spec.Fields()[0]
	if field.WidgetTemplate() != "title_control" || field.Widget() != forms.TextInput || field.MaxLength() != 8 || !field.Required() || field.Nullable() {
		t.Fatal("presentation replaced projected field policy")
	}
	raw, _ := bound.BoundForm().Form().Submitted().Get("title")
	if len(raw) != 1 || raw[0] != " raw < " {
		t.Fatal("normalization rewrote redisplay input")
	}
	for _, name := range []string{"id", "computed"} {
		if _, err := formmodel.NewSpecForFields(models.ArticleDescriptor{}.Metadata(), []string{"title"}, formmodel.OverrideField(name, formmodel.WithWidgetTemplate("custom"))); err == nil {
			t.Fatal("renderer promoted an unselected or computed field", name)
		}
	}
	choices, err := formmodel.NewSpecForFields(helpdmodels.TicketDescriptor{}.Metadata(), []string{"labels"}, formmodel.OverrideField("labels", formmodel.WithWidgetTemplate("membership")))
	if err != nil || choices.Fields()[0].WidgetTemplate() != "membership" || choices.Fields()[0].Widget() != forms.SelectMultiple || !choices.Fields()[0].ModelChoice() {
		t.Fatal("collection presentation lost canonical membership type", err)
	}
}
