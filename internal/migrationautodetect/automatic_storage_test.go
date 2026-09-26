package migrationautodetect

import (
	"bytes"
	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDetectAutomaticStorageDependencyCyclesAndResumeEveryPrefix(t *testing.T) {
	for _, mode := range []string{"self", "same_app_storage_fk", "cross_app_cycle", "alias"} {
		t.Run(mode, func(t *testing.T) {
			_, models, field := manytomanytest.AutomaticHistory(t, mode == "self", mode == "cross_app_cycle")
			models[0].ManyToMany = []ir.ManyToManyField{field}
			if mode == "alias" {
				alias := field.Clone()
				alias.Name, alias.GoName = "alias", "Alias"
				alias.Reverse = ir.ReverseRelation{Disabled: true}
				alias.Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner_labels"}, SourceField: "source", TargetField: "target"}
				models[0].ManyToMany = []ir.ManyToManyField{alias, field}
			}
			if mode == "same_app_storage_fk" || mode == "cross_app_cycle" {
				target := "owner_labels"
				if mode == "cross_app_cycle" {
					target = "owner"
				}
				models[1].Fields = append(models[1].Fields, ir.Field{Name: "link", GoName: "LinkID", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: target}, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: ir.DeleteCascade}})
			}
			schemas := []ir.Schema{{FormatVersion: ir.CurrentFormatVersion, AppLabel: "manyhistory", Models: models}}
			apps := []string{"manyhistory"}
			if mode == "cross_app_cycle" {
				schemas[0].Models = models[:1]
				schemas = append(schemas, ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "labels", Models: models[1:]})
				apps = append(apps, "labels")
			}
			desired := mustProjectState(t, schemas...)
			loaded := mustLoadDefinitions(t)
			changes := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: apps}).Migrations()
			assertGeneratedState(t, loaded, changes, desired)
			for prefix := 0; prefix <= len(changes); prefix++ {
				suffix := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, changes[:prefix]...), Desired: desired, ManagedApps: apps}).Migrations()
				if len(suffix) != len(changes)-prefix {
					t.Fatal("durable prefix changed plan length", prefix)
				}
				for i, change := range suffix {
					got, err := definition.Encode(testProducer, change)
					if err != nil {
						t.Fatal(err)
					}
					want, err := definition.Encode(testProducer, changes[prefix+i])
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want) {
						t.Fatal("durable prefix changed document", prefix, i)
					}
				}
			}
			if mode == "same_app_storage_fk" {
				if len(changes) < 2 {
					t.Fatal("FK to future storage was not deferred")
				}
			}
		})
	}
}

func TestDetectAutomaticStorageAddRenameRemove(t *testing.T) {
	history, models, field := manytomanytest.AutomaticHistory(t, false, false)
	loaded := mustLoadDefinitions(t, history[0])
	for _, name := range []string{"labels", "tags", ""} {
		owner := models[0].Clone()
		if name != "" {
			current := field.Clone()
			current.Name = name
			if name == "tags" {
				current.GoName = "Tags"
			}
			owner.ManyToMany = []ir.ManyToManyField{current}
		}
		desired := mustProjectState(t, ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "manyhistory", Models: []ir.Model{owner, models[1]}})
		changes := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"manyhistory"}}).Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("non-minimal automatic change", changes)
		}
		expected := map[string]string{"labels": "AddManyToMany", "tags": "RenameManyToMany", "": "RemoveManyToMany"}[name]
		if changes[0].Operations[0].Kind() != expected {
			t.Fatal("rename would lose retained links", changes)
		}
		assertGeneratedState(t, loaded, changes, desired)
		loaded = mustLoadDefinitions(t, append(loaded.Definitions(), changes...)...)
	}
}
