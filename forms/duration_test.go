package forms_test

import (
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/durationtest"
	"github.com/progresshans/godj/schema"
	"reflect"
	"strconv"
	"testing"
)

func TestDurationModelFormsAgainstPinnedDjango(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "dates", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.DurationField("elapsed", "Elapsed", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	same := duration.Duration{Days: 1, Microseconds: 7384123456}
	for index, observation := range durationtest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("elapsed", formmodel.WithRequired(observation.Required)))
			if err != nil {
				t.Fatal(err)
			}
			if field := spec.Fields()[0]; field.Widget() != forms.TextInput || !field.Nullable() || observation.Widget != "TextInput" {
				t.Fatal("time field presentation/presence changed")
			}
			data := map[string][]string{}
			for key, value := range observation.Input {
				data[key] = []string{value}
			}
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "zero": forms.Duration(duration.Duration{}), "same": forms.Duration(same)} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"elapsed": initial})
				if err != nil {
					t.Fatal(err)
				}
				codes := map[string][]string{}
				for _, v := range bound.Errors().All() {
					codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
				}
				if bound.Valid() != observation.Valid || !reflect.DeepEqual(codes, observation.Errors) {
					t.Fatalf("input %q: errors=%v want=%v", observation.Input, codes, observation.Errors)
				}
				got, present := bound.Cleaned().Get("elapsed")
				want, exists := observation.Cleaned["elapsed"]
				if present != exists {
					t.Fatal("invalid time leaked cleaned data")
				}
				if exists {
					if want == nil {
						if !got.IsNull() {
							t.Fatal("blank time became a value")
						}
					} else if time, ok := got.AsDuration(); !ok || time.String() != *want {
						t.Fatal("cleaned time differs")
					}
				}
				changed, exists := observation.Changed[name]
				if !exists || (len(bound.Changed()) == 1) != changed {
					t.Fatalf("changed from %s differs", name)
				}
			}
		})
	}
}
func TestDurationFormDefaultsAndInvalidValues(t *testing.T) {
	for _, value := range []forms.Value{forms.Null(), forms.Duration(duration.Duration{})} {
		field, err := forms.DurationField("elapsed", forms.WithNullable(), forms.WithRequired(false), forms.WithDefault(value))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		unbound, err := spec.Unbound(nil)
		actual, _ := unbound.Initial().Get("elapsed")
		if err != nil || !actual.Equal(value) {
			t.Fatal("time initial default changed")
		}
		bound, err := spec.Bind(forms.NewData(nil), nil)
		actual, _ = bound.Cleaned().Get("elapsed")
		if err != nil || !bound.Valid() || !actual.IsNull() {
			t.Fatal("time default overwrote bound omission")
		}
		bound, err = spec.Bind(forms.NewData(map[string][]string{"elapsed": {"12:34:56.123456", "12:34:56.654321"}}), nil)
		if err != nil || bound.Valid() || bound.Errors().Len() != 1 {
			t.Fatal("repeated time accepted")
		}
	}
	for _, value := range []forms.Value{forms.Duration(duration.Duration{Days: 1000000000}), forms.Duration(duration.Duration{Microseconds: -1}), forms.String("12:34:56.123456")} {
		if _, err := forms.DurationField("elapsed", forms.WithDefault(value)); err == nil {
			t.Fatal("invalid typed time default accepted")
		}
	}
}
