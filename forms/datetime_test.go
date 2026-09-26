package forms_test

import (
	"encoding/json"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/schema"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDateTimeFormsAgainstPinnedDjangoUTCObservations(t *testing.T) {
	data, err := os.ReadFile("testdata/datetime-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var observations struct {
		Django, Timezone string
		Cases            []struct {
			Required bool
			Input    string
			UTC      *string
			Codes    []string
			Widget   string
		}
	}
	if err := json.Unmarshal(data, &observations); err != nil {
		t.Fatal(err)
	}
	if observations.Django != "6.1" || observations.Timezone != "UTC" || len(observations.Cases) != 48 {
		t.Fatal("datetime reference roster incomplete")
	}
	for index, observation := range observations.Cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			model, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.DateTimeField("value", "Value", schema.Nullable())}}}})
			if err != nil {
				t.Fatal(err)
			}
			spec, err := formmodel.NewSpec(model.Models[0], formmodel.OverrideField("value", formmodel.WithRequired(observation.Required)))
			if err != nil {
				t.Fatal(err)
			}
			if observation.Widget != "DateTimeInput" || spec.Fields()[0].Widget() != forms.DateTimeInput {
				t.Fatal("model datetime widget mismatch")
			}
			bound, err := spec.Bind(forms.NewData(map[string][]string{"value": {observation.Input}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			codes := []string{}
			for _, violation := range bound.Errors().All() {
				codes = append(codes, string(violation.Code()))
			}
			// DEV-0011: the pinned Python parser stops at a trailing NUL.
			// GoDj rejects NUL instead of silently accepting a truncated input.
			// Keep the real reference result intact; this is not a parity pass.
			if strings.ContainsRune(observation.Input, '\x00') {
				if len(observation.Codes) != 0 || observation.UTC == nil || bound.Valid() || !reflect.DeepEqual(codes, []string{"invalid"}) {
					t.Fatal("NUL rejection differs from the documented deviation")
				}
				return
			}
			if !reflect.DeepEqual(codes, observation.Codes) || bound.Valid() != (len(codes) == 0) {
				t.Fatalf("datetime errors = %v, want %v", codes, observation.Codes)
			}
			if !bound.Valid() {
				return
			}
			value, _ := bound.Cleaned().Get("value")
			if observation.UTC == nil {
				if !value.IsNull() {
					t.Fatal("empty datetime became a value")
				}
				return
			}
			instant, ok := value.AsDateTime()
			if !ok || temporal.Format(instant) != *observation.UTC {
				t.Fatalf("UTC value differs: %v", instant)
			}
		})
	}
}

func TestDateTimeFormInitialChangeAndInvalidConfiguration(t *testing.T) {
	instant := time.Date(2026, 9, 19, 3, 34, 56, 123456000, time.UTC)
	field, err := forms.DateTimeField("at", forms.WithNullable(), forms.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"at": {"2026-09-19T12:34:56.123456+09:00"}}), map[string]forms.Value{"at": forms.DateTime(instant)})
	if err != nil || !bound.Valid() || len(bound.Changed()) != 0 {
		t.Fatal("offset-only datetime change was treated as a different instant")
	}
	cleared, err := spec.Bind(forms.NewData(nil), map[string]forms.Value{"at": forms.DateTime(instant)})
	value, _ := cleared.Cleaned().Get("at")
	if err != nil || !cleared.Valid() || !value.IsNull() || len(cleared.Changed()) != 1 {
		t.Fatal("empty datetime did not clear a nullable value")
	}
	for _, options := range [][]forms.FieldOption{{forms.WithRequired(false)}, {forms.WithMaxLength(10)}, {forms.WithWidget(forms.Textarea)}, {forms.WithDefault(forms.String("now"))}, {forms.WithDefault(forms.DateTime(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)))}} {
		if _, err := forms.DateTimeField("at", options...); err == nil {
			t.Fatal("invalid datetime form config accepted")
		}
	}
}
