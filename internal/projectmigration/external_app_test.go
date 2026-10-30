package projectmigration

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func TestImportedAppSnapshotRetainsOwnershipAndRefusesHostMigrations(t *testing.T) {
	library := testSchema("library", testModel("author", testChar("name", false)))
	host := testSchema("content", testModel("entry", testChar("title", false)))
	spec := testProjectSpec(host, library)
	spec.Apps[1].External = true
	spec.Apps[1].Package.Directory = ""
	source := initialSource(t, "embedded/library", library, definition.Producer{Name: "library", Version: "1"})
	request := Request{ProjectSpec: spec, ProgrammaticSources: []definition.Source{source}, WriterRoot: "migrations"}
	snapshot, err := BuildSnapshot(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.ManagedApps(), []string{"content"}) || len(snapshot.Candidates()) != 1 || snapshot.Candidates()[0].App() != "content" {
		t.Fatal("host assumed imported migration ownership")
	}
	owned := snapshot.ProjectSpec()
	if !owned.Apps[1].External || owned.Apps[1].Package.Directory != "" {
		t.Fatal("normalization lost imported file ownership")
	}
	owned.Apps[1].External = false
	owned.Apps[1].Schema.Models[0].Fields[1].Blank = true
	if !snapshot.ProjectSpec().Apps[1].External || snapshot.ProjectSpec().Apps[1].Schema.Models[0].Fields[1].Blank {
		t.Fatal("snapshot getter retained caller aliases")
	}
	for _, mode := range []string{"missing_history", "different_history", "filesystem_ownership"} {
		t.Run(mode, func(t *testing.T) {
			changed := request
			switch mode {
			case "missing_history":
				changed.ProgrammaticSources = nil
			case "different_history":
				changed.ProjectSpec = cloneProjectSpec(spec)
				changed.ProjectSpec.Apps[1].Schema.Models[0].Fields[0].Blank = true
			case "filesystem_ownership":
				changed.ProgrammaticSources = nil
				changed.FilesystemSources = []definition.Source{{SourceID: "migrations/library_0001_initial.godj.json", Document: source.Document}}
			}
			result, err := BuildSnapshot(changed)
			assertSnapshotError(t, err, CategoryCatalog, CodeInvalidCatalog)
			if result.Initialized() || len(result.Candidates()) != 0 {
				t.Fatal("failed dependency check published candidates")
			}
		})
	}
}

func TestImportedAppHistorySupportsOwnedForeignKeyWithoutPublishingDependency(t *testing.T) {
	library := testSchema("library", testModel("author", testChar("name", false)))
	host := testSchema("content", testModel("entry", testChar("title", false), testForeignKey("author", false, "library", "author")))
	spec := testProjectSpec(host, library)
	spec.Apps[1].External = true
	spec.Apps[1].Package.Directory = ""
	source := initialSource(t, "embedded/library", library, definition.Producer{Name: "library", Version: "1"})
	request := Request{ProjectSpec: spec, ProgrammaticSources: []definition.Source{source}, WriterRoot: "migrations"}
	snapshot, err := BuildSnapshot(request)
	if err != nil {
		t.Fatal(err)
	}
	candidates := snapshot.Candidates()
	if len(candidates) != 1 || candidates[0].App() != "content" {
		t.Fatal("host migration ownership", len(candidates))
	}
	document := candidates[0].Document()
	loaded, _, err := definition.Load(source, definition.Source{SourceID: "migrations/content_0001_initial.godj.json", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	reconstructor, err := loaded.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := state.Schema("content")
	desired, _ := snapshot.DesiredState().Schema("content")
	if !reflect.DeepEqual(actual, desired) {
		t.Fatal("owned foreign key was not reconstructed with imported history")
	}
	request.FilesystemSources = []definition.Source{{SourceID: "migrations/content_0001_initial.godj.json", Document: document}}
	second, err := BuildSnapshot(request)
	if err != nil || len(second.Candidates()) != 0 || !reflect.DeepEqual(second.ManagedApps(), []string{"content"}) {
		t.Fatal("dependency declaration created migration drift", err)
	}
}
