package forms_test

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/forms"
)

func TestIntegerFormAgainstPinnedDjangoObservations(t *testing.T) {
	data, err := os.ReadFile("testdata/integer-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var observations struct {
		Django string `json:"django"`
		Cases  []struct {
			Input    string   `json:"input"`
			Required bool     `json:"required"`
			Value    *string  `json:"value"`
			Codes    []string `json:"codes"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &observations); err != nil {
		t.Fatal(err)
	}
	if observations.Django != "6.1" || len(observations.Cases) == 0 {
		t.Fatal("pinned integer observations are missing")
	}
	for index, observation := range observations.Cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			field, err := forms.IntegerField("value", forms.WithRequired(observation.Required), forms.WithNullable())
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			bound, err := spec.Bind(forms.NewData(map[string][]string{"value": {observation.Input}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			codes := make([]string, 0)
			for _, failure := range bound.Errors().All() {
				codes = append(codes, string(failure.Code()))
			}
			if !reflect.DeepEqual(codes, observation.Codes) || bound.Valid() != (len(codes) == 0) {
				t.Fatalf("input %q: codes %v, want %v", observation.Input, codes, observation.Codes)
			}
			actual, found := bound.Cleaned().Get("value")
			if len(codes) != 0 {
				if found {
					t.Fatal("invalid integer published cleaned data")
				}
				return
			}
			if !found {
				t.Fatal("valid integer omitted cleaned data")
			}
			if observation.Value == nil {
				if !actual.IsNull() {
					t.Fatal("empty integer did not remain null")
				}
			} else if value, ok := actual.AsInteger(); !ok || strconv.FormatInt(value, 10) != *observation.Value {
				t.Fatalf("input %q: wrong cleaned integer", observation.Input)
			}
		})
	}
}

func TestIntegerFormInitialChangedAndInvalidConfiguration(t *testing.T) {
	field, err := forms.IntegerField("value", forms.WithDefault(forms.Integer(math.MaxInt64)))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	form, err := spec.Unbound(nil)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := form.Initial().Integer("value"); !ok || value != math.MaxInt64 {
		t.Fatal("integer default was lost")
	}
	form, err = spec.Bind(forms.NewData(map[string][]string{"value": {"+000.00"}}), map[string]forms.Value{"value": forms.Integer(0)})
	if err != nil || !form.Valid() || len(form.Changed()) != 0 {
		t.Fatalf("zero was not preserved: %v", err)
	}
	if value, ok := form.Cleaned().Integer("value"); !ok || value != 0 {
		t.Fatal("required zero was treated as missing")
	}
	for _, input := range [][]string{nil, {}, {""}, {"1", "2"}, {string([]byte{0xff})}} {
		bound, err := spec.Bind(forms.NewData(map[string][]string{"value": input}), nil)
		if err != nil || bound.Valid() || bound.Errors().Empty() {
			t.Fatal("invalid or missing integer accepted")
		}
	}
	for _, options := range [][]forms.FieldOption{
		{forms.WithDefault(forms.String("1"))}, {forms.WithDefault(forms.Boolean(false))},
		{forms.WithMaxLength(2)}, {forms.WithRequired(false)}, {nil},
	} {
		if _, err := forms.IntegerField("value", options...); err == nil {
			t.Fatal("invalid integer configuration accepted")
		}
	}
	if _, err := spec.Unbound(map[string]forms.Value{"value": forms.String("1")}); err == nil {
		t.Fatal("string initial value accepted")
	}
}
