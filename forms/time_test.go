package forms_test

import (
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/clocktimetest"
	"github.com/progresshans/godj/schema"
	"reflect"
	"strconv"
	"testing"
)

func TestClockTimeModelFormsAgainstPinnedDjango(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "dates", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.TimeField("at", "At", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	same := clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 123456}
	for index, observation := range clocktimetest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("at", formmodel.WithRequired(observation.Required)))
			if err != nil {
				t.Fatal(err)
			}
			if field := spec.Fields()[0]; field.Widget() != forms.TimeInput || !field.Nullable() || observation.Widget != "MicrosecondTimeInput" || !observation.SupportsMicroseconds {
				t.Fatal("time field presentation/presence changed")
			}
			data := map[string][]string{}
			for key, value := range observation.Input {
				data[key] = []string{value}
			}
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "same": forms.Time(same)} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"at": initial})
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
				got, present := bound.Cleaned().Get("at")
				want, exists := observation.Cleaned["at"]
				if present != exists {
					t.Fatal("invalid time leaked cleaned data")
				}
				if exists {
					if want == nil {
						if !got.IsNull() {
							t.Fatal("blank time became a value")
						}
					} else if time, ok := got.AsTime(); !ok || time.String() != *want {
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
func TestClockTimeFormDefaultsAndInvalidValues(t *testing.T) {
	for _, value := range []forms.Value{forms.Null(), forms.Time(clock.Time{})} {
		field, err := forms.TimeField("at", forms.WithNullable(), forms.WithRequired(false), forms.WithDefault(value))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		unbound, err := spec.Unbound(nil)
		actual, _ := unbound.Initial().Get("at")
		if err != nil || !actual.Equal(value) {
			t.Fatal("time initial default changed")
		}
		bound, err := spec.Bind(forms.NewData(nil), nil)
		actual, _ = bound.Cleaned().Get("at")
		if err != nil || !bound.Valid() || !actual.IsNull() {
			t.Fatal("time default overwrote bound omission")
		}
		bound, err = spec.Bind(forms.NewData(map[string][]string{"at": {"12:34:56.123456", "12:34:56.654321"}}), nil)
		if err != nil || bound.Valid() || bound.Errors().Len() != 1 {
			t.Fatal("repeated time accepted")
		}
	}
	for _, value := range []forms.Value{forms.Time(clock.Time{Hour: 24}), forms.Time(clock.Time{Second: 60}), forms.String("12:34:56.123456")} {
		if _, err := forms.TimeField("at", forms.WithDefault(value)); err == nil {
			t.Fatal("invalid typed time default accepted")
		}
	}
}
