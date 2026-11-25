package migrationautodetect

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestAuthoredModelRenameReplaysWithoutCreatingOrDeletingRows(t *testing.T) {
	before := mustModel(t, "blog", testModel("entry", testChar("code", false, nil)))
	after := mustModel(t, "blog", testModel("publication", testChar("code", false, nil)))
	initial := migrations.Migration{App: "blog", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "blog", Model: before}}}
	peer := func(target string) ir.Model {
		return mustModel(t, "news", testModel("note", testForeignKey("item", false, "blog", target, "notes")))
	}
	note := migrations.Migration{App: "news", Name: "0001_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "news", Model: peer("entry")}}}
	rename := migrations.Migration{App: "blog", Name: "0002_rename", Dependencies: []migrations.MigrationKey{initial.Key(), note.Key()}, Operations: []migrations.Operation{migrations.RenameModel{AppLabel: "blog", Before: before, After: after}}}
	desired := mustProjectState(t, testSchema("blog", after), testSchema("news", peer("publication")))
	request := Request{Desired: desired, ManagedApps: desired.Apps(), Definitions: mustLoadDefinitions(t, initial, note)}
	detectError(t, request, CodeUnsupportedChange)
	request.Definitions = mustLoadDefinitions(t, initial, note, rename)
	plan := mustDetect(t, request)
	if !plan.Empty() || !plan.BaseState().Equal(desired) {
		t.Fatal("authored rename produced destructive writer drift", plan.Migrations())
	}
	bad := rename
	bad.Dependencies = []migrations.MigrationKey{initial.Key()}
	if _, err := migrations.NewStateReconstructor(initial, note, bad); err == nil {
		t.Fatal("unordered incoming owner borrowed by model rename")
	}
}
