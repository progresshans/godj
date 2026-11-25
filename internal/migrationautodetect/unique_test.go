package migrationautodetect

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectNullabilityAndUniqueFromPopulatedModelHistory(t *testing.T) {
	field := testChar("external_id", true, nil)
	base := mustProjectState(t, testSchema("tickets", testModel("ticket", field)))
	model, _ := base.Model("tickets", "ticket")
	optional := model.Fields[1]
	required := optional.Clone()
	required.Nullable, required.Unique = false, true
	filled := required.Clone()
	filled.Default = &ir.Scalar{Kind: ir.ScalarString, String: "한'글"}
	before := optional
	history := initialMigrationsFromState(t, base)
	for _, after := range []ir.Field{required, optional, filled, optional} {
		desired := mustProjectState(t, testSchema("tickets", testModel("ticket", after)))
		request := Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"tickets"}}
		changes := mustDetect(t, request).Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 || len(changes[0].Dependencies) != 1 || changes[0].Dependencies[0] != history[len(history)-1].Key() {
			t.Fatal("expected one AlterField against the published predecessor", changes)
		}
		alter, ok := changes[0].Operations[0].(migrations.AlterField)
		if !ok || !alter.Before.Equal(before) || !alter.After.Equal(after) {
			t.Fatal("writer lost the old or new field", changes)
		}
		assertGeneratedState(t, request.Definitions, changes, desired)
		history = append(history, changes...)
		request.Definitions = mustLoadDefinitions(t, history...)
		if !mustDetect(t, request).Empty() {
			t.Fatal("published field transition did not reach a stable no-op")
		}
		before = after
	}
}

func TestDetectUniqueAdditionRemovalAndRepeatFromHistory(t *testing.T) {
	field := testChar("reference", true, nil)
	base := mustProjectState(t, testSchema("content", testModel("article", field)))
	model, _ := base.Model("content", "article")
	field = model.Fields[1].Clone()
	history := initialMigrationsFromState(t, base)
	for _, unique := range []bool{true, false, true} {
		before := field.Clone()
		field.Unique = unique
		desired := mustProjectState(t, testSchema("content", testModel("article", field)))
		loaded := mustLoadDefinitions(t, history...)
		changes := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}).Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("unique change did not produce one operation")
		}
		alter, ok := changes[0].Operations[0].(migrations.AlterField)
		if !ok || !alter.Before.Equal(before) || !alter.After.Equal(field) {
			t.Fatalf("unique change lost exact before/after: %#v", changes[0].Operations[0])
		}
		assertGeneratedState(t, loaded, changes, desired)
		history = append(history, changes...)
		if !mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}}).Empty() {
			t.Fatal("unique change did not reach a no-op")
		}
	}
	field.Unique, field.MaxLength = false, field.MaxLength+1
	mixed := mustProjectState(t, testSchema("content", testModel("article", field)))
	if _, err := Detect(Request{Definitions: mustLoadDefinitions(t, history...), Desired: mixed, ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("unique change absorbed a storage length change")
	}
}
