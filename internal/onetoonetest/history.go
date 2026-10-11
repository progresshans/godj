// Package onetoonetest owns shared authored migration inputs, never expected
// results or backend observations.
package onetoonetest

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func History(t testing.TB, unique bool) ([]migrations.Migration, ir.Model, ir.Model, ir.Model) {
	t.Helper()
	field := schema.ForeignKey("owner", "OwnerID", schema.Target("otohistory", "owner"), schema.RelatedName("children"), schema.Protect)
	field.Unique = unique
	built, err := schema.Build(schema.Definition{AppLabel: "otohistory", Models: []schema.Model{
		{Name: "owner", GoName: "Owner"},
		{Name: "child", GoName: "Child", Fields: []schema.Field{field}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parent, before := built.Models[0], built.Models[1]
	after := before.Clone()
	after.Fields[1].Unique = true
	after.Fields[1].Relation.Cardinality = ir.RelationOneToOne
	after.Fields[1].Relation.Reverse.Name = "child"
	initial := migrations.Migration{App: "otohistory", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "otohistory", Model: parent}, migrations.CreateModel{AppLabel: "otohistory", Model: before},
	}}
	alter := migrations.Migration{App: "otohistory", Name: "0002_one_to_one", Dependencies: []migrations.MigrationKey{initial.Key()},
		Operations: []migrations.Operation{migrations.AlterField{AppLabel: "otohistory", ModelName: "child", Before: before.Fields[1], After: after.Fields[1]}}}
	return []migrations.Migration{initial, alter}, parent, before, after
}
