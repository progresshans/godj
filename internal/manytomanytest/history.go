// Package manytomanytest owns authored migration inputs shared by native tests.
// Database observations and expected results are asserted by each caller.
package manytomanytest

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func History(t testing.TB, nullable, unique, self bool) ([]migrations.Migration, []ir.Model, ir.ManyToManyField) {
	t.Helper()
	target := "label"
	if self {
		target = "owner"
	}
	owner := schema.ForeignKey("owner", "OwnerID", schema.Target("manyhistory", "owner"), schema.NoReverse(), schema.Cascade)
	label := schema.ForeignKey("label", "LabelID", schema.Target("manyhistory", target), schema.NoReverse(), schema.Cascade)
	owner.Nullable, label.Nullable = nullable, nullable
	link := schema.Model{Name: "membership", GoName: "Membership", Fields: []schema.Field{owner, label, schema.CharField("note", "Note", 32)}}
	if unique {
		link.UniqueConstraints = []schema.UniqueConstraint{{Name: "pair", Fields: []string{"owner", "label"}}}
	}
	built, err := schema.Build(schema.Definition{AppLabel: "manyhistory", Models: []schema.Model{{Name: "owner", GoName: "Owner"}, {Name: "label", GoName: "Label"}, link}})
	if err != nil {
		t.Fatal(err)
	}
	field, err := ir.NormalizeManyToManyField("manyhistory", "owner", ir.ManyToManyField{Name: "labels", GoName: "Labels", Target: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: target}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "membership"}, SourceField: "owner", TargetField: "label"}})
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.Migration{App: "manyhistory", Name: "0001_initial"}
	for _, model := range built.Models {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: initial.App, Model: model})
	}
	renamed := field.Clone()
	renamed.Name, renamed.GoName = "tags", "Tags"
	history := []migrations.Migration{initial}
	for _, item := range []struct {
		name string
		op   migrations.Operation
	}{
		{"0002_labels", &migrations.AddManyToMany{AppLabel: initial.App, ModelName: "owner", Field: field.Clone()}},
		{"0003_tags", &migrations.RenameManyToMany{AppLabel: initial.App, ModelName: "owner", Before: field.Clone(), After: renamed.Clone()}},
		{"0004_remove", &migrations.RemoveManyToMany{AppLabel: initial.App, ModelName: "owner", Field: renamed.Clone()}},
	} {
		history = append(history, migrations.Migration{App: initial.App, Name: item.name, Dependencies: []migrations.MigrationKey{history[len(history)-1].Key()}, Operations: []migrations.Operation{item.op}})
	}
	return history, built.Models, field
}

// CrossHistory uses the same physical tables while the explicit intermediary
// belongs to a different historical app. The first two definitions create its
// dependency closure; the remaining four keep History's checkpoints.
func CrossHistory(t testing.TB, nullable, unique, self bool) ([]migrations.Migration, []ir.Model, ir.ManyToManyField) {
	history, models, field := History(t, nullable, unique, self)
	endpoints := migrations.Migration{App: "manyhistory", Name: "0000_endpoints", Operations: history[0].Operations[:2]}
	through := migrations.Migration{App: "joinhistory", Name: "0001_membership", Dependencies: []migrations.MigrationKey{endpoints.Key()}, Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "joinhistory", Model: models[2]}}}
	history[0].Operations = nil
	history[0].Dependencies = []migrations.MigrationKey{through.Key()}
	history[1].Operations[0].(*migrations.AddManyToMany).Field.Through.Model.AppLabel = "joinhistory"
	rename := history[2].Operations[0].(*migrations.RenameManyToMany)
	rename.Before.Through.Model.AppLabel = "joinhistory"
	rename.After.Through.Model.AppLabel = "joinhistory"
	history[3].Operations[0].(*migrations.RemoveManyToMany).Field.Through.Model.AppLabel = "joinhistory"
	field.Through.Model.AppLabel = "joinhistory"
	return append([]migrations.Migration{endpoints, through}, history...), models, field
}
