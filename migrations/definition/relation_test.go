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

func TestLoadedRelationSetValidatesCarrierAndEntersAuthorizedLifecycle(t *testing.T) {
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
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryTransaction ||
		migrationError.Code != migrations.CodeBeginFailed || errors.As(err, &capabilityError) {
		t.Fatalf("Set.Migrate(valid relation graph) error = %#v", err)
	}
	if backendSpy.openCalls != 1 {
		t.Fatalf("valid relation graph opened backend %d time(s), want 1", backendSpy.openCalls)
	}

	// The loaded core reaches SQLite only after exact history and whole-plan
	// validation. SQLite advertises Create/Delete relation support but rejects
	// this target-bearing AddField before any scalar prefix can begin.
	sqliteBackend, err := sqlite.OpenMemory(context.Background(), "definition-core-relation-blocker-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory(SQLite relation port): %v", err)
	}
	sqliteBoundary := &sqliteRelationCoreBlockerBackend{database: sqliteBackend}
	_, err = loaded.Migrate(context.Background(), migrations.Executor{Backend: sqliteBoundary}, migrations.LatestLifecycleRequest())
	migrationError = nil
	capabilityError = nil
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
		migrationError.Code != migrations.CodeUnsupported || !errors.As(err, &capabilityError) ||
		capabilityError.Feature != "relation_migration" {
		t.Fatalf("Set.Migrate(SQLite optional port) error = %#v capability=%#v", err, capabilityError)
	}
	if sqliteBoundary.capabilityCalls != 1 || sqliteBoundary.openCalls != 1 || sqliteBoundary.beginCalls != 0 {
		t.Fatalf("loaded core SQLite calls: capabilities=%d open=%d legacy-begin=%d", sqliteBoundary.capabilityCalls, sqliteBoundary.openCalls, sqliteBoundary.beginCalls)
	}
	if records := readDefinitionSQLiteHistory(t, sqliteBackend); len(records) != 0 {
		t.Fatalf("rejected relation AddField recorded history: %v", records)
	}
	if _, execErr := sqliteBackend.ExecContext(context.Background(), `INSERT INTO "authors_author" ("id") VALUES (1)`); execErr == nil {
		t.Fatal("rejected relation AddField committed its scalar creator prefix")
	}
	if err := sqliteBackend.Close(); err != nil {
		t.Fatalf("Close(SQLite relation port): %v", err)
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
	migrationError = nil
	capabilityError = nil
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryTransaction ||
		migrationError.Code != migrations.CodeBeginFailed || errors.As(err, &capabilityError) {
		t.Fatalf("Set after raw-copy use lost authority = %v", err)
	}
	if backendSpy.openCalls != 2 {
		t.Fatalf("Set after raw-copy use opened backend %d time(s), want 2", backendSpy.openCalls)
	}
	staged := &stagedRelationCancellationContext{Context: context.Background(), cancelAt: 5}
	_, err = loaded.Migrate(staged, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	capabilityError = nil
	if !errors.Is(err, context.Canceled) || errors.As(err, &capabilityError) || staged.calls.Load() < staged.cancelAt {
		t.Fatalf("post-static cancellation = error:%v capability:%#v calls:%d", err, capabilityError, staged.calls.Load())
	}
	if backendSpy.openCalls != 2 {
		t.Fatalf("post-static cancellation changed backend opens to %d", backendSpy.openCalls)
	}
	postSelection := &stagedRelationCancellationContext{Context: context.Background(), cancelAt: 6}
	_, err = loaded.Migrate(postSelection, migrations.Executor{Backend: backendSpy}, migrations.LatestLifecycleRequest())
	capabilityError = nil
	if !errors.Is(err, context.Canceled) || errors.As(err, &capabilityError) || postSelection.calls.Load() < postSelection.cancelAt {
		t.Fatalf("post-capability-selection cancellation = error:%v capability:%#v calls:%d", err, capabilityError, postSelection.calls.Load())
	}
	if backendSpy.openCalls != 2 {
		t.Fatalf("post-capability-selection cancellation changed backend opens to %d", backendSpy.openCalls)
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
	openCallsBeforeInvalidRequest := backendSpy.openCalls
	_, err = loaded.Migrate(context.Background(), migrations.Executor{Backend: backendSpy}, migrations.LifecycleRequest{})
	var planningError *migrations.PlanningError
	if !errors.As(err, &planningError) || planningError.Category != migrations.CategoryPlan ||
		planningError.Code != migrations.CodeInvalidTarget {
		t.Fatalf("invalid-request Set.Migrate error = %v", err)
	}
	if backendSpy.openCalls != openCallsBeforeInvalidRequest {
		t.Fatalf("outer precedence changed backend opens from %d to %d", openCallsBeforeInvalidRequest, backendSpy.openCalls)
	}
}

func TestLoadedRelationCreateMigratesThroughSQLiteApplyUnapplyReapplyAndRejectsRemove(t *testing.T) {
	ctx := context.Background()
	loaded, _, err := Load(
		Source{SourceID: "authors-create", Document: relationCreatorDocument("authors", "0001_author", "author", "Author", "authors_author", nil)},
		Source{SourceID: "blog-relation-create", Document: relationCreateModelDocument()},
	)
	if err != nil {
		t.Fatalf("Load(relation CreateModel lifecycle): %v", err)
	}
	database, err := sqlite.OpenMemory(ctx, "definition-relation-create-lifecycle-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory(): %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Errorf("Close(): %v", closeErr)
		}
	}()
	executor := migrations.Executor{Backend: database}

	state, err := loaded.Migrate(ctx, executor, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Set.Migrate(relation CreateModel apply): %v", err)
	}
	if state.FormatVersion() != migrations.RelationStateFormatVersion {
		t.Fatalf("applied state format = %d, want relation format", state.FormatVersion())
	}
	if _, exists := state.Model("authors", "author"); !exists {
		t.Fatal("applied state is missing authors.author")
	}
	if post, exists := state.Model("blog", "article"); !exists || len(post.Fields) != 2 || post.Fields[1].Relation == nil {
		t.Fatalf("applied relation model = %#v, exists=%t", post, exists)
	}
	assertDefinitionSQLiteHistory(t, database,
		backend.AppliedMigration{App: "authors", Name: "0001_author"},
		backend.AppliedMigration{App: "blog", Name: "0001_article"},
	)
	if _, err := database.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (1)`); err != nil {
		t.Fatalf("insert relation target: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id") VALUES (1, 1)`); err != nil {
		t.Fatalf("insert valid relation source: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id") VALUES (2, 999)`); err == nil {
		t.Fatal("SQLite accepted an invalid loaded relation foreign key")
	}

	state, err = loaded.Migrate(ctx, executor, migrations.TargetedLifecycleRequest(migrations.ZeroTarget("authors")))
	if err != nil {
		t.Fatalf("Set.Migrate(relation DeleteModel unapply): %v", err)
	}
	if len(state.Apps()) != 0 {
		t.Fatalf("unapplied state apps = %v, want empty", state.Apps())
	}
	assertDefinitionSQLiteHistory(t, database)
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id") VALUES (1, 1)`); err == nil {
		t.Fatal("relation source table survived DeleteModel unapply")
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (1)`); err == nil {
		t.Fatal("relation target table survived dependency unapply")
	}

	state, err = loaded.Migrate(ctx, executor, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Set.Migrate(relation CreateModel reapply): %v", err)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("reapplied state is missing blog.article")
	}
	assertDefinitionSQLiteHistory(t, database,
		backend.AppliedMigration{App: "authors", Name: "0001_author"},
		backend.AppliedMigration{App: "blog", Name: "0001_article"},
	)
	if _, err := database.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (1)`); err != nil {
		t.Fatalf("insert relation target after reapply: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id") VALUES (1, 1)`); err != nil {
		t.Fatalf("insert valid relation source after reapply: %v", err)
	}

	// Seed only the additional recorder transition on top of the physically
	// equivalent relation CreateModel state. The AddField definition set then
	// reconstructs an applied target-bearing Add and must reject its reverse
	// Remove capability before changing revision, schema, or rows.
	seedDefinitionSQLiteHistoryTransition(t, database, backend.HistoryTransition{
		Migration: backend.AppliedMigration{App: "blog", Name: "0002_article_author"},
		Kind:      backend.HistoryTransitionApply,
	})
	addSet := loadValidRelationLifecycleSet(t)
	beforeRecords := readDefinitionSQLiteHistory(t, database)
	state, err = addSet.Migrate(ctx, executor, migrations.TargetedLifecycleRequest(migrations.ZeroTarget("authors")))
	var migrationError *migrations.Error
	var capabilityError *backend.CapabilityError
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
		migrationError.Code != migrations.CodeUnsupported || !errors.As(err, &capabilityError) ||
		!strings.Contains(capabilityError.Detail, "RemoveForeignKeyByTableRemake") {
		t.Fatalf("Set.Migrate(relation RemoveField capability) = %#v capability=%#v", err, capabilityError)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("rejected relation RemoveField did not return reconstructed durable state")
	}
	afterRecords := readDefinitionSQLiteHistory(t, database)
	if !reflect.DeepEqual(afterRecords, beforeRecords) {
		t.Fatalf("rejected relation RemoveField changed history: before=%v after=%v", beforeRecords, afterRecords)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id") VALUES (2, 1)`); err != nil {
		t.Fatalf("rejected relation RemoveField changed physical schema or rows: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id") VALUES (3, 999)`); err == nil {
		t.Fatal("rejected relation RemoveField disabled the physical foreign key")
	}
}

func readDefinitionSQLiteHistory(t *testing.T, database *sqlite.Backend) []backend.AppliedMigration {
	t.Helper()
	ctx := context.Background()
	session, err := database.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(): %v", err)
	}
	records, readErr := session.ReadAppliedMigrations(ctx)
	closeErr := session.Close(ctx)
	if readErr != nil {
		t.Fatalf("ReadAppliedMigrations(): %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("Close revision session: %v", closeErr)
	}
	return records
}

func assertDefinitionSQLiteHistory(t *testing.T, database *sqlite.Backend, want ...backend.AppliedMigration) {
	t.Helper()
	got := readDefinitionSQLiteHistory(t, database)
	if len(got) != len(want) {
		t.Fatalf("SQLite migration history = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("SQLite migration history = %v, want %v", got, want)
		}
	}
}

func seedDefinitionSQLiteHistoryTransition(
	t *testing.T,
	database *sqlite.Backend,
	transition backend.HistoryTransition,
) {
	t.Helper()
	ctx := context.Background()
	session, err := database.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(seed): %v", err)
	}
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatalf("ReadAppliedMigrations(seed): %v", err)
	}
	transaction, err := session.BeginFencedMigration(ctx, transition)
	if err != nil {
		t.Fatalf("BeginFencedMigration(seed): %v", err)
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err != nil {
		t.Fatalf("RecordApplied(seed): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != backend.CommitCommitted {
		t.Fatalf("CommitFenced(seed) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(seed): %v", err)
	}
}

type rawRelationAuthorityBoundaryBackend struct {
	beginCalls int
	openCalls  int
	readCalls  int
}

type sqliteRelationCoreBlockerBackend struct {
	database        *sqlite.Backend
	beginCalls      int
	openCalls       int
	capabilityCalls int
}

func (value *sqliteRelationCoreBlockerBackend) BeginMigration(ctx context.Context) (backend.Transaction, error) {
	value.beginCalls++
	return value.database.BeginMigration(ctx)
}

func (value *sqliteRelationCoreBlockerBackend) OpenRevisionFencedSession(ctx context.Context) (backend.RevisionFencedSession, error) {
	value.openCalls++
	return value.database.OpenRevisionFencedSession(ctx)
}

func (value *sqliteRelationCoreBlockerBackend) RelationMigrationCapabilities() backend.RelationMigrationCapabilities {
	value.capabilityCalls++
	return value.database.RelationMigrationCapabilities()
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
			var migrationError *migrations.Error
			if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
				migrationError.Code != migrations.CodeRevisionFenceUnsupported {
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

func relationCreateModelDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":2,"operation_codec":2,"schema_ir":3},` +
		`"producer":{"name":"producer","version":"1"},` +
		`"migration":{"app":"blog","name":"0001_article","dependencies":[{"app":"authors","name":"0001_author"}],"operations":[` +
		`{"kind":"create_model","app_label":"blog","model":{` +
		`"name":"article","go_name":"Article","db_table":"blog_article","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null},` +
		`{"name":"author","go_name":"Author","column":"author_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":false,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"authors","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"articles","disabled":false},"on_delete":"protect"}}]}}]}}`)
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
