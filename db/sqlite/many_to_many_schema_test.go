package sqlite

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteManyToManyDeclarationCannotSilentlyCreateOnlyOwner(t *testing.T) {
	model := ir.Model{Name: "owner", GoName: "Owner", DBTable: "owners", Fields: []ir.Field{{Name: "id", Column: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}}, ManyToMany: []ir.ManyToManyField{{Name: "labels", GoName: "Labels"}}}
	if sql, err := compileMigrationCreateModel(model); err == nil || sql != "" {
		t.Fatal("ordinary create dropped M2M metadata", err)
	}
	if sql, err := compileSQLiteRelationCreateModel(model, nil); err == nil || sql != "" {
		t.Fatal("relation create dropped M2M metadata", err)
	}
}
