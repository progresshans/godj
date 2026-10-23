package migrationautodetect

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectOneToOneAndReverseNamespaceFromHistory(t *testing.T) {
	field := testForeignKey("ticket", false, "support", "ticket", "reports")
	state := func(field ir.Field) migrations.ProjectState {
		return mustProjectState(t, testSchema("support", testModel("ticket", testChar("subject", false, nil)), testModel("report", field)))
	}
	base := state(field)
	model, _ := base.Model("support", "report")
	field = model.Fields[1].Clone()
	history := initialMigrationsFromState(t, base)
	for _, change := range []struct {
		cardinality ir.RelationCardinality
		unique      bool
		reverse     ir.ReverseRelation
	}{
		{ir.RelationOneToOne, true, ir.ReverseRelation{Name: "report"}},
		{ir.RelationOneToOne, true, ir.ReverseRelation{Name: "service_report"}},
		{ir.RelationOneToOne, true, ir.ReverseRelation{Disabled: true}},
		{ir.RelationManyToOne, true, ir.ReverseRelation{Name: "reports"}},
		{ir.RelationManyToOne, false, ir.ReverseRelation{Name: "all_reports"}},
	} {
		before := field.Clone()
		field.Unique = change.unique
		field.Relation.Cardinality = change.cardinality
		field.Relation.Reverse = change.reverse
		desired := state(field)
		loaded := mustLoadDefinitions(t, history...)
		changes := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"support"}}).Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("relation change did not produce exactly one operation")
		}
		alter, ok := changes[0].Operations[0].(migrations.AlterField)
		if !ok || !alter.Before.Equal(before) || !alter.After.Equal(field) {
			t.Fatal("historical cardinality/reverse/unique state was lost")
		}
		assertGeneratedState(t, loaded, changes, desired)
		history = append(history, changes...)
		if !mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"support"}}).Empty() {
			t.Fatal("one-to-one/reverse namespace change is not stable after replay")
		}
	}
}
