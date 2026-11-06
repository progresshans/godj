package projectmigration

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func TestEmptyAppsRetainSnapshotIdentityWithoutFabricatedMigrations(t *testing.T) {
	for _, external := range []bool{false, true} {
		spec := testProjectSpec(testSchema("site"), testSchema("library"))
		spec.Apps[1].External = external
		if external {
			spec.Apps[1].Package.Directory = ""
		}
		request := Request{ProjectSpec: spec, WriterRoot: "migrations"}
		empty := mustBuildSnapshot(t, request)
		if len(empty.ProjectSpec().Apps) != 2 || len(empty.DesiredState().Apps()) != 2 || len(empty.Candidates()) != 0 || empty.FinalSemanticDigest() != definition.EmptySetDigest {
			t.Fatal("empty app snapshot changed identity or history")
		}
		wanted := []string{"library", "site"}
		if external {
			wanted = []string{"site"}
		}
		if !reflect.DeepEqual(empty.ManagedApps(), wanted) {
			t.Fatal("empty app ownership changed")
		}
		request.ProjectSpec.Apps[0].Schema = testSchema("site", testModel("entry", testChar("title", false)))
		first := mustBuildSnapshot(t, request)
		if len(first.Candidates()) != 1 || first.Candidates()[0].App() != "site" {
			t.Fatal("first model changed an unrelated empty app")
		}
		candidate := first.Candidates()[0]
		request.FilesystemSources = []definition.Source{{SourceID: "migrations/site_0001_initial.godj.json", Document: candidate.Document()}}
		if repeated := mustBuildSnapshot(t, request); len(repeated.Candidates()) != 0 {
			t.Fatal("model-free peer creates permanent drift")
		}
		request.ProjectSpec.Apps[0].Schema = testSchema("site")
		_, err := BuildSnapshot(request)
		assertSnapshotError(t, err, CategoryPlanning, CodeUnsupportedChange)
		if external {
			request.ProjectSpec.Apps[0].Schema = testSchema("site", testModel("entry", testChar("title", false)))
			request.ProjectSpec.Apps[1].Schema = testSchema("library", testModel("author", testChar("name", false)))
			_, err := BuildSnapshot(request)
			assertSnapshotError(t, err, CategoryCatalog, CodeInvalidCatalog)
		}
	}
}

func TestFinalStateEmptyAppRuleDoesNotHideUnexpectedOrLostHistory(t *testing.T) {
	empty := migrations.EmptyProjectState()
	declared, err := migrations.NewProjectState(testSchema("site"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyFinalState(empty, declared, empty, []string{"site"}); err != nil {
		t.Fatal(err)
	}
	if err := verifyFinalState(empty, empty, declared, nil); err == nil {
		t.Fatal("unexpected app state was ignored")
	}
	if err := verifyFinalState(declared, declared, empty, []string{"site"}); err == nil {
		t.Fatal("existing state identity disappeared")
	}
	nonempty, err := migrations.NewProjectState(testSchema("site", testModel("entry", testChar("title", false))))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyFinalState(nonempty, declared, empty, []string{"site"}); err == nil {
		t.Fatal("stored model history was ignored")
	}
}
