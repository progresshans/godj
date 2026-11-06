package migrationautodetect

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectExplicitManyToManyAddRenameRemoveAndRetainedOrder(t *testing.T) {
	history, models, field := manytomanytest.History(t, true, false, false)
	loaded := mustLoadDefinitions(t, history[0])
	for _, want := range []string{"labels", "tags", ""} {
		owner := models[0].Clone()
		if want != "" {
			current := field.Clone()
			current.Name = want
			if want == "tags" {
				current.GoName = "Tags"
			}
			owner.ManyToMany = []ir.ManyToManyField{current}
		}
		desired := mustProjectState(t, ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "manyhistory", Models: []ir.Model{owner, models[1], models[2]}})
		plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"manyhistory"}})
		changes := plan.Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("wrong metadata plan", changes)
		}
		expected := map[string]string{"labels": "AddManyToMany", "tags": "RenameManyToMany", "": "RemoveManyToMany"}[want]
		if changes[0].Operations[0].Kind() != expected {
			t.Fatal("wrong operation", changes[0].Operations[0])
		}
		assertGeneratedState(t, loaded, changes, desired)
		if add, ok := changes[0].Operations[0].(migrations.AddManyToMany); ok {
			add.Field.Through.SourceField = "mutated"
			again := plan.Migrations()[0].Operations[0].(migrations.AddManyToMany)
			if again.Field.Through.SourceField != "owner" {
				t.Fatal("plan exposes mutable through storage")
			}
		}
		loaded = mustLoadDefinitions(t, append(loaded.Definitions(), plan.Migrations()...)...)
		if next := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"manyhistory"}}); !next.Empty() {
			t.Fatal("metadata plan not idempotent")
		}
	}
}

func TestDetectExplicitManyToManyFreshAndCrossAppCycleResumeEveryPrefix(t *testing.T) {
	for _, cross := range []bool{false, true} {
		t.Run(fmt.Sprint(cross), func(t *testing.T) {
			_, models, field := manytomanytest.History(t, false, true, false)
			models[0].ManyToMany = []ir.ManyToManyField{field}
			schemas := []ir.Schema{{FormatVersion: ir.CurrentFormatVersion, AppLabel: "manyhistory", Models: models}}
			apps := []string{"manyhistory"}
			if cross {
				apps = []string{"alpha", "beta", "gamma"}
				schemas = nil
				for index, model := range models {
					model = model.Clone()
					if index == 0 {
						model.ManyToMany[0].Target.AppLabel = "beta"
						model.ManyToMany[0].Through.Model.AppLabel = "gamma"
					}
					if index == 2 {
						model.Fields[1].Relation.Target.AppLabel = "alpha"
						model.Fields[2].Relation.Target.AppLabel = "beta"
					}
					schemas = append(schemas, ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: apps[index], Models: []ir.Model{model}})
				}
			}
			desired := mustProjectState(t, schemas...)
			loaded := mustLoadDefinitions(t)
			plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: apps})
			changes := plan.Migrations()
			assertGeneratedState(t, loaded, changes, desired)
			adds := 0
			for _, change := range changes {
				for _, operation := range change.Operations {
					if _, ok := operation.(migrations.AddManyToMany); ok {
						adds++
					}
					if create, ok := operation.(migrations.CreateModel); ok && len(create.Model.ManyToMany) != 0 {
						t.Fatal("explicit through was embedded before FK readiness")
					}
				}
			}
			if adds != 1 {
				t.Fatal("collection missing from cold plan", changes)
			}
			for prefix := 0; prefix <= len(changes); prefix++ {
				resumed := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, changes[:prefix]...), Desired: desired, ManagedApps: apps}).Migrations()
				if len(resumed) != len(changes)-prefix {
					t.Fatal("prefix changed suffix length", prefix, resumed)
				}
				for index, change := range resumed {
					actual, err := definition.Encode(testProducer, change)
					if err != nil {
						t.Fatal(err)
					}
					expected, err := definition.Encode(testProducer, changes[prefix+index])
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(actual, expected) {
						t.Fatal("durable prefix changed remaining document", prefix, index)
					}
				}
			}
		})
	}
}

func TestDetectExplicitManyToManyMiddleAnchorsAndAmbiguity(t *testing.T) {
	history, models, field := manytomanytest.History(t, false, true, false)
	loaded := mustLoadDefinitions(t, history[:2]...)
	first := field.Clone()
	first.Name, first.GoName = "first", "First"
	first.Reverse = ir.ReverseRelation{Disabled: true}
	last := first.Clone()
	last.Name, last.GoName = "last", "Last"
	models[0].ManyToMany = []ir.ManyToManyField{first, field, last}
	desired := mustProjectState(t, ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "manyhistory", Models: models})
	plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"manyhistory"}})
	assertGeneratedState(t, loaded, plan.Migrations(), desired)
	// Retained declaration reordering is not silently replaced with data-changing
	// remove/add operations; the user can author the intended transition.
	reordered := models[0].Clone()
	reordered.ManyToMany = []ir.ManyToManyField{last, field, first}
	next := mustProjectState(t, ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "manyhistory", Models: []ir.Model{reordered, models[1], models[2]}})
	full := mustLoadDefinitions(t, append(loaded.Definitions(), plan.Migrations()...)...)
	if result, err := Detect(Request{Definitions: full, Desired: next, ManagedApps: []string{"manyhistory"}}); err == nil || !result.Empty() {
		t.Fatal("retained relation order changed silently")
	}
}
