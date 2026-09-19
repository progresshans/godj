package migrations

import (
	"context"
	"reflect"
	"strings"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func loadedGraphModel(t *testing.T, name string, fields ...ir.Field) ir.Model {
	t.Helper()
	schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "graph",
		Models: []ir.Model{{Name: name, GoName: "Model" + name, Fields: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	return schema.Models[0]
}

func loadedGraphFK(name, target string) ir.Field {
	return ir.Field{Name: name, GoName: "Field" + name, Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "graph", ModelName: target},
			Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
}

func TestLoadedMigrationGraphMaterializesSelfAndTransitiveCycleInBothDirections(t *testing.T) {
	a := loadedGraphModel(t, "a", loadedGraphFK("parent", "a"))
	b := loadedGraphModel(t, "b", loadedGraphFK("a", "a"))
	c := loadedGraphModel(t, "c", loadedGraphFK("b", "b"))
	complete := loadedGraphModel(t, "a", loadedGraphFK("parent", "a"), loadedGraphFK("peer", "c"), loadedGraphFK("other", "b"))
	definition := Migration{App: "graph", Name: "0001_cycle", Operations: []Operation{
		CreateModel{AppLabel: "graph", Model: a}, CreateModel{AppLabel: "graph", Model: b}, CreateModel{AppLabel: "graph", Model: c},
		AddField{AppLabel: "graph", ModelName: "a", Field: complete.Fields[2]},
		AddField{AppLabel: "graph", ModelName: "a", Field: complete.Fields[3]},
	}}
	reconstructor, err := newLoadedStateReconstructor([]Migration{definition})
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconstructor.Reconstruct(LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	got, exists := state.Model("graph", "a")
	if !exists || !reflect.DeepEqual(got, complete) {
		t.Fatal("latest cycle model lost fields or self metadata")
	}
	builder := newLoadedStateBuilder()
	forward, err := reconstructor.materializeLoadedStep(context.Background(), builder, PlanStep{Key: definition.Key(), Direction: DirectionForward}, false)
	if err != nil {
		t.Fatal(err)
	}
	intent := loadedBackendRelationIntent(forward.intent)
	if len(intent.Operations[3].Targets) != 2 || len(intent.Operations[3].RelatedModels) != 1 || intent.Operations[3].RelatedModels[0].Model.Name != "b" {
		t.Fatal("operation did not seal complete direct and transitive target authority")
	}
	if len(intent.Operations[4].Targets) != 3 || len(intent.Operations[4].RelatedModels) != 0 {
		t.Fatal("shared direct target was duplicated as a transitive vertex")
	}
	plan, err := migrationbackend.ResolveMigrationGraphPlan(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "graph", Name: "0001_cycle"}, Kind: migrationbackend.HistoryTransitionApply}, intent)
	if err != nil || len(plan.FinalModels()) != 3 {
		t.Fatalf("backend graph plan: %v", err)
	}
	backward, err := reconstructor.materializeLoadedStep(context.Background(), builder, PlanStep{Key: definition.Key(), Direction: DirectionBackward}, false)
	if err != nil || !builder.empty() {
		t.Fatalf("reverse graph reconstruction: %v", err)
	}
	_, err = migrationbackend.ResolveMigrationGraphPlan(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "graph", Name: "0001_cycle"}, Kind: migrationbackend.HistoryTransitionUnapply}, loadedBackendRelationIntent(backward.intent))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadedMigrationGraphTracksDisabledReverseOwnersAndClonesThem(t *testing.T) {
	a := loadedGraphModel(t, "a", loadedGraphFK("self", "a"))
	b := loadedGraphModel(t, "b", loadedGraphFK("a", "a"))
	builder := newLoadedStateBuilder()
	if err := builder.createModel(CreateModel{AppLabel: "graph", Model: a}); err != nil {
		t.Fatal(err)
	}
	if err := builder.createModel(CreateModel{AppLabel: "graph", Model: b}); err != nil {
		t.Fatal(err)
	}
	clone := builder.clone()
	if err := clone.deleteModel(CreateModel{AppLabel: "graph", Model: a}); err == nil || !strings.Contains(err.Error(), "still targeted") {
		t.Fatalf("disabled reverse accessor hid an inbound relation: %v", err)
	}
	if err := clone.deleteModel(CreateModel{AppLabel: "graph", Model: b}); err != nil {
		t.Fatal(err)
	}
	if err := clone.deleteModel(CreateModel{AppLabel: "graph", Model: a}); err != nil {
		t.Fatal(err)
	}
	if !clone.empty() || builder.empty() || len(builder.incoming) != 1 {
		t.Fatal("clone shared mutable incoming ownership or leaked self edge")
	}
}

func TestLoadedMigrationGraphSelfChoicesUseExactBackwardAfterBoundary(t *testing.T) {
	field := ir.Field{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 4,
		Choices: []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "one"}, Label: "One"}}}
	model := loadedGraphModel(t, "node", field, loadedGraphFK("parent", "node"))
	before := model.Fields[1].Clone()
	after := before.Clone()
	after.Choices[0].Label = "Changed"
	definition := Migration{App: "graph", Name: "0001_choices", Operations: []Operation{
		CreateModel{AppLabel: "graph", Model: model}, AlterField{AppLabel: "graph", ModelName: "node", Before: before, After: after},
	}}
	r, err := newLoadedStateReconstructor([]Migration{definition})
	if err != nil {
		t.Fatal(err)
	}
	builder := newLoadedStateBuilder()
	for _, direction := range []Direction{DirectionForward, DirectionBackward} {
		step, err := r.materializeLoadedStep(context.Background(), builder, PlanStep{Key: definition.Key(), Direction: direction}, false)
		if err != nil {
			t.Fatal(err)
		}
		kind := migrationbackend.HistoryTransitionApply
		if direction == DirectionBackward {
			kind = migrationbackend.HistoryTransitionUnapply
		}
		_, err = migrationbackend.ResolveMigrationGraphPlan(migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "graph", Name: "0001_choices"}, Kind: kind}, loadedBackendRelationIntent(step.intent))
		if err != nil {
			t.Fatal(err)
		}
	}
	if !builder.empty() {
		t.Fatal("choices reverse did not restore empty state")
	}
}
