package definitionload

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
)

type handoffObservation struct {
	Calls                    int
	ObservedDigest           string
	SourceReadsAfterSnapshot int
}

type testHandoffCoordinator struct {
	afterSnapshot func()
	observation   handoffObservation
}

func (c *testHandoffCoordinator) migrate(
	ctx context.Context,
	executor migrations.Executor,
	loaded loadedDefinitionSet,
	request migrations.LifecycleRequest,
) (migrations.ProjectState, error) {
	// The coordinator owns the digest as an observation. The only values passed
	// through the existing public lifecycle boundary are a fresh definitions
	// snapshot and the lifecycle request.
	c.observation.ObservedDigest = loaded.Digest
	definitionSnapshot := cloneMigrations(loaded.Definitions)
	if c.afterSnapshot != nil {
		c.afterSnapshot()
	}
	c.observation.Calls++
	return executor.Migrate(ctx, definitionSnapshot, request)
}

func TestLoadedDefinitionsReachPublicExecutorExactlyOnce(t *testing.T) {
	ctx := context.Background()
	sources := definitionSources(t)
	loaded, metrics, err := loadDefinitions(sources)
	if err != nil {
		t.Fatalf("load lifecycle definitions: %v", err)
	}
	if metrics.PlannerConstruction != 1 || metrics.DefinitionSetsPublished != 1 {
		t.Fatalf("loader construction metrics = %+v", metrics)
	}
	wantDigest := loaded.Digest
	wantDefinitions := cloneMigrations(loaded.Definitions)

	for sourceIndex := range sources {
		for byteIndex := range sources[sourceIndex].Document {
			sources[sourceIndex].Document[byteIndex] = 'x'
		}
		sources[sourceIndex].SourceID = "mutated-after-load"
	}
	if loaded.Digest != wantDigest || !reflect.DeepEqual(loaded.Definitions, wantDefinitions) {
		t.Fatal("source mutation after load changed the published snapshot")
	}

	backend, err := sqlite.OpenMemory(ctx, "definitionload-lifecycle-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("close SQLite backend: %v", closeErr)
		}
	})

	coordinator := &testHandoffCoordinator{}
	coordinator.afterSnapshot = func() {
		loaded.Definitions[0].Name = "mutated_after_coordinator_snapshot"
		if create, ok := loaded.Definitions[0].Operations[0].(migrations.CreateModel); ok {
			create.Model.Fields[1].Name = "mutated_title"
			loaded.Definitions[0].Operations[0] = create
		}
		loaded.Digest = "sha256:caller-mutated-observation"
	}
	state, err := coordinator.migrate(
		ctx,
		migrations.Executor{Backend: backend},
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		t.Fatalf("Executor.Migrate handoff: %v", err)
	}
	if coordinator.observation.Calls != 1 {
		t.Fatalf("Executor.Migrate handoff calls = %d, want 1", coordinator.observation.Calls)
	}
	if coordinator.observation.ObservedDigest != wantDigest {
		t.Fatalf("coordinator digest = %q, want %q", coordinator.observation.ObservedDigest, wantDigest)
	}
	if coordinator.observation.SourceReadsAfterSnapshot != 0 {
		t.Fatalf("source reads after snapshot = %d", coordinator.observation.SourceReadsAfterSnapshot)
	}

	model, exists := state.Model("alpha", "entry")
	if !exists {
		t.Fatal("final lifecycle state has no alpha.entry model")
	}
	wantFields := []string{"id", "title", "published", "summary"}
	gotFields := make([]string, len(model.Fields))
	for index, field := range model.Fields {
		gotFields[index] = field.Name
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("final lifecycle fields = %v, want %v", gotFields, wantFields)
	}
	if _, err := backend.ExecContext(
		ctx,
		`INSERT INTO "godj_definition_entry" ("title", "published", "summary") VALUES (?, ?, ?)`,
		"proof",
		true,
		"public lifecycle reached SQLite",
	); err != nil {
		t.Fatalf("insert through final SQLite schema: %v", err)
	}

	if stateApps := state.Apps(); !reflect.DeepEqual(stateApps, []string{"alpha"}) {
		t.Fatalf("final lifecycle apps = %v", stateApps)
	}
	if got := state.FormatVersion(); got != 1 {
		t.Fatalf("final state format version = %d", got)
	}
}
