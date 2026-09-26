package migrations_test

import (
	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestManyToManyHistoricalBindingValidationAndDependencyAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]migrations.Migration)
	}{
		{"missing through", func(h []migrations.Migration) {
			h[1].Operations[0].(*migrations.AddManyToMany).Field.Through.Model.ModelName = "missing"
		}},
		{"wrong selected endpoint", func(h []migrations.Migration) {
			h[1].Operations[0].(*migrations.AddManyToMany).Field.Through.SourceField = "label"
		}},
		{"wrong target", func(h []migrations.Migration) {
			h[1].Operations[0].(*migrations.AddManyToMany).Field.Target.ModelName = "membership"
		}},
		{"scalar namespace", func(h []migrations.Migration) { h[1].Operations[0].(*migrations.AddManyToMany).Field.Name = "id" }},
		{"reverse namespace", func(h []migrations.Migration) {
			h[1].Operations[0].(*migrations.AddManyToMany).Field.Reverse.Name = "id"
		}},
		{"missing ancestry", func(h []migrations.Migration) { h[1].Dependencies = nil }},
		{"stale removal", func(h []migrations.Migration) {
			h[3].Operations[0].(*migrations.RemoveManyToMany).Field.GoName = "Stale"
		}},
		{"rename retarget", func(h []migrations.Migration) {
			h[2].Operations[0].(*migrations.RenameManyToMany).After.Through.TargetField = "owner"
		}},
		{"rename through ownership mismatch", func(h []migrations.Migration) { h[1].Operations[0].(*migrations.AddManyToMany).Field.Through = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			history, _, _ := manytomanytest.History(t, false, true, false)
			test.mutate(history)
			if _, err := migrations.NewStateReconstructor(history...); err == nil {
				t.Fatal("invalid chronology/binding accepted")
			}
		})
	}
	history, _, _ := manytomanytest.History(t, true, false, true)
	if _, err := migrations.NewStateReconstructor(history...); err != nil {
		t.Fatal("valid nullable nonunique symmetric self intermediary rejected", err)
	}
	// A later FK edit may not invalidate a retained explicit collection.
	history, models, _ := manytomanytest.History(t, false, true, false)
	before := models[2].Fields[1]
	after := before.Clone()
	after.Relation.Target.ModelName = "label"
	history = history[:2]
	history = append(history, migrations.Migration{App: "manyhistory", Name: "0003_retarget", Dependencies: []migrations.MigrationKey{history[1].Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "manyhistory", ModelName: "membership", Before: before, After: after}}})
	if _, err := migrations.NewStateReconstructor(history...); err == nil {
		t.Fatal("retained collection lost its selected FK")
	}
}

func TestManyToManyHistoricalInsertionAndIndependentSnapshots(t *testing.T) {
	history, _, field := manytomanytest.History(t, false, true, false)
	second := field.Clone()
	second.Name, second.GoName = "extra", "Extra"
	second.Reverse = ir.ReverseRelation{Disabled: true}
	third := second.Clone()
	third.Name, third.GoName = "first", "First"
	history = history[:2]
	history[1].Operations = append(history[1].Operations, migrations.AddManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: second}, migrations.AddManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: third, BeforeField: "labels"}, migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field, BeforeField: "extra"})
	reconstructor, err := migrations.NewStateReconstructor(history...)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	model, _ := state.Model("manyhistory", "owner")
	if len(model.Fields) != 1 || len(model.ManyToMany) != 2 || model.ManyToMany[0].Name != "first" || model.ManyToMany[1].Name != "extra" {
		t.Fatal("columnless order changed", model)
	}
	model.ManyToMany[0].Through.SourceField = "mutated"
	fresh, _ := state.Model("manyhistory", "owner")
	if fresh.ManyToMany[0].Through.SourceField != "owner" {
		t.Fatal("state aliases nested through")
	}
}
