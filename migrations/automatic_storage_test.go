package migrations_test

import (
	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDeletingModelOrdersReadsOfItsAutomaticStorage(t *testing.T) {
	history, models, field := manytomanytest.AutomaticHistory(t, false, false)
	initial, link := history[0], history[1]
	owner := models[0].Clone()
	owner.ManyToMany = []ir.ManyToManyField{field}
	deletion := migrations.Migration{App: "manyhistory", Name: "0003_drop", Dependencies: []migrations.MigrationKey{link.Key()}, Operations: []migrations.Operation{
		migrations.DeleteModel{AppLabel: "manyhistory", Model: owner, BeforeModel: "label"},
	}}
	audit := migrations.Migration{App: "audit", Name: "0001_read", Dependencies: []migrations.MigrationKey{link.Key()}, Operations: []migrations.Operation{
		migrations.RunData{Key: migrations.DataKey{App: "audit", Name: "read", Version: 1}, Forward: migrations.DataNoop, Reverse: migrations.DataNoop,
			Models: []ir.ModelIdentity{{AppLabel: "manyhistory", ModelName: "owner_labels"}}},
	}}
	if _, err := migrations.NewStateReconstructor(initial, link, audit, deletion); err == nil {
		t.Fatal("unordered read of an intermediary survived deletion of its owner")
	}
	deletion.Dependencies = append(deletion.Dependencies, audit.Key())
	if _, err := migrations.NewStateReconstructor(initial, link, audit, deletion); err != nil {
		t.Fatal("ordered read of the original intermediary", err)
	}
}

func TestAutomaticManyToManyHistoricalAliasesAndRemovalAuthority(t *testing.T) {
	for _, mode := range []string{"alias_removed", "alias_retained", "fk_retained", "future_storage", "no_ancestry", "recreated"} {
		t.Run(mode, func(t *testing.T) {
			history, _, field := manytomanytest.AutomaticHistory(t, false, false)
			history = history[:2]
			alias := field.Clone()
			alias.Name, alias.GoName = "alias", "Alias"
			alias.Reverse = ir.ReverseRelation{Disabled: true}
			alias.Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner_labels"}, SourceField: "source", TargetField: "target"}
			fk := ir.Field{Name: "link", GoName: "LinkID", Kind: ir.FieldForeignKey, Nullable: true, Relation: &ir.ForeignKeyRelation{Target: alias.Through.Model, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: ir.DeleteCascade}}
			later := migrations.Migration{App: "manyhistory", Name: "0003_refs", Dependencies: []migrations.MigrationKey{history[1].Key()}}
			switch mode {
			case "fk_retained":
				later.Operations = []migrations.Operation{migrations.AddField{AppLabel: "manyhistory", ModelName: "label", Field: fk}, migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field}}
			case "recreated":
				later.Operations = []migrations.Operation{migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field}, migrations.AddManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field}, migrations.AddField{AppLabel: "manyhistory", ModelName: "label", Field: fk}}
			default:
				later.Operations = []migrations.Operation{migrations.AddManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: alias}}
				if mode == "alias_removed" {
					later.Operations = append(later.Operations, migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: alias})
				}
				if mode == "future_storage" {
					history[1].Operations, later.Operations = later.Operations, history[1].Operations
				}
				if mode == "no_ancestry" {
					later.Dependencies = []migrations.MigrationKey{history[0].Key()}
				}
				if mode == "alias_removed" || mode == "alias_retained" {
					anchor := ""
					if mode == "alias_retained" {
						anchor = "alias"
					}
					later.Operations = append(later.Operations, migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field, BeforeField: anchor})
				}
			}
			history = append(history, later)
			reconstructor, err := migrations.NewStateReconstructor(history...)
			want := mode == "alias_removed" || mode == "recreated"
			if (err == nil) != want {
				t.Fatal("storage visibility/ownership differs", mode, err)
			}
			if want {
				state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
				if err != nil {
					t.Fatal(err)
				}
				if _, leaked := state.Model("manyhistory", "owner_labels"); leaked {
					t.Fatal("derived model became logical state")
				}
			}
		})
	}
}

func TestAutomaticManyToManyCreateWithOwnedAndExplicitDeclarations(t *testing.T) {
	history, models, field := manytomanytest.AutomaticHistory(t, true, false)
	second := field.Clone()
	second.Name, second.GoName = "other", "Other"
	alias := field.Clone()
	alias.Name, alias.GoName = "alias", "Alias"
	alias.Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner_labels"}, SourceField: "source", TargetField: "target"}
	models[0].ManyToMany = []ir.ManyToManyField{field, second, alias}
	history = history[:1]
	history[0].Operations[0] = migrations.CreateModel{AppLabel: "manyhistory", Model: models[0]}
	r, err := migrations.NewStateReconstructor(history...)
	if err != nil {
		t.Fatal(err)
	}
	state, err := r.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := state.Model("manyhistory", "owner")
	if !owner.Equal(models[0]) {
		t.Fatal("inline ownership lost", owner)
	}
}
