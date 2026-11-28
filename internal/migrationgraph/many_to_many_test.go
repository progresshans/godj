package migrationgraph_test

import (
	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/internal/migrationgraph"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestManyToManyGraphSealsSelectedThroughAndRejectsUnrelatedDeltas(t *testing.T) {
	_, models, field := manytomanytest.History(t, true, false, false)
	before := models[0].Clone()
	after := before.Clone()
	after.ManyToMany = []ir.ManyToManyField{field}
	operation := migrationgraph.MigrationOperation{Kind: migrationgraph.MigrationAlterManyToMany, Before: before, After: after, RelatedModels: []migrationgraph.MigrationModel{{AppLabel: "manyhistory", Model: models[1]}, {AppLabel: "manyhistory", Model: models[2]}}}
	if _, _, err := operation.ChangedManyToMany(); err != nil {
		t.Fatal(err)
	}
	graph, err := migrationgraph.ResolveMigrationGraph("manyhistory", operation)
	if err != nil || len(graph.Models()) != 3 {
		t.Fatal("columnless source lost its complete through graph", err)
	}
	for _, mutate := range []func(*migrationgraph.MigrationOperation){
		func(op *migrationgraph.MigrationOperation) { op.After.Fields[0].Column = "other" },
		func(op *migrationgraph.MigrationOperation) { op.After.DBTable = "other" },
		func(op *migrationgraph.MigrationOperation) {
			op.After.UniqueConstraints = []ir.UniqueConstraint{{Name: "new", Fields: []string{"id"}}}
		},
		func(op *migrationgraph.MigrationOperation) {
			op.After.ManyToMany = append(op.After.ManyToMany, field.Clone())
		},
	} {
		bad := (migrationgraph.MigrationIntent{Operations: []migrationgraph.MigrationOperation{operation}}).Clone().Operations[0]
		mutate(&bad)
		if _, _, err := bad.ChangedManyToMany(); err == nil {
			t.Fatal("metadata mutation smuggled another change")
		}
	}
	bad := (migrationgraph.MigrationIntent{Operations: []migrationgraph.MigrationOperation{operation}}).Clone().Operations[0]
	bad.After.ManyToMany[0].Through.TargetField = "note"
	if _, err := migrationgraph.ResolveMigrationGraph("manyhistory", bad); err == nil {
		t.Fatal("through field selection was not checked")
	}
	renamed := after.Clone()
	renamed.ManyToMany[0].Name = "renamed"
	renamed.ManyToMany[0].GoName = "Renamed"
	// Stored field/constraint editors must not smuggle a columnless rename.
	renamed.Fields = append(renamed.Fields, ir.Field{Name: "extra", GoName: "Extra", Column: "extra", Kind: ir.FieldText, Nullable: true})
	if _, err := (migrationgraph.MigrationOperation{Kind: migrationgraph.MigrationAddField, Before: after, After: renamed}).ChangedField(); err == nil {
		t.Fatal("AddField omitted retained relation meaning")
	}
	renamed.Fields = renamed.Fields[:len(after.Fields)]
	renamed.UniqueConstraints = []ir.UniqueConstraint{{Name: "new", Fields: []string{"id"}}}
	if _, err := (migrationgraph.MigrationOperation{Kind: migrationgraph.MigrationAddConstraint, Before: after, After: renamed}).ChangedConstraint(); err == nil {
		t.Fatal("AddConstraint omitted retained relation meaning")
	}
	renamed.UniqueConstraints = nil
	renamed.Fields[0].GoName = "Different"
	if _, _, _, err := mb.ChangedField(after, renamed); err == nil {
		t.Fatal("AlterField omitted retained relation meaning")
	}
}
