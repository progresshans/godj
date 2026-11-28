package migrationautodetect

import (
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectModelDeletionReleasesReferencesToOwnedStorage(t *testing.T) {
	const app = "manyhistory"
	history, models, _ := manytomanytest.AutomaticHistory(t, false, false)
	initial, link := history[0], history[1]
	field := testForeignKey("link", true, app, "owner_labels", "")
	field.Relation.Reverse = ir.ReverseRelation{Disabled: true}
	field, err := ir.NormalizeField(field)
	if err != nil {
		t.Fatal(err)
	}
	ownRef := migrations.Migration{App: app, Name: "0003_link", Dependencies: []migrations.MigrationKey{link.Key()}, Operations: []migrations.Operation{
		migrations.AddField{AppLabel: app, ModelName: "owner", Field: field},
	}}
	want := mustProjectState(t, testSchema(app, models[1]))
	request := Request{Definitions: mustLoadDefinitions(t, initial, link, ownRef), Desired: want, ManagedApps: []string{app}}
	steps := mustDetect(t, request).Migrations()
	if len(steps) != 2 || len(steps[0].Operations) != 1 || len(steps[1].Operations) != 1 {
		t.Fatal("expected reference release followed by model deletion", steps)
	}
	remove, ok := steps[0].Operations[0].(migrations.RemoveField)
	drop, retired := steps[1].Operations[0].(migrations.DeleteModel)
	if !ok || remove.ModelName != "owner" || remove.Field.Name != "link" || !retired || drop.Model.Name != "owner" || drop.BeforeModel != "label" {
		t.Fatal("intermediary is still in use when its owner is deleted", steps)
	}
	assertGeneratedState(t, request.Definitions, steps, want)

	other := mustModel(t, "audit", testModel("entry", field))
	retained := migrations.Migration{App: "audit", Name: "0001_entry", Dependencies: []migrations.MigrationKey{link.Key()}, Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "audit", Model: other},
	}}
	request.Definitions = mustLoadDefinitions(t, initial, link, retained)
	detectError(t, request, CodeInvalidRelation)
}

func TestDetectModelDeletionOrdersCrossAppCyclesAndReplaysEveryPrefix(t *testing.T) {
	current := mustProjectState(t,
		testSchema("people", testModel("writer", testForeignKey("featured", true, "blog", "post", "owners")), testModel("keep")),
		testSchema("blog", testModel("post", testForeignKey("writer", false, "people", "writer", "posts"))),
	)
	managed := []string{"people", "blog"}
	base := mustDetect(t, Request{Definitions: mustLoadDefinitions(t), Desired: current, ManagedApps: managed}).Migrations()
	want := mustProjectState(t, testSchema("people", testModel("keep")), testSchema("blog"))
	request := Request{Definitions: mustLoadDefinitions(t, base...), Desired: want, ManagedApps: managed}
	steps := mustDetect(t, request).Migrations()
	seen := map[string]bool{}
	released := false
	for _, step := range steps {
		for _, op := range step.Operations {
			switch op := op.(type) {
			case migrations.RemoveField:
				released = true
			case migrations.DeleteModel:
				seen[op.Model.Name] = true
				if op.Model.Name == "writer" && op.BeforeModel != "keep" {
					t.Fatal("lost historical model position", op)
				}
			}
		}
	}
	if !released || len(seen) != 2 || !seen["writer"] || !seen["post"] {
		t.Fatal("incomplete deletion plan", steps)
	}
	for i := 0; i <= len(steps); i++ {
		request.Definitions = mustLoadDefinitions(t, append(slices.Clone(base), steps[:i]...)...)
		got := mustDetect(t, request).Migrations()
		if len(got) != len(steps[i:]) || len(got) != 0 && !reflect.DeepEqual(got, steps[i:]) {
			t.Fatal("different suffix after durable prefix", i, got)
		}
	}
	r, err := request.Definitions.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := r.Reconstruct(migrations.LatestStateRequest())
	if err != nil || !state.Equal(mustProjectState(t, testSchema("people", testModel("keep")))) {
		t.Fatal("wrong final historical state", state, err)
	}
}
