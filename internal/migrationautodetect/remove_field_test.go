package migrationautodetect

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectFieldRemovalPreservesPositionAndPublishedPrefixes(t *testing.T) {
	base := mustProjectState(t, testSchema("removal", testModel("item", testChar("head", true, nil), testChar("middle", true, nil), testChar("tail", true, nil))))
	desired := mustProjectState(t, testSchema("removal", testModel("item", testChar("tail", true, nil))))
	initial := initialMigrationsFromState(t, base)
	loaded := mustLoadDefinitions(t, initial...)
	request := Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"removal"}}
	plan := mustDetect(t, request).Migrations()
	if len(plan) != 1 || len(plan[0].Operations) != 2 {
		t.Fatal("field removal plan incomplete")
	}
	for i, want := range []struct{ name, anchor string }{{"head", "middle"}, {"middle", "tail"}} {
		operation, ok := plan[0].Operations[i].(migrations.RemoveField)
		if !ok || operation.Field.Name != want.name || operation.BeforeField != want.anchor {
			t.Fatal("removal lost its exact historical position")
		}
	}
	assertGeneratedState(t, loaded, plan, desired)
	if !reflect.DeepEqual(plan, mustDetect(t, request).Migrations()) {
		t.Fatal("removal plan is nondeterministic")
	}
	request.Definitions = mustLoadDefinitions(t, append(initial, plan...)...)
	if remaining := mustDetect(t, request); !remaining.Empty() || !remaining.BaseState().Equal(desired) {
		t.Fatal("published removal did not become a no-op")
	}
}

func TestDetectImageRemovalReleasesDimensionsBeforeDroppingThem(t *testing.T) {
	image := testChar("portrait", true, nil)
	image.Kind, image.WidthField = ir.FieldImage, "width"
	width := ir.Field{Name: "width", GoName: "Width", Kind: ir.FieldInteger, Nullable: true}
	tail := testChar("tail", true, nil)
	before := mustProjectState(t, testSchema("removal", testModel("item", width, image, tail)))
	after := mustProjectState(t, testSchema("removal", testModel("item", tail)))
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, before)...)
	plan := mustDetect(t, Request{Definitions: loaded, Desired: after, ManagedApps: []string{"removal"}}).Migrations()
	if len(plan) != 1 || len(plan[0].Operations) != 2 {
		t.Fatal("image removal plan incomplete")
	}
	for i, name := range []string{"portrait", "width"} {
		operation, ok := plan[0].Operations[i].(migrations.RemoveField)
		if !ok || operation.Field.Name != name || operation.BeforeField != "tail" {
			t.Fatal("dimension removed before its image owner or lost actual anchor")
		}
	}
	assertGeneratedState(t, loaded, plan, after)
}
