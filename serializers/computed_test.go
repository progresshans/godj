package serializers_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

type computedRow struct {
	title string
	state bool
}

func computedEncoder(t *testing.T) serializers.ModelEncoder[computedRow] {
	t.Helper()
	model := ir.Model{Fields: []ir.Field{{Name: "title", Kind: ir.FieldChar, MaxLength: 100}, {Name: "hidden", Kind: ir.FieldBoolean}}, ManyToMany: []ir.ManyToManyField{{Name: "members", Target: ir.ModelIdentity{AppLabel: "example", ModelName: "member"}, Symmetry: ir.ManyToManyDirected}}}
	spec, err := serializers.FromModel(model, serializers.ModelField{Name: "title"})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, model, func(row computedRow, field ir.Field) (query.Value, bool) {
		return query.String(row.title), field.Name == "title"
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoder
}

func TestComputedModelFieldsOwnProjectionAndRejectInput(t *testing.T) {
	base := computedEncoder(t)
	field, err := serializers.BooleanField("state", serializers.WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	fields := []serializers.ComputedField[computedRow]{{Field: field, Read: func(row computedRow) (serializers.Value, bool) { return serializers.Boolean(row.state), true }}}
	projection, err := base.WithComputed(fields...)
	if err != nil {
		t.Fatal(err)
	}
	fields[0].Read = func(computedRow) (serializers.Value, bool) { return serializers.Boolean(false), true }
	label, err := serializers.StringField("label", serializers.WithReadOnly(), serializers.WithMaxLength(10))
	if err != nil {
		t.Fatal(err)
	}
	extended, err := projection.WithComputed(serializers.ComputedField[computedRow]{Field: label, Read: func(row computedRow) (serializers.Value, bool) {
		return serializers.String(" " + row.title + " "), true
	}})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := projection.WithComputed(serializers.ComputedField[computedRow]{Field: label, Read: func(computedRow) (serializers.Value, bool) { return serializers.String("sibling"), true }})
	if err != nil {
		t.Fatal("derived projection mutated its parent's computed registry", err)
	}
	other, err := sibling.Encode(computedRow{"raw", true})
	if err != nil {
		t.Fatal(err)
	}
	otherObject, _ := other.AsObject()
	otherLabel, _ := otherObject.Get("label")
	if value, _ := otherLabel.AsString(); value != "sibling" {
		t.Fatal("sibling computed callback not independent")
	}
	detached := extended.Spec().Fields()
	detached[0] = label
	// Concurrent reuse must not share mutable projection state or retain a row.
	var workers sync.WaitGroup
	for n := range 12 {
		workers.Go(func() {
			row := computedRow{"raw", n%2 == 0}
			value, err := extended.Encode(row)
			if err != nil {
				t.Error(err)
				return
			}
			object, _ := value.AsObject()
			state, _ := object.Get("state")
			if got, ok := state.AsBoolean(); !ok || got != row.state {
				t.Error("computed snapshot or ownership lost")
			}
			text, _ := object.Get("label")
			if got, ok := text.AsString(); !ok || got != " raw " {
				t.Error("computed output was input-cleaned")
			}
		})
	}
	workers.Wait()
	if len(base.Spec().Fields()) != 1 || len(projection.Spec().Fields()) != 2 || len(extended.Spec().Fields()) != 3 || extended.Spec().Fields()[0].Name() != "title" {
		t.Fatal("derived encoder changed another projection")
	}
	result, err := extended.Spec().Bind(decodeObject(t, `{"title":"value","state":true}`), serializers.ModeFull)
	if err != nil || result.Valid() {
		t.Fatal("computed response field became writable", err)
	}
	baseValue, err := base.Encode(computedRow{"base", true})
	if err != nil {
		t.Fatal(err)
	}
	baseObject, _ := baseValue.AsObject()
	if _, exists := baseObject.Get("state"); exists {
		t.Fatal("computed field leaked into base")
	}
}

func TestComputedModelFieldsFailClosedWithoutPartialOutput(t *testing.T) {
	base := computedEncoder(t)
	for _, name := range []string{"title", "hidden", "members"} {
		field, err := serializers.BooleanField(name, serializers.WithReadOnly())
		if err != nil {
			t.Fatal(err)
		}
		_, err = base.WithComputed(serializers.ComputedField[computedRow]{Field: field, Read: func(computedRow) (serializers.Value, bool) { return serializers.Boolean(true), true }})
		if !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
			t.Fatal("computed field replaced stored or relation metadata", name, err)
		}
	}
	boolean, _ := serializers.BooleanField("state", serializers.WithReadOnly())
	writable, _ := serializers.BooleanField("state")
	valid := serializers.ComputedField[computedRow]{Field: boolean, Read: func(computedRow) (serializers.Value, bool) { return serializers.Boolean(true), true }}
	for _, fields := range [][]serializers.ComputedField[computedRow]{
		{{}}, {{Field: boolean}}, {{Field: writable, Read: valid.Read}}, {valid, valid},
	} {
		if _, err := base.WithComputed(fields...); err == nil {
			t.Fatal("invalid computed definition accepted")
		}
	}
	if _, err := (serializers.ModelEncoder[computedRow]{}).WithComputed(valid); err == nil {
		t.Fatal("zero model encoder accepted")
	}
	projection, err := base.WithComputed(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.WithComputed(valid); err == nil {
		t.Fatal("chained duplicate computed field accepted")
	}
	for _, test := range []struct {
		name    string
		value   serializers.Value
		present bool
	}{
		{"missing", serializers.Boolean(true), false}, {"zero", serializers.Value{}, true},
		{"null", serializers.Null(), true}, {"wrong_type", serializers.String("secret"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			field := valid
			field.Read = func(computedRow) (serializers.Value, bool) { return test.value, test.present }
			encoder, err := base.WithComputed(field)
			if err != nil {
				t.Fatal(err)
			}
			value, err := encoder.Encode(computedRow{"private title", true})
			if !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private title") {
				t.Fatal("computed failure was accepted or disclosed value", err)
			}
			if _, published := value.AsObject(); published {
				t.Fatal("partial representation published")
			}
		})
	}
}

func TestComputedModelFieldsApplyOutputConstraintsWithoutInputCoercion(t *testing.T) {
	base := computedEncoder(t)
	label, _ := serializers.StringField("derived", serializers.WithReadOnly(), serializers.WithMaxLength(2))
	nullable, _ := serializers.BooleanField("derived", serializers.WithReadOnly(), serializers.WithNullable())
	amount, _ := serializers.DecimalField("derived", 4, 2, serializers.WithReadOnly())
	list, _ := serializers.IntegerListField("derived", serializers.WithReadOnly())
	for _, test := range []struct {
		name    string
		field   serializers.Field
		value   serializers.Value
		allowed bool
	}{
		{"unicode_length", label, serializers.String("日本"), true}, {"too_long", label, serializers.String("日本語"), false},
		{"invalid_utf8", label, serializers.String("\xff"), false}, {"nul", label, serializers.String("\x00"), false},
		{"nullable", nullable, serializers.Null(), true}, {"no_bool_coercion", nullable, serializers.Integer(1), false},
		{"decimal_scale", amount, serializers.Decimal(mustComputedDecimal(t, "12.5")), true}, {"decimal_overflow", amount, serializers.Decimal(mustComputedDecimal(t, "123.4")), false},
		{"decimal_precision", amount, serializers.Decimal(mustComputedDecimal(t, "1.234")), false},
		{"collection", list, serializers.Integers(9, 3), true}, {"wrong_collection", list, serializers.String("9,3"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoder, err := base.WithComputed(serializers.ComputedField[computedRow]{Field: test.field, Read: func(computedRow) (serializers.Value, bool) { return test.value, true }})
			if err != nil {
				t.Fatal(err)
			}
			value, err := encoder.Encode(computedRow{"subject", true})
			if (err == nil) != test.allowed {
				t.Fatal("output constraint mismatch", err)
			}
			if test.name == "decimal_scale" {
				data, err := serializers.Encode(value, serializers.Limits{})
				if err != nil || !strings.Contains(string(data), `"derived":"12.50"`) {
					t.Fatal("computed decimal output lost declared scale", string(data), err)
				}
			}
		})
	}
}

func mustComputedDecimal(t *testing.T, text string) decimal.Decimal {
	t.Helper()
	value, err := decimal.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
