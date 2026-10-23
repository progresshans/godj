// Package cascadetest owns authored migration inputs shared by native tests.
// Expected outcomes and database observations remain in the independent tests.
package cascadetest

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func History(t testing.TB, nullable, oneToOne bool) ([]migrations.Migration, ir.Model, ir.Model, ir.Model) {
	t.Helper()
	field := schema.ForeignKey("owner", "OwnerID", schema.Target("cascadehistory", "owner"), schema.RelatedName("children"), schema.Protect)
	field.Nullable = nullable
	built, err := schema.Build(schema.Definition{AppLabel: "cascadehistory", Models: []schema.Model{
		{Name: "owner", GoName: "Owner"},
		{Name: "child", GoName: "Child", Fields: []schema.Field{
			field, schema.CharField("code", "Code", 32),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "owner_code", Fields: []string{"owner", "code"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parent, before := built.Models[0], built.Models[1]
	after := before.Clone()
	after.Fields[1].Relation.OnDelete = ir.DeleteCascade
	if oneToOne {
		after.Fields[1].Unique = true
		after.Fields[1].Relation.Cardinality = ir.RelationOneToOne
		after.Fields[1].Relation.Reverse.Name = "child"
	}
	initial := migrations.Migration{App: "cascadehistory", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "cascadehistory", Model: parent},
		migrations.CreateModel{AppLabel: "cascadehistory", Model: before},
	}}
	alter := migrations.Migration{App: initial.App, Name: "0002_cascade", Dependencies: []migrations.MigrationKey{initial.Key()},
		Operations: []migrations.Operation{migrations.AlterField{AppLabel: initial.App, ModelName: before.Name,
			Before: before.Fields[1], After: after.Fields[1]}}}
	return []migrations.Migration{initial, alter}, parent, before, after
}

func RequiredCycle(t testing.TB) (migrations.Migration, []ir.Model) {
	t.Helper()
	built, err := schema.Build(schema.Definition{AppLabel: "cascadecycle", Models: []schema.Model{
		{Name: "first", GoName: "First", Fields: []schema.Field{schema.ForeignKey("second", "SecondID", schema.Target("cascadecycle", "second"), schema.NoReverse(), schema.Cascade)}},
		{Name: "second", GoName: "Second", Fields: []schema.Field{schema.ForeignKey("first", "FirstID", schema.Target("cascadecycle", "first"), schema.NoReverse(), schema.Cascade)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	first, second := built.Models[0].Clone(), built.Models[1].Clone()
	first.Fields, second.Fields = first.Fields[:1], second.Fields[:1]
	initial := migrations.Migration{App: "cascadecycle", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "cascadecycle", Model: first}, migrations.CreateModel{AppLabel: "cascadecycle", Model: second},
		migrations.AddField{AppLabel: "cascadecycle", ModelName: "first", Field: built.Models[0].Fields[1]},
		migrations.AddField{AppLabel: "cascadecycle", ModelName: "second", Field: built.Models[1].Fields[1]},
	}}
	return initial, built.Models
}
