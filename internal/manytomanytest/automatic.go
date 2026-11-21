package manytomanytest

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

// AutomaticHistory supplies authored logical definitions, never backend DDL.
// The final four definitions are endpoint creation, add, rename and remove.
func AutomaticHistory(t testing.TB, self, cross bool) ([]migrations.Migration, []ir.Model, ir.ManyToManyField) {
	t.Helper()
	built, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "manyhistory", Models: []ir.Model{
		{Name: "owner", GoName: "Owner", Fields: []ir.Field{{Name: "note", GoName: "Note", Kind: ir.FieldText}}},
		{Name: "label", GoName: "Label"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	target := ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "label"}
	if cross {
		target.AppLabel = "labels"
	}
	if self {
		target = ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner"}
	}
	field, err := ir.NormalizeManyToManyField("manyhistory", "owner", ir.ManyToManyField{Name: "labels", GoName: "Labels", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.Migration{App: "manyhistory", Name: "0001_initial"}
	var history []migrations.Migration
	if cross {
		external := migrations.Migration{App: "labels", Name: "0001_label", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "labels", Model: built.Models[1]}}}
		history = append(history, external)
		initial.Dependencies = []migrations.MigrationKey{external.Key()}
	}
	initial.Operations = []migrations.Operation{migrations.CreateModel{AppLabel: "manyhistory", Model: built.Models[0]}}
	if !cross {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: "manyhistory", Model: built.Models[1]})
	}
	history = append(history, initial)
	renamed := field.Clone()
	renamed.Name, renamed.GoName = "tags", "Tags"
	for _, step := range []struct {
		name string
		op   migrations.Operation
	}{
		{"0002_labels", migrations.AddManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field}},
		{"0003_tags", migrations.RenameManyToMany{AppLabel: "manyhistory", ModelName: "owner", Before: field, After: renamed}},
		{"0004_remove", migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: renamed}},
	} {
		history = append(history, migrations.Migration{App: "manyhistory", Name: step.name, Dependencies: []migrations.MigrationKey{history[len(history)-1].Key()}, Operations: []migrations.Operation{step.op}})
	}
	return history, built.Models, field
}

func AutomaticModel(t testing.TB, owner ir.Model, field ir.ManyToManyField) ir.Model {
	t.Helper()
	owner = owner.Clone()
	owner.ManyToMany = []ir.ManyToManyField{field}
	model, err := ir.AutomaticThroughModel("manyhistory", owner, field)
	if err != nil {
		t.Fatal(err)
	}
	return model
}
