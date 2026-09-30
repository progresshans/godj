package model_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
)

func TestNarrowedModelFormInitialAgainstPinnedDjango(t *testing.T) {
	data, err := os.ReadFile("../testdata/initial-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django string
		Cases  []struct {
			Name, Initial, Candidate string
			Valid, Changed           bool
			Codes                    []string
			Prepared                 *string
		} `json:"model_cases"`
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Cases) != 2 {
		t.Fatal("unexpected native model initial reference")
	}
	metadata := (models.ArticleDescriptor{}).Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"title"}, formmodel.OverrideField("title", formmodel.WithMaxLength(4)))
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			current := models.NewArticleWithID(7)
			current.Title = observed.Initial
			initial, err := formmodel.InitialValues(metadata, spec, current, (models.ArticleDescriptor{}).WriteFieldValue)
			if err != nil {
				t.Fatal("narrowed input policy prevented initial projection", err)
			}
			if title, ok := initial["title"].AsString(); !ok || title != observed.Initial {
				t.Fatal("projected initial changed")
			}
			submitted := observed.Initial
			if observed.Name == "corrected" {
				submitted = "new"
			}
			instance, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {submitted}}), &current, formmodel.PostClean{})
			if err != nil {
				t.Fatal("stored initial prevented a narrowed form from binding", err)
			}
			bound := instance.BoundForm()
			codes := []string{}
			for _, failure := range bound.Form().Errors().All() {
				codes = append(codes, string(failure.Code()))
			}
			candidate, _ := bound.Candidate().String("title")
			if bound.Form().Valid() != observed.Valid || (len(bound.Form().Changed()) != 0) != observed.Changed || !slices.Equal(codes, observed.Codes) || candidate != observed.Candidate {
				t.Fatal("model initial/candidate result differs from native", bound.Form().Valid(), bound.Form().Changed(), codes, candidate)
			}
			prepared, err := instance.Prepare()
			if observed.Prepared == nil {
				if err == nil {
					t.Fatal("invalid unchanged value was prepared")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				value, err := prepared.Model()
				if err != nil || value.Title != *observed.Prepared || value.ID != 7 {
					t.Fatal("corrected typed preparation differs from native", err)
				}
			}
			if current.Title != observed.Initial {
				t.Fatal("binding or preparation changed caller's instance")
			}
		})
	}
}
