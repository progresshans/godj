package definition

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/internal/definitionhandoff"
	"github.com/progresshans/godj/schema/ir"
)

func TestLoadRelationProfileGoldenDigestAndMixedPlanner(t *testing.T) {
	t.Parallel()

	relationSource := Source{SourceID: "relation-blog-author", Document: relationDefinitionDocument("test-only-relation-candidate", "0.1.0", nil)}
	relation, relationReport, err := Load(relationSource)
	if err != nil {
		t.Fatalf("Load(relation): %v", err)
	}
	const relationDigest = "sha256:5abaa4dff57b7454d1526cb88917390d5593b5c297be12eebbb8bb175d1fa682"
	if relation.Digest() != relationDigest || relation.handoff.IsZero() {
		t.Fatalf("relation set = digest:%q zero-handoff:%t", relation.Digest(), relation.handoff.IsZero())
	}
	if !reflect.DeepEqual(relationReport, LoadReport{
		DocumentsReceived: 1, HeadersValidated: 1, OperationsDecoded: 1, PlannerConstruction: 1,
		DefinitionsPublished: 1, DefinitionSetsPublished: 1,
	}) {
		t.Fatalf("relation report = %+v", relationReport)
	}
	definitions := relation.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("definitions = %#v", definitions)
	}
	operation, ok := definitions[0].Operations[0].(migrations.AddField)
	if !ok || operation.Field.Kind != ir.FieldForeignKey || operation.Field.Relation == nil ||
		operation.Field.Relation.Target != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
		operation.Field.Relation.Reverse != (ir.ReverseRelation{Name: "articles"}) ||
		operation.Field.Relation.OnDelete != ir.DeleteProtect {
		t.Fatalf("relation operation = %#v", definitions[0].Operations[0])
	}

	mixedRelationDocument := relationDefinitionDocument("test-only-relation-candidate", "0.1.0", nil)
	mixed, mixedReport, err := Load(
		Source{SourceID: "relation-blog-author", Document: mixedRelationDocument},
		Source{SourceID: "opaque-z-root", Document: lifecycleRootDocument()},
	)
	if err != nil {
		t.Fatalf("Load(mixed cross-profile graph): %v", err)
	}
	const mixedDigest = "sha256:08127d3e13bcedaedb52bf80b9ae2281b4ab596481d31f5b1f78d749fdae1644"
	if mixed.Digest() != mixedDigest || mixed.handoff.IsZero() || mixedReport.PlannerConstruction != 1 {
		t.Fatalf("mixed set = digest:%q zero-handoff:%t report:%+v", mixed.Digest(), mixed.handoff.IsZero(), mixedReport)
	}
	permuted, _, err := Load(
		Source{SourceID: "opaque-z-root", Document: lifecycleRootDocument()},
		Source{SourceID: "relation-blog-author", Document: mixedRelationDocument},
	)
	if err != nil || permuted.Digest() != mixed.Digest() {
		t.Fatalf("permuted mixed digest = %q/%q error=%v", permuted.Digest(), mixed.Digest(), err)
	}
	dependentDocument := relationDefinitionDocument(
		"test-only-relation-candidate",
		"0.1.0",
		[]byte(`{"app":"alpha","name":"0001_initial"}`),
	)
	dependent, dependentReport, err := Load(
		Source{SourceID: "relation-blog-author", Document: dependentDocument},
		Source{SourceID: "opaque-z-root", Document: lifecycleRootDocument()},
	)
	if err != nil || len(dependent.Definitions()) != 2 || dependentReport.PlannerConstruction != 1 {
		t.Fatalf("cross-profile dependency = definitions:%d report:%+v error:%v", len(dependent.Definitions()), dependentReport, err)
	}
}

func TestRelationDigestExcludesSourceAndProducerAndSnapshotsAliases(t *testing.T) {
	t.Parallel()

	document := relationDefinitionDocument("first", "1", nil)
	loaded, _, err := Load(Source{SourceID: "first-source", Document: document})
	if err != nil {
		t.Fatalf("Load(first): %v", err)
	}
	equivalent, _, err := Load(Source{SourceID: "renamed-source", Document: relationDefinitionDocument("different", "9", nil)})
	if err != nil {
		t.Fatalf("Load(equivalent): %v", err)
	}
	if loaded.Digest() != equivalent.Digest() {
		t.Fatalf("metadata changed digest: %q != %q", loaded.Digest(), equivalent.Digest())
	}

	for index := range document {
		document[index] = 'x'
	}
	definitions := loaded.Definitions()
	operation := definitions[0].Operations[0].(migrations.AddField)
	operation.Field.Relation.Target.AppLabel = "mutated"
	operation.Field.Relation.Reverse.Name = "mutated"
	definitions[0].Operations[0] = operation
	fresh := loaded.Definitions()[0].Operations[0].(migrations.AddField)
	if fresh.Field.Relation.Target.AppLabel != "authors" || fresh.Field.Relation.Reverse.Name != "articles" {
		t.Fatalf("relation accessor retained alias: %#v", fresh.Field.Relation)
	}
	if loaded.Sources()[0].Producer != (Producer{Name: "first", Version: "1"}) {
		t.Fatalf("producer snapshot = %#v", loaded.Sources()[0].Producer)
	}
}

func TestScalarOnlyRelationProfileRetainsV2ProfileAndCarrier(t *testing.T) {
	t.Parallel()

	legacyDocument := lifecycleRootDocument()
	relationProfileDocument := []byte(strings.NewReplacer(
		`"loader_abi":1`, `"loader_abi":2`,
		`"operation_codec":1`, `"operation_codec":2`,
		`"schema_ir":2`, `"schema_ir":3`,
	).Replace(string(legacyDocument)))
	legacy, _, err := Load(Source{SourceID: "legacy", Document: legacyDocument})
	if err != nil {
		t.Fatalf("Load(legacy): %v", err)
	}
	relationProfile, _, err := Load(Source{SourceID: "relation-profile", Document: relationProfileDocument})
	if err != nil {
		t.Fatalf("Load(scalar relation profile): %v", err)
	}
	if relationProfile.handoff.IsZero() || relationProfile.Digest() == legacy.Digest() ||
		!reflect.DeepEqual(relationProfile.Definitions(), legacy.Definitions()) {
		t.Fatalf(
			"scalar relation profile collapsed: handoff-zero=%t digests=%q/%q definitions-equal=%t",
			relationProfile.handoff.IsZero(), relationProfile.Digest(), legacy.Digest(),
			reflect.DeepEqual(relationProfile.Definitions(), legacy.Definitions()),
		)
	}

	ctx := context.Background()
	database, err := sqlite.OpenMemory(ctx, "definition-relation-profile-scalar-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory(): %v", err)
	}
	state, migrateErr := relationProfile.Migrate(
		ctx,
		migrations.Executor{Backend: database},
		migrations.LatestLifecycleRequest(),
	)
	closeErr := database.Close()
	if migrateErr != nil {
		t.Fatalf("Set.Migrate(scalar relation profile): %v", migrateErr)
	}
	if closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}
	if _, exists := state.Model("alpha", "entry"); !exists {
		t.Fatalf("Set.Migrate(scalar relation profile) state = %#v", state)
	}
	if state.FormatVersion() != migrations.StateFormatVersion {
		t.Fatalf("scalar relation-profile state format = %d, want %d", state.FormatVersion(), migrations.StateFormatVersion)
	}

	tampered := relationProfile
	tampered.definitions = cloneMigrations(relationProfile.definitions)
	tampered.definitions[0].Name = "forged"
	backendSpy := &definitionHandoffFailureBackend{}
	_, err = tampered.Migrate(ctx, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	var capabilityError *backend.CapabilityError
	if !errors.As(err, &capabilityError) || capabilityError.Feature != "relation_migration" ||
		!strings.Contains(capabilityError.Error(), "does not match sealed loader definition") {
		t.Fatalf("Set.Migrate(tampered scalar relation profile) error = %v", err)
	}
	if backendSpy.openCalls != 0 {
		t.Fatalf("tampered scalar relation profile open calls = %d, want 0", backendSpy.openCalls)
	}

	type contextValueKey struct{}
	valueContext := context.WithValue(ctx, contextValueKey{}, "preserved")
	rawOpenError := errors.New("observe stripped carrier")
	observer := &carrierObservationBackend{openErr: rawOpenError}
	_, err = relationProfile.Migrate(
		valueContext,
		migrations.Executor{Backend: observer},
		migrations.LatestLifecycleRequest(),
	)
	if !errors.Is(err, rawOpenError) || observer.context == nil || observer.context.Value(contextValueKey{}) != "preserved" {
		t.Fatalf("scalar carrier observation = context:%#v error:%v", observer.context, err)
	}
	if _, _, found := definitionhandoff.Take(observer.context); found {
		t.Fatal("revision-fenced backend retained loader handoff context")
	}
}

func TestRelationProfileCarrierMatchesLoaderByteAndSemanticIdentifierLimits(t *testing.T) {
	relationProfileDocument := []byte(strings.NewReplacer(
		`"loader_abi":1`, `"loader_abi":2`,
		`"operation_codec":1`, `"operation_codec":2`,
		`"schema_ir":2`, `"schema_ir":3`,
	).Replace(string(lifecycleRootDocument())))

	t.Run("exact document and source ID byte maxima", func(t *testing.T) {
		build := func(defaultLength int) []byte {
			document := strings.Replace(
				string(relationProfileDocument),
				`"max_length":64`,
				`"max_length":`+strconv.Itoa(defaultLength),
				1,
			)
			document = strings.Replace(
				document,
				`"string":"untitled"`,
				`"string":"`+strings.Repeat("x", defaultLength)+`"`,
				1,
			)
			return []byte(document)
		}
		defaultLength := MaxDocumentBytes - len(relationProfileDocument) + len("untitled")
		var document []byte
		for attempt := 0; attempt < 4; attempt++ {
			document = build(defaultLength)
			adjustment := MaxDocumentBytes - len(document)
			if adjustment == 0 {
				break
			}
			defaultLength += adjustment
		}
		if len(document) != MaxDocumentBytes {
			t.Fatalf("boundary document bytes = %d, want %d", len(document), MaxDocumentBytes)
		}
		loaded, report, err := Load(Source{
			SourceID: strings.Repeat("s", MaxSourceIDBytes),
			Document: document,
		})
		if err != nil || loaded.handoff.IsZero() || report.DefinitionSetsPublished != 1 {
			t.Fatalf("Load(exact byte maxima) = handoff-zero:%t report:%+v error:%v", loaded.handoff.IsZero(), report, err)
		}
	})

	t.Run("semantic identifier longer than source ID cap", func(t *testing.T) {
		longApp := strings.Repeat("a", MaxSourceIDBytes+1)
		document := []byte(strings.ReplaceAll(string(relationProfileDocument), "alpha", longApp))
		loaded, report, err := Load(Source{SourceID: "long-semantic-app", Document: document})
		if err != nil || loaded.handoff.IsZero() || report.DefinitionSetsPublished != 1 {
			t.Fatalf("Load(long semantic identifier) = handoff-zero:%t report:%+v error:%v", loaded.handoff.IsZero(), report, err)
		}
	})
}

func TestRelationProfileDispatchAndWireRemainStrict(t *testing.T) {
	t.Parallel()

	base := string(relationDefinitionDocument("producer", "1", nil))
	tests := []struct {
		name    string
		doc     string
		code    ErrorCode
		pointer string
	}{
		{
			name: "hybrid loader", doc: strings.Replace(base, `"operation_codec":2`, `"operation_codec":1`, 1),
			code: CodeLoaderABIIncompatible, pointer: "/compatibility/loader_abi",
		},
		{
			name: "hybrid codec", doc: strings.Replace(base, `"loader_abi":2`, `"loader_abi":1`, 1),
			code: CodeOperationCodecIncompatible, pointer: "/compatibility/operation_codec",
		},
		{
			name: "unknown schema", doc: strings.Replace(base, `"schema_ir":3`, `"schema_ir":9`, 1),
			code: CodeSchemaIRIncompatible, pointer: "/compatibility/schema_ir",
		},
		{
			name: "target field is not a wire arm",
			doc:  strings.Replace(base, `"relation":{`, `"target_field":"id","relation":{`, 1),
			code: CodeInvalidIR, pointer: "/migration/operations/0/field/target_field",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			set, report, err := Load(Source{SourceID: "source", Document: []byte(test.doc)})
			var sourceError *Error
			if !errors.As(err, &sourceError) || sourceError.Code != test.code || sourceError.Context().JSONPointer != test.pointer {
				t.Fatalf("Load() error = %#v context=%+v", err, sourceError.Context())
			}
			if set.Digest() != EmptySetDigest || report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 {
				t.Fatalf("failed Load published state: digest=%q report=%+v", set.Digest(), report)
			}
		})
	}
}

func TestLoadedRelationSetValidatesCarrierThenStopsBeforeIO(t *testing.T) {
	t.Parallel()

	invalid, _, err := Load(Source{SourceID: "source", Document: relationDefinitionDocument("producer", "1", nil)})
	if err != nil {
		t.Fatalf("Load(relation): %v", err)
	}
	backendSpy := &definitionHandoffFailureBackend{}
	_, err = invalid.Migrate(context.Background(), migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	var migrationError *migrations.Error
	var capabilityError *backend.CapabilityError
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryState ||
		migrationError.Code != migrations.CodeInvalidState || migrationError.OperationIndex != 0 ||
		migrationError.Operation != "AddField" || !strings.Contains(migrationError.Cause.Error(), "exactly one historical creator") {
		t.Fatalf("Set.Migrate(invalid relation graph) error = %#v", err)
	}
	if backendSpy.openCalls != 0 {
		t.Fatalf("OpenRevisionFencedSession calls = %d, want 0", backendSpy.openCalls)
	}

	loaded := loadValidRelationLifecycleSet(t)
	_, err = loaded.Migrate(context.Background(), migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	migrationError = nil
	capabilityError = nil
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
		migrationError.Code != migrations.CodeUnsupported || !errors.As(err, &capabilityError) ||
		capabilityError.Feature != "relation_migration" || !strings.Contains(capabilityError.Error(), "validated relation state is ready") {
		t.Fatalf("Set.Migrate(valid relation graph) error = %#v capability=%#v", err, capabilityError)
	}
	if backendSpy.openCalls != 0 {
		t.Fatalf("valid relation graph opened backend %d time(s), want 0", backendSpy.openCalls)
	}

	// Definitions() is intentionally only a deep copy of the public raw
	// migration values. It must not copy the private carrier authority that is
	// available exclusively through Set.Migrate.
	rawDefinitions := loaded.Definitions()
	rawBackend := &rawRelationAuthorityBoundaryBackend{}
	_, err = (migrations.Executor{Backend: rawBackend}).Migrate(
		context.Background(), rawDefinitions, migrations.LatestLifecycleRequest(),
	)
	migrationError = nil
	capabilityError = nil
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
		migrationError.Code != migrations.CodeUnsupported || !errors.As(err, &capabilityError) ||
		capabilityError.Feature != "relation_migration" || !strings.Contains(capabilityError.Error(), "handoff is missing") {
		t.Fatalf("Executor.Migrate(Set.Definitions()) error = %#v capability=%#v", err, capabilityError)
	}
	if rawBackend.beginCalls != 0 || rawBackend.openCalls != 0 || rawBackend.readCalls != 0 {
		t.Fatalf("raw definition copy touched backend: begin=%d open=%d read=%d", rawBackend.beginCalls, rawBackend.openCalls, rawBackend.readCalls)
	}
	rawRelationIndex := relationMigrationDefinitionIndex(t, rawDefinitions)
	rawOperation := rawDefinitions[rawRelationIndex].Operations[0].(migrations.AddField)
	rawOperation.Field.Relation.Reverse.Name = "mutated"
	rawDefinitions[rawRelationIndex].Operations[0] = rawOperation
	freshDefinitions := loaded.Definitions()
	freshOperation := freshDefinitions[relationMigrationDefinitionIndex(t, freshDefinitions)].Operations[0].(migrations.AddField)
	if freshOperation.Field.Relation.Reverse.Name != "articles" {
		t.Fatalf("Set.Definitions mutation escaped into Set: %#v", freshOperation.Field.Relation)
	}
	_, err = loaded.Migrate(context.Background(), migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	capabilityError = nil
	if !errors.As(err, &capabilityError) || capabilityError.Feature != "relation_migration" ||
		!strings.Contains(capabilityError.Error(), "validated relation state is ready") {
		t.Fatalf("Set after raw-copy use lost authority = %v", err)
	}
	if backendSpy.openCalls != 0 {
		t.Fatalf("Set after raw-copy use opened backend %d time(s), want 0", backendSpy.openCalls)
	}
	staged := &stagedRelationCancellationContext{Context: context.Background(), cancelAt: 5}
	_, err = loaded.Migrate(staged, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	capabilityError = nil
	if !errors.Is(err, context.Canceled) || errors.As(err, &capabilityError) || staged.calls.Load() < staged.cancelAt {
		t.Fatalf("post-static cancellation = error:%v capability:%#v calls:%d", err, capabilityError, staged.calls.Load())
	}
	if backendSpy.openCalls != 0 {
		t.Fatalf("post-static cancellation opened backend %d time(s), want 0", backendSpy.openCalls)
	}
	postSelection := &stagedRelationCancellationContext{Context: context.Background(), cancelAt: 6}
	_, err = loaded.Migrate(postSelection, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	capabilityError = nil
	if !errors.Is(err, context.Canceled) || errors.As(err, &capabilityError) || postSelection.calls.Load() < postSelection.cancelAt {
		t.Fatalf("post-capability-selection cancellation = error:%v capability:%#v calls:%d", err, capabilityError, postSelection.calls.Load())
	}
	if backendSpy.openCalls != 0 {
		t.Fatalf("post-capability-selection cancellation opened backend %d time(s), want 0", backendSpy.openCalls)
	}

	tampered := loaded
	tampered.definitions = cloneMigrations(loaded.definitions)
	tampered.definitions[0].Name = "forged"
	_, err = tampered.Migrate(context.Background(), migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	if !errors.As(err, &capabilityError) || capabilityError.Feature != "relation_migration" ||
		!strings.Contains(capabilityError.Error(), "does not match sealed loader definition") {
		t.Fatalf("tampered carrier pairing error = %v", err)
	}
	missing := loaded
	missing.handoff = definitionhandoff.Handoff{}
	_, err = missing.Migrate(context.Background(), migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	if !errors.As(err, &capabilityError) || !strings.Contains(capabilityError.Error(), "handoff is missing") {
		t.Fatalf("missing carrier error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = loaded.Migrate(ctx, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Set.Migrate error = %v", err)
	}
	_, err = loaded.Migrate(nil, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	if err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("nil-context Set.Migrate error = %v", err)
	}
	deadlineContext, deadlineCancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer deadlineCancel()
	_, err = loaded.Migrate(deadlineContext, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired-deadline Set.Migrate error = %v", err)
	}
	_, err = loaded.Migrate(context.Background(), migrations.Executor{Backend: backendSpy}, migrations.LifecycleRequest{})
	var planningError *migrations.PlanningError
	if !errors.As(err, &planningError) || planningError.Category != migrations.CategoryPlan ||
		planningError.Code != migrations.CodeInvalidTarget {
		t.Fatalf("invalid-request Set.Migrate error = %v", err)
	}
	if backendSpy.openCalls != 0 {
		t.Fatalf("outer precedence opened backend %d time(s), want 0", backendSpy.openCalls)
	}
}

type rawRelationAuthorityBoundaryBackend struct {
	beginCalls int
	openCalls  int
	readCalls  int
}

func (value *rawRelationAuthorityBoundaryBackend) BeginMigration(context.Context) (backend.Transaction, error) {
	value.beginCalls++
	return nil, errors.New("legacy migration path must not run")
}

func (value *rawRelationAuthorityBoundaryBackend) OpenRevisionFencedSession(context.Context) (backend.RevisionFencedSession, error) {
	value.openCalls++
	return &rawRelationAuthorityBoundarySession{owner: value}, nil
}

type rawRelationAuthorityBoundarySession struct {
	owner *rawRelationAuthorityBoundaryBackend
}

func (value *rawRelationAuthorityBoundarySession) ReadAppliedMigrations(context.Context) ([]backend.AppliedMigration, error) {
	value.owner.readCalls++
	return nil, nil
}

func (*rawRelationAuthorityBoundarySession) BeginFencedMigration(context.Context, backend.HistoryTransition) (backend.RevisionFencedTransaction, error) {
	return nil, errors.New("fenced transaction must not run")
}

func (*rawRelationAuthorityBoundarySession) Close(context.Context) error { return nil }
func TestRelationSetConcurrentAccessDoesNotRetainAliases(t *testing.T) {
	loaded := loadValidRelationLifecycleSet(t)
	wantDigest := loaded.Digest()
	const workers = 32
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			definitions := loaded.Definitions()
			relationIndex := relationMigrationDefinitionIndex(t, definitions)
			operation := definitions[relationIndex].Operations[0].(migrations.AddField)
			operation.Field.Relation.Target.ModelName = "mutated"
			definitions[relationIndex].Operations[0] = operation
			_ = loaded.Sources()
			if loaded.Digest() != wantDigest {
				t.Errorf("concurrent digest = %q, want %q", loaded.Digest(), wantDigest)
			}
			_, err := loaded.Migrate(
				context.Background(),
				migrations.Executor{},
				migrations.LatestLifecycleRequest(),
			)
			var capabilityError *backend.CapabilityError
			if !errors.As(err, &capabilityError) || capabilityError.Feature != "relation_migration" {
				t.Errorf("concurrent Set.Migrate error = %v", err)
			}
		}()
	}
	group.Wait()
	freshDefinitions := loaded.Definitions()
	fresh := freshDefinitions[relationMigrationDefinitionIndex(t, freshDefinitions)].Operations[0].(migrations.AddField)
	if !reflect.DeepEqual(fresh.Field.Relation.Target, ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) {
		t.Fatalf("concurrent accessor mutation escaped: %#v", fresh.Field.Relation)
	}
}

func loadValidRelationLifecycleSet(t *testing.T) Set {
	t.Helper()
	loaded, _, err := Load(
		Source{SourceID: "authors-root", Document: relationCreatorDocument("authors", "0001_author", "author", "Author", "authors_author", nil)},
		Source{SourceID: "blog-root", Document: relationCreatorDocument(
			"blog", "0001_article", "article", "Article", "blog_article",
			[]byte(`{"app":"authors","name":"0001_author"}`),
		)},
		Source{SourceID: "blog-relation", Document: relationDefinitionDocument(
			"producer", "1", []byte(`{"app":"blog","name":"0001_article"}`),
		)},
	)
	if err != nil {
		t.Fatalf("Load(valid relation lifecycle): %v", err)
	}
	return loaded
}

func relationMigrationDefinitionIndex(t *testing.T, definitions []migrations.Migration) int {
	t.Helper()
	for index := range definitions {
		if definitions[index].Key() == (migrations.MigrationKey{App: "blog", Name: "0002_article_author"}) {
			return index
		}
	}
	t.Fatal("loaded relation definition is missing")
	return -1
}

func relationCreatorDocument(app, name, model, goName, table string, dependency []byte) []byte {
	dependencies := `[]`
	if len(dependency) != 0 {
		dependencies = `[` + string(dependency) + `]`
	}
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":2,"operation_codec":2,"schema_ir":3},` +
		`"producer":{"name":"producer","version":"1"},` +
		`"migration":{"app":"` + app + `","name":"` + name + `","dependencies":` + dependencies + `,"operations":[` +
		`{"kind":"create_model","app_label":"` + app + `","model":{` +
		`"name":"` + model + `","go_name":"` + goName + `","db_table":"` + table + `","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`)
}

func relationDefinitionDocument(producerName, producerVersion string, dependency []byte) []byte {
	dependencies := `[]`
	if len(dependency) != 0 {
		dependencies = `[` + string(dependency) + `]`
	}
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":2,"operation_codec":2,"schema_ir":3},` +
		`"producer":{"name":"` + producerName + `","version":"` + producerVersion + `"},` +
		`"migration":{"app":"blog","name":"0002_article_author","dependencies":` + dependencies + `,"operations":[` +
		`{"kind":"add_field","app_label":"blog","model_name":"article","field":{` +
		`"name":"author","go_name":"Author","column":"author_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":false,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"authors","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"articles","disabled":false},"on_delete":"protect"}}}]}}`)
}

type carrierObservationBackend struct {
	context context.Context
	openErr error
}

type stagedRelationCancellationContext struct {
	context.Context
	calls    atomic.Int32
	cancelAt int32
}

func (value *stagedRelationCancellationContext) Err() error {
	if value.calls.Add(1) >= value.cancelAt {
		return context.Canceled
	}
	return nil
}

func (*carrierObservationBackend) BeginMigration(context.Context) (backend.Transaction, error) {
	return nil, errors.New("legacy migration path must not run")
}

func (value *carrierObservationBackend) OpenRevisionFencedSession(ctx context.Context) (backend.RevisionFencedSession, error) {
	value.context = ctx
	return nil, value.openErr
}
