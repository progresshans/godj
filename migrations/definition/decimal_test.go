package definition_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestDecimalPrecisionAlterFieldRetainsHistoricalBeforeAfterAndDefaultBounds(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "costs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.DecimalField("cost", "Cost", 5, 2, schema.Nullable(), schema.Default(decimal.Decimal{Coefficient: "123", Exponent: -2}))}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := built.Models[0]
	before, after := model.Fields[1].Clone(), model.Fields[1].Clone()
	after.Decimal = &ir.DecimalSpec{MaxDigits: 7, DecimalPlaces: 3}
	producer := definition.Producer{Name: "decimal-migration-test", Version: "1"}
	initial := migrations.Migration{App: "costs", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "costs", Model: model}}}
	change := migrations.Migration{App: "costs", Name: "0002_precision", Dependencies: []migrations.MigrationKey{{App: "costs", Name: initial.Name}}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "costs", ModelName: "record", Before: before, After: after}}}
	first, err := definition.Encode(producer, initial)
	if err != nil {
		t.Fatal(err)
	}
	second, err := definition.Encode(producer, change)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "initial", Document: first}, definition.Source{SourceID: "precision", Document: second})
	if err != nil {
		t.Fatal(err)
	}
	operation := loaded.Definitions()[1].Operations[0].(migrations.AlterField)
	if !operation.Before.Equal(before) || !operation.After.Equal(after) {
		t.Fatal("historical precision transition changed")
	}
	again, err := definition.Encode(producer, loaded.Definitions()[1])
	if err != nil || !bytes.Equal(second, again) {
		t.Fatal("precision change encoding drift", err)
	}
	operation.Before.Decimal.MaxDigits = 8
	operation.After.Decimal.DecimalPlaces = 4
	fresh := loaded.Definitions()[1].Operations[0].(migrations.AlterField)
	if fresh.Before.Decimal.MaxDigits != 5 || fresh.After.Decimal.DecimalPlaces != 3 {
		t.Fatal("loaded precision aliases caller")
	}
	for _, mutate := range []func(*migrations.AlterField){
		func(op *migrations.AlterField) { op.After.Decimal.DecimalPlaces = 1 }, // unchanged default would need rounding
		func(op *migrations.AlterField) { op.After.Nullable = false },
		func(op *migrations.AlterField) { op.After.Column = "renamed" },
	} {
		invalid := migrations.AlterField{AppLabel: "costs", ModelName: "record", Before: before.Clone(), After: after.Clone()}
		mutate(&invalid)
		bad := change
		bad.Operations = []migrations.Operation{invalid}
		if _, err := definition.Encode(producer, bad); err == nil {
			t.Fatal("unsafe precision transition encoded")
		}
	}
}

func TestDecimalHistoricalDefaultsAndPrecisionRoundTrip(t *testing.T) {
	for _, raw := range []string{"0", "-0", "1.23", "-999.99"} {
		value, err := decimal.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		model, err := schema.Build(schema.Definition{AppLabel: "costs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.DecimalField("cost", "Cost", 5, 2, schema.Nullable(), schema.Default(value))}}}})
		if err != nil {
			t.Fatal(err)
		}
		producer := definition.Producer{Name: "decimal-test", Version: "1"}
		migration := migrations.Migration{App: "costs", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "costs", Model: model.Models[0]}}}
		encoded, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "decimal", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		again, err := definition.Encode(producer, loaded.Definitions()[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("decimal historical round trip changed identity")
		}
		for _, replacement := range []string{"1.230", "01.23", "1e0", "NaN", "1000", "1.001"} {
			bad := bytes.Replace(encoded, []byte(`"decimal":"`+raw+`"`), []byte(`"decimal":"`+replacement+`"`), 1)
			if bytes.Equal(bad, encoded) {
				t.Fatal("negative control did not modify default")
			}
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid-value", Document: bad}); err == nil {
				t.Fatalf("invalid decimal default %s accepted", replacement)
			}
		}
		for _, change := range [][2]string{
			{`"max_digits":5`, `"max_digits":0`}, {`"max_digits":5`, `"max_digits":1001`},
			{`"decimal_places":2`, `"decimal_places":6`}, {`"decimal_places":2`, `"decimal_places":-1`},
			{`"decimal_places":2`, `"decimal_places":2.0`}, {`"max_digits":5`, `"max_digits":"5"`},
			{`"max_digits":5`, `"max_digits":5,"unexpected":1`},
		} {
			bad := bytes.Replace(encoded, []byte(change[0]), []byte(change[1]), 1)
			if bytes.Equal(bad, encoded) {
				t.Fatal("negative control did not modify precision")
			}
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid-precision", Document: bad}); err == nil {
				t.Fatalf("invalid precision %s accepted", change[1])
			}
		}
		operation := loaded.Definitions()[0].Operations[0].(migrations.CreateModel)
		operation.Model.Fields[1].Decimal.MaxDigits = 6
		fresh := loaded.Definitions()[0].Operations[0].(migrations.CreateModel)
		if fresh.Model.Fields[1].Decimal.MaxDigits != 5 {
			t.Fatal("loaded historical precision aliases caller")
		}
		changed, err := definition.Encode(producer, migrations.Migration{App: "costs", Name: "0001_initial", Operations: []migrations.Operation{operation}})
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := definition.Load(definition.Source{SourceID: "changed", Document: changed})
		if err != nil || other.Digest() == loaded.Digest() {
			t.Fatal("historical digest ignored decimal precision", err)
		}
	}
}
