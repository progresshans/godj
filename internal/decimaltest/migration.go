package decimaltest

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

// PrecisionHistory is a nullable Decimal(5,2) -> Decimal(7,3) migration.
// The database tests inject external values directly into this public history.
func PrecisionHistory(t testing.TB) (migrations.LoadedDefinitionSet, ir.Model, ir.Model) {
	t.Helper()
	built, err := schema.Build(schema.Definition{AppLabel: "decimalref", Models: []schema.Model{{Name: "cost", GoName: "Cost", Fields: []schema.Field{schema.DecimalField("value", "Value", 5, 2, schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	before, after := built.Models[0], built.Models[0].Clone()
	after.Fields[1].Decimal = &ir.DecimalSpec{MaxDigits: 7, DecimalPlaces: 3}
	changes := []migrations.Migration{
		{App: "decimalref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "decimalref", Model: before}}},
		{App: "decimalref", Name: "0002_precision", Dependencies: []migrations.MigrationKey{{App: "decimalref", Name: "0001_initial"}}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "decimalref", ModelName: "cost", Before: before.Fields[1], After: after.Fields[1]}}},
	}
	var sources []definition.Source
	for _, change := range changes {
		wire, err := definition.Encode(definition.Producer{Name: "decimal-precision-test", Version: "1"}, change)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: change.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, before, after
}

// MixedPrecisionSQLRequest keeps a physical AddField between two different
// AlterField kinds so metadata-only output cannot shift operation positions.
func MixedPrecisionSQLRequest(t testing.TB) mb.ForwardMigrationSQLRequest {
	t.Helper()
	_, before, after := PrecisionHistory(t)
	added := after.Clone()
	note := ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}
	added.Fields = append(added.Fields, note)
	chosen := added.Clone()
	chosen.Fields[2].Choices = []ir.Choice{schema.Choice("a", "A")}
	return mb.ForwardMigrationSQLRequest{App: "decimalref", Name: "0002_mixed", Intent: mb.MigrationIntent{Operations: []mb.MigrationOperation{
		{OperationIndex: 0, Kind: mb.MigrationAlterField, Before: before, After: after},
		{OperationIndex: 1, Kind: mb.MigrationAddField, Before: after, After: added},
		{OperationIndex: 2, Kind: mb.MigrationAlterField, Before: added, After: chosen},
	}}}
}
