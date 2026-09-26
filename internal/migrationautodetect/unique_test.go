package migrationautodetect

import (
	"testing"

	"github.com/progresshans/godj/migrations"
)

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
	field.Unique, field.Nullable = false, false
	mixed := mustProjectState(t, testSchema("content", testModel("article", field)))
	if _, err := Detect(Request{Definitions: mustLoadDefinitions(t, history...), Desired: mixed, ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("unique change absorbed nullability")
	}
}
