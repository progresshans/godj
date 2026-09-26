package model_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
)

func TestTextFormsAgainstPinnedDjangoObservations(t *testing.T) {
	data, err := os.ReadFile("testdata/text-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var observations struct {
		Django string `json:"django"`
		Cases  []struct {
			Kind     string   `json:"kind"`
			Nullable bool     `json:"nullable"`
			Required bool     `json:"required"`
			Input    string   `json:"input"`
			Value    *string  `json:"value"`
			Codes    []string `json:"codes"`
			Widget   string   `json:"widget"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &observations); err != nil {
		t.Fatal(err)
	}
	if observations.Django != "6.1" || len(observations.Cases) == 0 {
		t.Fatal("pinned Text observations are missing")
	}
	for index, observation := range observations.Cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			options := []schema.FieldOption{}
			if observation.Nullable {
				options = append(options, schema.Nullable())
			}
			field := schema.CharField("value", "Value", 40, options...)
			if observation.Kind == "text" {
				field = schema.TextField("value", "Value", options...)
			} else if observation.Kind != "char" {
				t.Fatal("unknown reference field")
			}
			model, err := schema.Build(schema.Definition{AppLabel: "notes", Models: []schema.Model{{Name: "note", GoName: "Note", Fields: []schema.Field{field}}}})
			if err != nil {
				t.Fatal(err)
			}
			spec, err := formmodel.NewSpec(model.Models[0], formmodel.OverrideField("value", formmodel.WithRequired(observation.Required)))
			if err != nil {
				t.Fatal(err)
			}
			widget := "TextInput"
			if spec.Fields()[0].Widget() == forms.Textarea {
				widget = "Textarea"
			}
			if widget != observation.Widget {
				t.Fatal("model widget differs from reference")
			}
			form, err := spec.Bind(forms.NewData(map[string][]string{"value": {observation.Input}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			codes := make([]string, 0)
			for _, failure := range form.Errors().All() {
				codes = append(codes, string(failure.Code()))
			}
			if !reflect.DeepEqual(codes, observation.Codes) {
				t.Fatalf("input %q: codes %v, want %v", observation.Input, codes, observation.Codes)
			}
			value, present := form.Cleaned().Get("value")
			if len(codes) > 0 {
				if present || form.Valid() {
					t.Fatal("invalid Text published cleaned data")
				}
				return
			}
			if !present || !form.Valid() {
				t.Fatal("valid Text omitted cleaned data")
			}
			if observation.Value == nil {
				if !value.IsNull() {
					t.Fatal("reference null became string")
				}
				return
			}
			if text, ok := value.AsString(); !ok || text != *observation.Value {
				t.Fatal("Text cleaning lost empty, multiline, or long data")
			}
		})
	}
}
