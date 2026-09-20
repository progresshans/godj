package forms_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/uuidtest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

func TestUUIDModelFormsAgainstPinnedDjango(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.UUIDField("reference", "Reference", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	sample, err := uuid.Parse("12345678-9abc-4def-8123-456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	for index, observed := range uuidtest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(built.Models[0], formmodel.OverrideField("reference", formmodel.WithRequired(observed.Required)))
			if err != nil {
				t.Fatal(err)
			}
			field := spec.Fields()[0]
			if field.Widget() != forms.TextInput || field.MaxLength() != 0 || observed.Widget != "TextInput" || len(observed.WidgetAttrs) != 0 {
				t.Fatal("UUID Form borrowed physical storage width")
			}
			data := map[string][]string{}
			for key, value := range observed.Input {
				data[key] = []string{value}
			}
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "zero": forms.UUID(uuid.UUID{}), "same": forms.UUID(sample)} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"reference": initial})
				if err != nil {
					t.Fatal(err)
				}
				codes := map[string][]string{}
				for _, violation := range bound.Errors().All() {
					codes[string(violation.Field())] = append(codes[string(violation.Field())], string(violation.Code()))
				}
				if bound.Valid() != observed.Valid || !reflect.DeepEqual(codes, observed.Errors) {
					t.Fatalf("input %q codes=%v want %v", observed.Input, codes, observed.Errors)
				}
				got, present := bound.Cleaned().Get("reference")
				want, exists := observed.Cleaned["reference"]
				if present != exists {
					t.Fatal("invalid UUID leaked cleaned value")
				}
				if exists {
					if want == nil {
						if !got.IsNull() {
							t.Fatal("empty UUID lost NULL")
						}
					} else if identifier, ok := got.AsUUID(); !ok || identifier != want.UUID(t) {
						t.Fatal("cleaned UUID value changed")
					}
				}
				changed, exists := observed.Changed[name]
				if !exists || (len(bound.Changed()) == 1) != changed {
					t.Fatalf("UUID change from %s differs for %q", name, observed.Input)
				}
			}
		})
	}
}

func TestUUIDFormDefaultsInitialOwnershipAndConstraints(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.UUIDField("reference", "Reference", schema.Nullable(), schema.Default(uuid.UUID{}))}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := built.Models[0]
	spec, err := formmodel.NewSpec(metadata)
	if err != nil {
		t.Fatal(err)
	}
	value := uuid.UUID{0: 0x80, 15: 1}
	initial, err := formmodel.InitialValues(metadata, spec, value, func(v uuid.UUID, field ir.Field) (query.Value, bool) {
		field.Default.UUID = "changed"
		return query.UUID(v), true
	})
	if err != nil {
		t.Fatal(err)
	}
	value[0] = 0
	unbound, err := spec.Unbound(initial)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := unbound.Initial().UUID("reference")
	if !ok || got[0] != 0x80 {
		t.Fatal("UUID initial retained caller storage")
	}
	if metadata.Fields[1].Default.UUID != (uuid.UUID{}).String() {
		t.Fatal("UUID initial callback mutated metadata")
	}
	defaults, err := spec.Unbound(nil)
	if err != nil {
		t.Fatal(err)
	}
	if zero, ok := defaults.Initial().UUID("reference"); !ok || zero != (uuid.UUID{}) {
		t.Fatal("zero UUID default became absent")
	}
	if _, err := spec.Unbound(map[string]forms.Value{"reference": forms.String(got.String())}); err == nil {
		t.Fatal("string bypassed typed UUID initial")
	}
	for _, options := range [][]forms.FieldOption{{nil}, {forms.WithRequired(false)}, {forms.WithMaxLength(32)}, {forms.WithWidget(forms.NumberInput)}, {forms.WithDefault(forms.String(got.String()))}} {
		if _, err := forms.UUIDField("reference", options...); err == nil {
			t.Fatal("unsupported UUID Form configuration accepted")
		}
	}
}
