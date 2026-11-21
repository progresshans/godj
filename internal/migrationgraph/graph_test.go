package migrationgraph

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func graphTestModel(t *testing.T, app, name string, fields ...ir.Field) MigrationModel {
	t.Helper()
	schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: app,
		Models: []ir.Model{{Name: name, GoName: "Model" + name, Fields: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	return MigrationModel{AppLabel: app, Model: schema.Models[0]}
}

func graphTestFK(name, app, target string) ir.Field {
	return ir.Field{Name: name, GoName: "Field" + name, Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: app, ModelName: target},
			Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
}

func graphTestTarget(field ir.Field, target MigrationModel) MigrationTarget {
	return MigrationTarget{SourceField: field, TargetModel: target.Model, TargetKey: target.Model.Fields[0]}
}

func TestMigrationRelationGraphCyclesAndDetachedSnapshots(t *testing.T) {
	t.Parallel()
	leaf := graphTestModel(t, "third", "item", ir.Field{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 20,
		Default: &ir.Scalar{Kind: ir.ScalarString, String: "base"}, Choices: []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "base"}, Label: "Base"}}})
	peer := graphTestModel(t, "second", "item", graphTestFK("back", "first", "item"), graphTestFK("leaf", "third", "item"))
	root := graphTestModel(t, "first", "item", graphTestFK("self", "first", "item"), graphTestFK("peer", "second", "item"), graphTestFK("again", "second", "item"))
	operation := MigrationOperation{Kind: MigrationCreateModel, After: root.Model,
		Targets:       []MigrationTarget{graphTestTarget(root.Model.Fields[1], root), graphTestTarget(root.Model.Fields[2], peer), graphTestTarget(root.Model.Fields[3], peer)},
		RelatedModels: []MigrationModel{leaf}}
	graph, err := ResolveMigrationGraph("first", operation)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Root() != root.Identity() || len(graph.Models()) != 3 {
		t.Fatal("self/shared/cross-app vertices were not resolved exactly")
	}
	got, err := graph.Targets(peer.Identity())
	if err != nil || len(got) != 2 || !reflect.DeepEqual(got[0].TargetModel, root.Model) || !reflect.DeepEqual(got[1].TargetModel, leaf.Model) {
		t.Fatalf("peer bindings: %#v, %v", got, err)
	}
	operation.RelatedModels[0].Model.Fields[1].Default.String = "input mutation"
	got[1].TargetModel.Fields[1].Choices[0].Label = "output mutation"
	models := graph.Models()
	models[2].Model.Fields[1].Default.String = "model accessor mutation"
	again, err := graph.Targets(peer.Identity())
	if err != nil || again[1].TargetModel.Fields[1].Default.String != "base" || again[1].TargetModel.Fields[1].Choices[0].Label != "Base" {
		t.Fatal("graph retained a mutable input or output alias")
	}
}

func TestMigrationRelationGraphRejectsIncompleteOrConflictingAuthority(t *testing.T) {
	leaf := graphTestModel(t, "graph", "leaf")
	peer := graphTestModel(t, "graph", "peer", graphTestFK("leaf", "graph", "leaf"))
	root := graphTestModel(t, "graph", "root", graphTestFK("peer", "graph", "peer"))
	base := MigrationIntent{Operations: []MigrationOperation{{Kind: MigrationCreateModel, After: root.Model,
		Targets: []MigrationTarget{graphTestTarget(root.Model.Fields[1], peer)}, RelatedModels: []MigrationModel{leaf}}}}
	for _, test := range []struct {
		name   string
		mutate func(*MigrationOperation)
	}{
		{"missing direct", func(o *MigrationOperation) { o.Targets = nil }},
		{"missing transitive", func(o *MigrationOperation) { o.RelatedModels = nil }},
		{"wrong source field", func(o *MigrationOperation) { o.Targets[0].SourceField.Column = "forged" }},
		{"wrong target key", func(o *MigrationOperation) { o.Targets[0].TargetKey.Column = "forged" }},
		{"wrong target identity", func(o *MigrationOperation) { o.Targets[0].TargetModel.Name = "other" }},
		{"unnormalized transitive", func(o *MigrationOperation) { o.RelatedModels[0].Model.Fields[0].Column = "" }},
		{"unreachable transitive", func(o *MigrationOperation) {
			o.RelatedModels = append(o.RelatedModels, graphTestModel(t, "graph", "unused"))
		}},
		{"repeated direct vertex", func(o *MigrationOperation) { o.RelatedModels = append(o.RelatedModels, peer) }},
		{"repeated source vertex", func(o *MigrationOperation) { o.RelatedModels = append(o.RelatedModels, root) }},
		{"physical table alias", func(o *MigrationOperation) { o.RelatedModels[0].Model.DBTable = root.Model.DBTable }},
		{"reverse field collision", func(o *MigrationOperation) {
			o.Targets[0].SourceField.Relation.Reverse = ir.ReverseRelation{Name: "leaf"}
			o.After.Fields[1] = o.Targets[0].SourceField.Clone()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := base.Clone().Operations[0]
			test.mutate(&input)
			if _, err := ResolveMigrationGraph("graph", input); err == nil {
				t.Fatal("accepted incomplete or conflicting graph")
			}
		})
	}
}

func TestMigrationRelationGraphOwnsFiniteDeepCycles(t *testing.T) {
	const count = migrationGraphMaxModels
	models := make([]MigrationModel, count)
	for index := range models {
		models[index] = graphTestModel(t, "graph", fmt.Sprintf("m%04d", index), graphTestFK("next", "graph", fmt.Sprintf("m%04d", (index+1)%count)))
	}
	graph, err := NewRelationGraph(models[0], models[1:])
	if err != nil || len(graph.Models()) != count {
		t.Fatalf("finite cycle: %v", err)
	}
	extra := append(CloneMigrationModels(models[1:]), graphTestModel(t, "graph", "outside"))
	if _, err := NewRelationGraph(models[0], extra); err == nil {
		t.Fatal("accepted a graph beyond its vertex limit")
	}
	if _, err := (RelationGraph{}).Targets(models[0].Identity()); err == nil {
		t.Fatal("zero graph resolved a target")
	}
}

func TestMigrationGraphPlanPreservesChronologicalCycleBoundaries(t *testing.T) {
	a := graphTestModel(t, "graph", "a", graphTestFK("self", "graph", "a"))
	b := graphTestModel(t, "graph", "b", graphTestFK("a", "graph", "a"))
	complete := graphTestModel(t, "graph", "a", graphTestFK("self", "graph", "a"), graphTestFK("b", "graph", "b"))
	apply := MigrationIntent{Operations: []MigrationOperation{
		{OperationIndex: 0, Kind: MigrationCreateModel, After: a.Model, Targets: []MigrationTarget{graphTestTarget(a.Model.Fields[1], a)}},
		{OperationIndex: 1, Kind: MigrationCreateModel, After: b.Model, Targets: []MigrationTarget{graphTestTarget(b.Model.Fields[1], a)}},
		{OperationIndex: 2, Kind: MigrationAddField, Before: a.Model, After: complete.Model, Targets: []MigrationTarget{graphTestTarget(complete.Model.Fields[1], complete), graphTestTarget(complete.Model.Fields[2], b)}},
	}}
	plan, err := ResolveMigrationGraphPlan("graph", "0001", false, apply)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.InitialModels()) != 0 || len(plan.FinalModels()) != 2 {
		t.Fatal("incorrect apply graph boundaries")
	}
	targets, err := plan.FinalTargets(b.Identity())
	if err != nil || !reflect.DeepEqual(targets[0].TargetModel, complete.Model) {
		t.Fatal("final target retains stale pre-add fields")
	}
	if _, err := plan.BoundaryTargets(a.Model, true); err == nil {
		t.Fatal("accepted stale source boundary")
	}
	remove := MigrationIntent{Operations: []MigrationOperation{
		{OperationIndex: 2, Kind: MigrationRemoveField, Before: complete.Model, After: a.Model, Targets: apply.Operations[2].Targets},
		{OperationIndex: 1, Kind: MigrationDeleteModel, Before: b.Model, Targets: apply.Operations[1].Targets},
		{OperationIndex: 0, Kind: MigrationDeleteModel, Before: a.Model, Targets: apply.Operations[0].Targets},
	}}
	backward, err := ResolveMigrationGraphPlan("graph", "0001", true, remove)
	if err != nil || len(backward.InitialModels()) != 2 || len(backward.FinalModels()) != 0 {
		t.Fatalf("reverse cycle: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*MigrationIntent)
	}{
		{"target before creator", func(i *MigrationIntent) {
			i.Operations[0], i.Operations[1] = i.Operations[1], i.Operations[0]
			i.Operations[0].OperationIndex = 0
			i.Operations[1].OperationIndex = 1
		}},
		{"stale self target", func(i *MigrationIntent) { i.Operations[2].Targets[0].TargetModel = a.Model.Clone() }},
		{"discontinuous source", func(i *MigrationIntent) { i.Operations[2].Before.GoName = "Changed" }},
		{"future target metadata", func(i *MigrationIntent) { i.Operations[1].Targets[0].TargetModel = complete.Model.Clone() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := apply.Clone()
			test.mutate(&input)
			if _, err := ResolveMigrationGraphPlan("graph", "0001", false, input); err == nil {
				t.Fatal("accepted invalid operation chronology")
			}
		})
	}
}

func TestMigrationGraphPlanBoundsAggregateBeforeCloning(t *testing.T) {
	root := graphTestModel(t, "graph", "root", ir.Field{Name: "value", GoName: "Value", Kind: ir.FieldChar, MaxLength: 1})
	root.Model.Fields[1].GoName = strings.Repeat("A", 512<<10)
	intent := MigrationIntent{Operations: make([]MigrationOperation, 40)}
	for index := range intent.Operations {
		intent.Operations[index] = MigrationOperation{OperationIndex: index, Kind: MigrationCreateModel, After: root.Model}
	}
	_, err := ResolveMigrationGraphPlan("graph", "0001", false, intent)
	if err == nil || !strings.Contains(err.Error(), "aggregate relation intent byte limit") {
		t.Fatalf("aggregate budget error: %v", err)
	}
}
