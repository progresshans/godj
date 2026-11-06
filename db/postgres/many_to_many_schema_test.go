package postgres

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresManyToManyDeclarationCannotSilentlyCreateOnlyOwner(t *testing.T) {
	model := ir.Model{Name: "owner", GoName: "Owner", DBTable: "owners", Fields: []ir.Field{{Name: "id", Column: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}}, ManyToMany: []ir.ManyToManyField{{Name: "labels", GoName: "Labels"}}}
	if statements, err := compilePostgresMigrationCreateModel("app", model, nil); err == nil || statements != nil {
		t.Fatal("create dropped M2M metadata", err)
	}
}
