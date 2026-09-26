package forms_test

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/calendardatetest"
	"github.com/progresshans/godj/schema"
	"reflect"
	"strconv"
	"testing"
)

func TestCalendarDateModelFormsAgainstPinnedDjango(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "dates", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.DateField("day", "Day", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	same := calendar.Date{Year: 2026, Month: 9, Day: 20}
	for index, observation := range calendardatetest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("day", formmodel.WithRequired(observation.Required)))
			if err != nil {
				t.Fatal(err)
			}
			if field := spec.Fields()[0]; field.Widget() != forms.DateInput || !field.Nullable() || observation.Widget != "DateInput" {
				t.Fatal("date field presentation/presence changed")
			}
			data := map[string][]string{}
			for key, value := range observation.Input {
				data[key] = []string{value}
			}
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "same": forms.Date(same)} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"day": initial})
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
				got, present := bound.Cleaned().Get("day")
				want, exists := observation.Cleaned["day"]
				if present != exists {
					t.Fatal("invalid date leaked cleaned data")
				}
				if exists {
					if want == nil {
						if !got.IsNull() {
							t.Fatal("blank date became a value")
						}
					} else if date, ok := got.AsDate(); !ok || date.String() != *want {
						t.Fatal("cleaned date differs")
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
func TestCalendarDateFormDefaultsAndInvalidValues(t *testing.T) {
	for _, value := range []forms.Value{forms.Null(), forms.Date(calendar.Date{Year: 1, Month: 1, Day: 1})} {
		field, err := forms.DateField("day", forms.WithNullable(), forms.WithRequired(false), forms.WithDefault(value))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		unbound, err := spec.Unbound(nil)
		actual, _ := unbound.Initial().Get("day")
		if err != nil || !actual.Equal(value) {
			t.Fatal("date initial default changed")
		}
		bound, err := spec.Bind(forms.NewData(nil), nil)
		actual, _ = bound.Cleaned().Get("day")
		if err != nil || !bound.Valid() || !actual.IsNull() {
			t.Fatal("date default overwrote bound omission")
		}
		bound, err = spec.Bind(forms.NewData(map[string][]string{"day": {"2026-09-20", "2026-09-21"}}), nil)
		if err != nil || bound.Valid() || bound.Errors().Len() != 1 {
			t.Fatal("repeated date accepted")
		}
	}
	for _, value := range []forms.Value{forms.Date(calendar.Date{}), forms.Date(calendar.Date{Year: 1900, Month: 2, Day: 29}), forms.String("2026-09-20")} {
		if _, err := forms.DateField("day", forms.WithDefault(value)); err == nil {
			t.Fatal("invalid typed date default accepted")
		}
	}
}
