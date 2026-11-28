package migrationautodetect

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectUUIDAdditionNoOpAndRemoval(t *testing.T) {
	field := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldUUID, Nullable: true}
	before := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
	initial := initialMigrationsFromState(t, before)
	loaded := mustLoadDefinitions(t, initial...)
	desired := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil), field)))
	added := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}).Migrations()
	if len(added) != 1 || len(added[0].Operations) != 1 {
		t.Fatal("UUID addition did not produce one operation")
	}
	operation, ok := added[0].Operations[0].(migrations.AddField)
	if !ok || !operation.Field.Equal(field) {
		t.Fatal("UUID addition lost field semantics")
	}
	assertGeneratedState(t, loaded, added, desired)
	reloaded := mustLoadDefinitions(t, append(initial, added...)...)
	if !mustDetect(t, Request{Definitions: reloaded, Desired: desired, ManagedApps: []string{"content"}}).Empty() {
		t.Fatal("UUID no-op generated migration")
	}
	removed := mustDetect(t, Request{Definitions: reloaded, Desired: before, ManagedApps: []string{"content"}}).Migrations()
	if len(removed) != 1 || len(removed[0].Operations) != 1 {
		t.Fatal("UUID removal did not produce one operation")
	}
	removal, ok := removed[0].Operations[0].(migrations.RemoveField)
	if !ok || !removal.Field.Equal(field) || removal.BeforeField != "" {
		t.Fatal("UUID removal lost its full historical field or position")
	}
	assertGeneratedState(t, reloaded, removed, before)
	contracted := mustLoadDefinitions(t, append(reloaded.Definitions(), removed...)...)
	if !mustDetect(t, Request{Definitions: contracted, Desired: before, ManagedApps: []string{"content"}}).Empty() {
		t.Fatal("UUID removal did not become a no-op")
	}
}
