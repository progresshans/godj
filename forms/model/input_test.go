package model_test

import (
	"reflect"
	"testing"

	models "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestModelFormInputRetainsTargetBindingAndFreshExcludedValues(t *testing.T) {
	metadata := (models.ArticleDescriptor{}).Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	current := models.NewArticleWithID(0) // present zero is not an unsaved row
	current.Title, current.Summary = "old", new("initial")
	bound, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {"changed"}}), &current, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	input, err := formmodel.PrepareInput(models.ArticleObjects, bound.BoundForm())
	if err != nil {
		t.Fatal(err)
	}
	if key, bound := input.TargetKey(); !bound || !key.Equal(query.Integer(0)) {
		t.Fatal("initial instance binding was lost")
	}
	current.Summary = new("fresh write value")
	prepared, err := models.ArticleObjects.PrepareInput(input, &current)
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Model()
	if err != nil || value.Title != "changed" || value.Summary == current.Summary || *value.Summary != "fresh write value" {
		t.Fatal("stale form initial replaced excluded data", value, err)
	}
	wrong := models.NewArticleWithID(1)
	for _, target := range []*models.Article{nil, &wrong, new(models.Article)} {
		if result, err := models.ArticleObjects.PrepareInput(input, target); err == nil || !reflect.DeepEqual(result, orm.PreparedModel[models.Article]{}) {
			t.Fatal("form crossed instance identity", err)
		}
	}
	if _, err := input.WithTargetKey(query.Integer(1)); err == nil {
		t.Fatal("existing binding was replaced")
	}
	if _, err := input.WithTargetKey(query.Null()); err == nil {
		t.Fatal("existing binding was cleared")
	}
	newForm, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {"new"}}), nil, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	newInput, err := formmodel.PrepareInput(models.ArticleObjects, newForm.BoundForm())
	if err != nil {
		t.Fatal(err)
	}
	if key, bound := newInput.TargetKey(); !bound || !key.IsNull() {
		t.Fatal("new form acquired an existing identity")
	}
	if _, err := models.ArticleObjects.PrepareInput(newInput, &current); err == nil {
		t.Fatal("new form overwrote present zero key")
	}
	if _, err := models.ArticleObjects.PrepareInput(newInput, nil); err != nil {
		t.Fatal(err)
	}
}
