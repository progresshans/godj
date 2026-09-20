package migrations

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func decimalPrecisionDefinitions() []Migration {
	model := ir.Model{Name: "cost", GoName: "Cost", DBTable: "costs_cost", Fields: []ir.Field{
		{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "value", GoName: "Value", Column: "value", Kind: ir.FieldDecimal, Nullable: true, Decimal: &ir.DecimalSpec{MaxDigits: 5, DecimalPlaces: 2}},
		{Name: "label", GoName: "Label", Column: "label", Kind: ir.FieldChar, MaxLength: 32},
	}}
	before, after := model.Fields[1].Clone(), model.Fields[1].Clone()
	after.Decimal = &ir.DecimalSpec{MaxDigits: 7, DecimalPlaces: 3}
	oldLabel, newLabel := model.Fields[2].Clone(), model.Fields[2].Clone()
	newLabel.Choices = []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "a"}, Label: "A"}}
	return []Migration{
		{App: "costs", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "costs", Model: model}}},
		{App: "costs", Name: "0002_mixed", Dependencies: []MigrationKey{{App: "costs", Name: "0001_initial"}}, Operations: []Operation{
			AlterField{AppLabel: "costs", ModelName: "cost", Before: before, After: after},
			AddField{AppLabel: "costs", ModelName: "cost", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}},
			AlterField{AppLabel: "costs", ModelName: "cost", Before: oldLabel, After: newLabel},
		}},
	}
}

func TestSQLProjectionRetainsMetadataSlotsAndRejectsMissingPhysicalOperations(t *testing.T) {
	loaded := testLoadedDefinitionSet(t, decimalPrecisionDefinitions())
	target := MigrationKey{App: "costs", Name: "0002_mixed"}
	for _, test := range []struct {
		name        string
		slots, want []string
	}{
		{"sqlite", []string{"", "ALTER TABLE costs_cost ADD COLUMN note TEXT", ""}, []string{"ALTER TABLE costs_cost ADD COLUMN note TEXT"}},
		{"postgres", []string{"ALTER TABLE costs_cost ALTER COLUMN value TYPE NUMERIC(7,3)", "ALTER TABLE costs_cost ADD COLUMN note TEXT", ""}, []string{"ALTER TABLE costs_cost ALTER COLUMN value TYPE NUMERIC(7,3)", "ALTER TABLE costs_cost ADD COLUMN note TEXT"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			renderer := &migrationSQLRendererSpy{statements: test.slots}
			statements, err := RenderMigrationSQL(context.Background(), loaded, target, renderer)
			if err != nil || !reflect.DeepEqual(statements, test.want) || renderer.calls != 1 {
				t.Fatalf("ordered SQL projection: %v %v", statements, err)
			}
		})
	}
	for _, slots := range [][]string{
		{"ALTER TABLE costs_cost ALTER COLUMN value TYPE NUMERIC(7,3)", "", ""},
		{"", "ALTER TABLE costs_cost ADD COLUMN note TEXT", "UNEXPECTED SQL FOR CHOICES"},
		{"ALTER TABLE costs_cost ALTER COLUMN value TYPE NUMERIC(7,3)", "ALTER TABLE costs_cost ADD COLUMN note TEXT"},
		{"", "ALTER TABLE costs_cost ADD COLUMN note TEXT"},
		{"", "ALTER TABLE costs_cost ADD COLUMN note TEXT", "", "EXTRA"},
	} {
		result, err := RenderMigrationSQL(context.Background(), loaded, target, &migrationSQLRendererSpy{statements: slots})
		if result != nil || err == nil {
			t.Fatal("missing, shifted or extra operation slot was published")
		}
		assertMigrationSQLError(t, err, CategorySQLRender, CodeInvalidRenderedSQL, target)
	}
}
