package backend_test

import (
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestNamedUniqueAlterFieldRejectsChangedConstraintMetadata(t *testing.T) {
	before := ir.Model{Name: "label", GoName: "Label", DBTable: "label", Fields: []ir.Field{
		{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "value", GoName: "Value", Column: "value", Kind: ir.FieldInteger},
	}, UniqueConstraints: []ir.UniqueConstraint{{Name: "scope", Fields: []string{"value"}}}}
	after := before.Clone()
	after.Fields[1].Unique = true
	if _, _, kind, err := backend.ChangedField(before, after); err != nil || kind != ir.ChangeUnique {
		t.Fatal("ordinary column uniqueness change failed", err)
	}
	after.UniqueConstraints[0].Fields[0] = "id"
	if _, _, _, err := backend.ChangedField(before, after); err == nil {
		t.Fatal("AlterField silently changed a different named constraint")
	}
}
