package definition

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

const oneCreateModelDigest = "sha256:07e61f8d956002cff0d7fe2db10c16ea4a30829e9f0ced09c69c40ff2c2399bc"

func TestZeroSetAndEmptyLoadAreCanonical(t *testing.T) {
	t.Parallel()

	var zero Set
	if zero.Digest() != EmptySetDigest {
		t.Fatalf("zero Set digest = %q, want %q", zero.Digest(), EmptySetDigest)
	}
	if len(zero.Definitions()) != 0 || len(zero.Sources()) != 0 {
		t.Fatalf("zero Set is not empty: definitions=%v sources=%v", zero.Definitions(), zero.Sources())
	}

	loaded, report, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.Digest() != EmptySetDigest {
		t.Fatalf("empty Load digest = %q, want %q", loaded.Digest(), EmptySetDigest)
	}
	want := LoadReport{PlannerConstruction: 1, DefinitionSetsPublished: 1}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("empty Load report = %+v, want %+v", report, want)
	}
	if _, exists := report.Failure(); exists {
		t.Fatal("empty successful Load reports a failure")
	}
}

func TestLoadPublishesOwnedCanonicalSnapshot(t *testing.T) {
	t.Parallel()

	document := oneCreateModelDocument("alpha", "0001_initial")
	input := Source{SourceID: "z-source", Document: document}
	loaded, report, err := Load(input)
	if err != nil {
		t.Fatalf("Load(valid): %v", err)
	}
	if loaded.Digest() != oneCreateModelDigest {
		t.Fatalf("digest = %q, want %q", loaded.Digest(), oneCreateModelDigest)
	}
	wantReport := LoadReport{
		DocumentsReceived:       1,
		HeadersValidated:        1,
		OperationsDecoded:       1,
		PlannerConstruction:     1,
		DefinitionsPublished:    1,
		DefinitionSetsPublished: 1,
	}
	if !reflect.DeepEqual(report, wantReport) {
		t.Fatalf("Load report = %+v, want %+v", report, wantReport)
	}

	definitions := loaded.Definitions()
	if len(definitions) != 1 || definitions[0].Key() != (migrations.MigrationKey{App: "alpha", Name: "0001_initial"}) {
		t.Fatalf("definitions = %#v", definitions)
	}
	operation, ok := definitions[0].Operations[0].(migrations.CreateModel)
	if !ok || operation.Model.Name != "widget" || len(operation.Model.Fields) != 1 {
		t.Fatalf("CreateModel = %#v", definitions[0].Operations[0])
	}
	sources := loaded.Sources()
	wantSources := []SourceInfo{{
		SourceID:  "z-source",
		Producer:  Producer{Name: "godj-example-generator", Version: "0.1.0"},
		Migration: migrations.MigrationKey{App: "alpha", Name: "0001_initial"},
	}}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("sources = %#v, want %#v", sources, wantSources)
	}

	for index := range document {
		document[index] = 'x'
	}
	definitions[0].Name = "mutated"
	operation.Model.Fields[0].Name = "mutated"
	definitions[0].Operations[0] = operation
	sources[0].SourceID = "mutated"

	fresh := loaded.Definitions()
	freshOperation := fresh[0].Operations[0].(migrations.CreateModel)
	if fresh[0].Name != "0001_initial" || freshOperation.Model.Fields[0].Name != "id" {
		t.Fatalf("caller mutation reached Set: %#v", fresh)
	}
	if loaded.Sources()[0].SourceID != "z-source" || loaded.Digest() != oneCreateModelDigest {
		t.Fatal("caller mutation changed source inventory or digest")
	}

	relabelled, _, err := Load(Source{SourceID: "a-relabeled", Document: oneCreateModelDocument("alpha", "0001_initial")})
	if err != nil {
		t.Fatalf("Load(relabeled): %v", err)
	}
	if relabelled.Digest() != loaded.Digest() {
		t.Fatalf("SourceID relabel changed digest: %q != %q", relabelled.Digest(), loaded.Digest())
	}
}

func TestLoadReturnsRawPlanningErrorAndImmutableGraphContext(t *testing.T) {
	t.Parallel()

	_, report, err := Load(
		Source{SourceID: "a-original", Document: oneCreateModelDocument("alpha", "0001_initial")},
		Source{SourceID: "z-duplicate", Document: oneCreateModelDocument("alpha", "0001_initial")},
	)
	var planningError *migrations.PlanningError
	if !errors.As(err, &planningError) {
		t.Fatalf("Load duplicate error = %T %v, want *migrations.PlanningError", err, err)
	}
	if planningError.Code != migrations.CodeDuplicateNode {
		t.Fatalf("planning error code = %q, want %q", planningError.Code, migrations.CodeDuplicateNode)
	}
	context, exists := report.Failure()
	if !exists {
		t.Fatal("graph failure is absent from report")
	}
	if context.Stage != "graph" || context.SourceID != "z-duplicate" || context.JSONPointer != "/migration" || context.Reason != string(migrations.CodeDuplicateNode) {
		t.Fatalf("graph failure context = %+v", context)
	}
	wantSources := []GraphSource{
		{Migration: migrations.MigrationKey{App: "alpha", Name: "0001_initial"}, SourceID: "a-original"},
		{Migration: migrations.MigrationKey{App: "alpha", Name: "0001_initial"}, SourceID: "z-duplicate"},
	}
	gotSources := context.GraphSources()
	if !reflect.DeepEqual(gotSources, wantSources) {
		t.Fatalf("graph sources = %#v, want %#v", gotSources, wantSources)
	}
	gotSources[0].SourceID = "mutated"
	contextAgain, _ := report.Failure()
	if !reflect.DeepEqual(contextAgain.GraphSources(), wantSources) {
		t.Fatal("GraphSources result aliases report state")
	}
	if report.PlannerConstruction != 1 || report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 {
		t.Fatalf("graph failure report = %+v", report)
	}
}

func TestLoadInvokesInjectedPlannerExactlyOnceAndNeverBeforeGraphStage(t *testing.T) {
	t.Parallel()

	valid := []Source{{SourceID: "valid", Document: oneCreateModelDocument("alpha", "0001_initial")}}
	calls := 0
	set, report, err := loadWithPlanner(valid, func(definitions []migrations.Migration) error {
		calls++
		if len(definitions) != 1 || definitions[0].Key() != (migrations.MigrationKey{App: "alpha", Name: "0001_initial"}) {
			t.Fatalf("injected planner definitions = %#v", definitions)
		}
		return nil
	})
	if err != nil || calls != 1 || report.PlannerConstruction != 1 || report.DefinitionSetsPublished != 1 || len(set.Definitions()) != 1 {
		t.Fatalf("successful injected planner = calls:%d set:%#v report:%+v error:%v", calls, set.Definitions(), report, err)
	}

	calls = 0
	_, report, err = loadWithPlanner([]Source{{SourceID: "", Document: valid[0].Document}}, func([]migrations.Migration) error {
		calls++
		return nil
	})
	if err == nil || calls != 0 || report.PlannerConstruction != 0 {
		t.Fatalf("pre-graph failure invoked planner: calls=%d report=%+v error=%v", calls, report, err)
	}

	wantFailure := errors.New("injected graph failure")
	calls = 0
	failed, report, err := loadWithPlanner(valid, func([]migrations.Migration) error {
		calls++
		return wantFailure
	})
	if err != wantFailure || calls != 1 || report.PlannerConstruction != 1 || report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 {
		t.Fatalf("injected graph failure = calls:%d report:%+v error:%v", calls, report, err)
	}
	if failed.Digest() != EmptySetDigest || len(failed.Definitions()) != 0 || len(failed.Sources()) != 0 {
		t.Fatalf("injected graph failure published set: %#v", failed)
	}
	context, exists := report.Failure()
	if !exists || context.Stage != "graph" || context.Reason != "planner_error" {
		t.Fatalf("injected graph failure context = %+v, %v", context, exists)
	}
}

func TestLoadPreservesEveryRawPlanningDiagnosticAndGraphSourceMapping(t *testing.T) {
	t.Parallel()

	root := migrations.MigrationKey{App: "alpha", Name: "0001_root"}
	child := migrations.MigrationKey{App: "alpha", Name: "0002_child"}
	missing := migrations.MigrationKey{App: "alpha", Name: "0999_missing"}
	invalidParent := migrations.MigrationKey{Name: "0000_invalid"}
	cycleA := migrations.MigrationKey{App: "alpha", Name: "0100_cycle_a"}
	cycleB := migrations.MigrationKey{App: "alpha", Name: "0101_cycle_b"}

	tests := []struct {
		name         string
		sources      []Source
		code         migrations.ErrorCode
		node         migrations.MigrationKey
		related      migrations.MigrationKey
		members      []migrations.MigrationKey
		pointer      string
		primary      string
		graphSources []GraphSource
	}{
		{
			name: "invalid node", sources: graphDefinitionSources(t, graphDefinitionSource{"invalid", migrations.MigrationKey{Name: "0001_invalid"}, nil}),
			code: migrations.CodeInvalidNode, node: migrations.MigrationKey{Name: "0001_invalid"}, pointer: "/migration", primary: "invalid",
			graphSources: []GraphSource{{Migration: migrations.MigrationKey{Name: "0001_invalid"}, SourceID: "invalid"}},
		},
		{
			name: "duplicate node", sources: graphDefinitionSources(t,
				graphDefinitionSource{"a-original", root, nil},
				graphDefinitionSource{"z-duplicate", root, nil},
			),
			code: migrations.CodeDuplicateNode, node: root, pointer: "/migration", primary: "z-duplicate",
			graphSources: []GraphSource{{Migration: root, SourceID: "a-original"}, {Migration: root, SourceID: "z-duplicate"}},
		},
		{
			name: "invalid dependency", sources: graphDefinitionSources(t, graphDefinitionSource{"child", child, []migrations.MigrationKey{invalidParent}}),
			code: migrations.CodeInvalidDependency, node: child, related: invalidParent, pointer: "/migration/dependencies", primary: "child",
			graphSources: []GraphSource{{Migration: child, SourceID: "child"}},
		},
		{
			name: "duplicate dependency", sources: graphDefinitionSources(t,
				graphDefinitionSource{"root", root, nil},
				graphDefinitionSource{"child", child, []migrations.MigrationKey{root, root}},
			),
			code: migrations.CodeDuplicateDependency, node: child, related: root, pointer: "/migration/dependencies", primary: "child",
			graphSources: []GraphSource{{Migration: root, SourceID: "root"}, {Migration: child, SourceID: "child"}},
		},
		{
			name: "missing dependency", sources: graphDefinitionSources(t, graphDefinitionSource{"child", child, []migrations.MigrationKey{missing}}),
			code: migrations.CodeDependencyNotFound, node: child, related: missing, pointer: "/migration/dependencies", primary: "child",
			graphSources: []GraphSource{{Migration: child, SourceID: "child"}},
		},
		{
			name: "dependency cycle", sources: graphDefinitionSources(t,
				graphDefinitionSource{"cycle-a", cycleA, []migrations.MigrationKey{cycleB}},
				graphDefinitionSource{"cycle-b", cycleB, []migrations.MigrationKey{cycleA}},
			),
			code: migrations.CodeDependencyCycle, members: []migrations.MigrationKey{cycleA, cycleB}, pointer: "/migration/dependencies", primary: "cycle-a",
			graphSources: []GraphSource{{Migration: cycleA, SourceID: "cycle-a"}, {Migration: cycleB, SourceID: "cycle-b"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set, report, err := Load(test.sources...)
			var planningError *migrations.PlanningError
			if !errors.As(err, &planningError) {
				t.Fatalf("error = %T %v, want *migrations.PlanningError", err, err)
			}
			if planningError.Category != migrations.CategoryGraph || planningError.Code != test.code || planningError.Node != test.node || planningError.Related != test.related || !reflect.DeepEqual(planningError.Members(), test.members) {
				t.Fatalf("planning diagnostic = category:%s code:%s node:%v related:%v members:%v", planningError.Category, planningError.Code, planningError.Node, planningError.Related, planningError.Members())
			}
			context, exists := report.Failure()
			if !exists || context.Stage != "graph" || context.JSONPointer != test.pointer || context.SourceID != test.primary || context.Reason != string(test.code) || !reflect.DeepEqual(context.GraphSources(), test.graphSources) {
				t.Fatalf("graph report context = %+v sources=%#v, want source=%q pointer=%q sources=%#v", context, context.GraphSources(), test.primary, test.pointer, test.graphSources)
			}
			if report.PlannerConstruction != 1 || report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 || set.Digest() != EmptySetDigest || len(set.Definitions()) != 0 || len(set.Sources()) != 0 {
				t.Fatalf("graph failure was not atomic: set=%#v report=%+v", set, report)
			}
		})
	}
}

func TestLoadResourceLimitsFailBeforeLaterStages(t *testing.T) {
	t.Parallel()

	t.Run("source count", func(t *testing.T) {
		sources := make([]Source, MaxSources+1)
		_, report, err := Load(sources...)
		assertLimitFailure(t, report, err, CodeInvalidSource, "source", "source_count", MaxSources, MaxSources+1)
	})

	t.Run("source ID bytes", func(t *testing.T) {
		_, report, err := Load(Source{SourceID: strings.Repeat("x", MaxSourceIDBytes+1)})
		assertLimitFailure(t, report, err, CodeInvalidSource, "source", "source_id_bytes", MaxSourceIDBytes, MaxSourceIDBytes+1)
	})
}

func TestSetConcurrentReadsReturnIndependentSnapshots(t *testing.T) {
	t.Parallel()

	loaded, _, err := Load(Source{SourceID: "source", Document: oneCreateModelDocument("alpha", "0001_initial")})
	if err != nil {
		t.Fatalf("Load(valid): %v", err)
	}
	const workers = 32
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			definitions := loaded.Definitions()
			definitions[0].Name = "mutated"
			if loaded.Digest() != oneCreateModelDigest || loaded.Definitions()[0].Name != "0001_initial" {
				t.Errorf("concurrent accessor observed mutation")
			}
		}()
	}
	group.Wait()
}

func TestDefinitionsAccessorDeepCopiesNestedDefaultPointers(t *testing.T) {
	t.Parallel()

	loaded, report, err := Load(
		Source{SourceID: "root", Document: lifecycleRootDocument()},
		Source{SourceID: "tail", Document: lifecycleTailDocument()},
	)
	if err != nil {
		t.Fatalf("Load lifecycle definitions: %v", err)
	}
	wantDigest := loaded.Digest()
	first := loaded.Definitions()
	second := loaded.Definitions()
	create := first[0].Operations[0].(migrations.CreateModel)
	add := first[1].Operations[0].(migrations.AddField)
	if create.Model.Fields[1].Default == nil || add.Field.Default == nil {
		t.Fatalf("nested defaults are absent: create=%#v add=%#v", create, add)
	}
	create.Model.Fields[1].Default.String = "mutated"
	add.Field.Default.Boolean = true
	first[0].Operations[0] = create
	first[1].Operations[0] = add

	secondCreate := second[0].Operations[0].(migrations.CreateModel)
	secondAdd := second[1].Operations[0].(migrations.AddField)
	fresh := loaded.Definitions()
	freshCreate := fresh[0].Operations[0].(migrations.CreateModel)
	freshAdd := fresh[1].Operations[0].(migrations.AddField)
	if secondCreate.Model.Fields[1].Default.String != "untitled" || secondAdd.Field.Default.Boolean || freshCreate.Model.Fields[1].Default.String != "untitled" || freshAdd.Field.Default.Boolean {
		t.Fatalf("nested default mutation escaped accessor copy: second=%#v/%#v fresh=%#v/%#v", secondCreate.Model.Fields[1].Default, secondAdd.Field.Default, freshCreate.Model.Fields[1].Default, freshAdd.Field.Default)
	}
	if loaded.Digest() != wantDigest || report.DefinitionSetsPublished != 1 || report.DefinitionsPublished != 2 {
		t.Fatalf("accessor mutation changed set/report: digest=%q/%q report=%+v", loaded.Digest(), wantDigest, report)
	}
}

func TestLoadedSetMigratesThroughIndependentRevisionFencedExecutors(t *testing.T) {
	ctx := context.Background()
	loaded, report, err := Load(
		Source{SourceID: "opaque-z-root", Document: lifecycleRootDocument()},
		Source{SourceID: "opaque-a-tail", Document: lifecycleTailDocument()},
	)
	if err != nil {
		t.Fatalf("Load lifecycle definitions: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 3 || report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("lifecycle load report = %+v", report)
	}
	definitions := loaded.Definitions()
	if len(definitions) != 2 {
		t.Fatalf("definitions = %#v", definitions)
	}
	booleanAdd, ok := definitions[1].Operations[0].(migrations.AddField)
	if !ok || booleanAdd.Field.Default == nil || booleanAdd.Field.Default.Boolean {
		t.Fatalf("explicit false BooleanField default was not preserved: %#v", definitions[1].Operations[0])
	}
	definitions[0].Name = "caller_mutated"
	booleanAdd.Field.Default.Boolean = true
	definitions[1].Operations[0] = booleanAdd

	for run := 0; run < 2; run++ {
		backend, openErr := sqlite.OpenMemory(ctx, "definition-set-migrate-"+t.Name()+"-"+string(rune('a'+run)))
		if openErr != nil {
			t.Fatalf("OpenMemory(%d): %v", run, openErr)
		}
		state, migrateErr := loaded.Migrate(
			ctx,
			migrations.Executor{Backend: backend},
			migrations.LatestLifecycleRequest(),
		)
		closeErr := backend.Close()
		if migrateErr != nil {
			t.Fatalf("Set.Migrate(%d): %v", run, migrateErr)
		}
		if closeErr != nil {
			t.Fatalf("close backend %d: %v", run, closeErr)
		}
		model, exists := state.Model("alpha", "entry")
		if !exists {
			t.Fatalf("Set.Migrate(%d) state has no alpha.entry", run)
		}
		gotFields := make([]string, len(model.Fields))
		for index, field := range model.Fields {
			gotFields[index] = field.Name
		}
		wantFields := []string{"id", "title", "published", "summary"}
		if !reflect.DeepEqual(gotFields, wantFields) {
			t.Fatalf("Set.Migrate(%d) fields = %v, want %v", run, gotFields, wantFields)
		}
	}
}

func TestSetMigrateCallsTheExistingLifecycleOnceAndPreservesItsRawCause(t *testing.T) {
	t.Parallel()

	loaded, _, err := Load(Source{SourceID: "source", Document: oneCreateModelDocument("alpha", "0001_initial")})
	if err != nil {
		t.Fatalf("Load(valid): %v", err)
	}
	raw := errors.New("raw fenced session open failure")
	backend := &definitionHandoffFailureBackend{openErr: raw}
	_, err = loaded.Migrate(
		context.Background(),
		migrations.Executor{Backend: backend},
		migrations.LatestLifecycleRequest(),
	)
	if backend.openCalls != 1 {
		t.Fatalf("Set.Migrate backend open calls = %d, want 1", backend.openCalls)
	}
	if !errors.Is(err, raw) {
		t.Fatalf("Set.Migrate error = %T %v, want raw lifecycle cause", err, err)
	}
	var migrationError *migrations.Error
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryTransaction || migrationError.Code != migrations.CodeBeginFailed {
		t.Fatalf("Set.Migrate lifecycle classification = %#v", migrationError)
	}
}

func assertLimitFailure(t *testing.T, report LoadReport, err error, code ErrorCode, stage, limit string, maximum, actual int) {
	t.Helper()
	var definitionError *Error
	if !errors.As(err, &definitionError) {
		t.Fatalf("error = %T %v, want *definition.Error", err, err)
	}
	if definitionError.Category != CategorySource || definitionError.Code != code {
		t.Fatalf("error = %+v", definitionError)
	}
	context := definitionError.Context()
	if context.Stage != stage || context.Reason != "resource_limit_exceeded" || context.Limit != limit || context.Maximum != uint64(maximum) || context.Actual != uint64(actual) {
		t.Fatalf("limit context = %+v", context)
	}
	reported, exists := report.Failure()
	if !exists || !reflect.DeepEqual(reported, context) {
		t.Fatalf("report failure = %+v, %v; error context = %+v", reported, exists, context)
	}
	if report.PlannerConstruction != 0 || report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 {
		t.Fatalf("resource report publishes partial state: %+v", report)
	}
}

func oneCreateModelDocument(app, name string) []byte {
	return []byte(`{
  "compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},
  "producer":{"name":"godj-example-generator","version":"0.1.0"},
  "migration":{"app":"` + app + `","name":"` + name + `","dependencies":[],"operations":[
    {"kind":"create_model","app_label":"` + app + `","model":{"name":"widget","go_name":"Widget","db_table":"alpha_widget","fields":[
      {"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}
    ]}}
  ]}
}`)
}

func lifecycleRootDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"producer":{"name":"godj-reference","version":"0.1.0"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[{"kind":"create_model","app_label":"alpha","model":{"name":"entry","go_name":"Entry","db_table":"godj_definition_alpha_entry","fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null},{"name":"title","go_name":"Title","column":"title","kind":"char","primary_key":false,"nullable":false,"max_length":64,"default":{"kind":"string","string":"untitled"}}]}}]}}`)
}

func lifecycleTailDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"producer":{"name":"godj-reference","version":"0.1.0"},"migration":{"app":"alpha","name":"0002_fields","dependencies":[{"app":"alpha","name":"0001_initial"}],"operations":[{"kind":"add_field","app_label":"alpha","model_name":"entry","field":{"name":"published","go_name":"Published","column":"published","kind":"boolean","primary_key":false,"nullable":false,"max_length":0,"default":{"kind":"boolean","boolean":false}}},{"kind":"add_field","app_label":"alpha","model_name":"entry","field":{"name":"summary","go_name":"Summary","column":"summary","kind":"char","primary_key":false,"nullable":true,"max_length":255,"default":null}}]}}`)
}

type graphDefinitionSource struct {
	sourceID     string
	key          migrations.MigrationKey
	dependencies []migrations.MigrationKey
}

func graphDefinitionSources(t *testing.T, definitions ...graphDefinitionSource) []Source {
	t.Helper()
	sources := make([]Source, len(definitions))
	for index, definition := range definitions {
		dependencies := make([]map[string]string, len(definition.dependencies))
		for dependencyIndex, dependency := range definition.dependencies {
			dependencies[dependencyIndex] = map[string]string{"app": dependency.App, "name": dependency.Name}
		}
		document, err := json.Marshal(map[string]any{
			"compatibility": map[string]int64{
				"definition_format": DefinitionFormatVersion,
				"loader_abi":        LoaderABIVersion,
				"operation_codec":   OperationCodecVersion,
				"schema_ir":         SchemaIRVersion,
			},
			"producer": map[string]string{"name": "graph-test", "version": "1"},
			"migration": map[string]any{
				"app":          definition.key.App,
				"name":         definition.key.Name,
				"dependencies": dependencies,
				"operations":   []any{},
			},
		})
		if err != nil {
			t.Fatalf("marshal graph definition %s: %v", definition.sourceID, err)
		}
		sources[index] = Source{SourceID: definition.sourceID, Document: document}
	}
	return sources
}

type definitionHandoffFailureBackend struct {
	openCalls int
	openErr   error
}

func (backend *definitionHandoffFailureBackend) BeginMigration(context.Context) (migrationbackend.Transaction, error) {
	return nil, errors.New("legacy migration path must not run")
}

func (backend *definitionHandoffFailureBackend) OpenRevisionFencedSession(context.Context) (migrationbackend.RevisionFencedSession, error) {
	backend.openCalls++
	return nil, backend.openErr
}
