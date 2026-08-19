package definition

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"path/filepath"
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
	// validation. The required relation Add is authorized because the source
	// created by the preceding migration remains physically empty under the
	// pinned transaction preflight.
	sqliteBackend, err := sqlite.OpenMemory(context.Background(), "definition-core-relation-blocker-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory(SQLite relation port): %v", err)
	}
	sqliteBoundary := &sqliteRelationCoreBlockerBackend{database: sqliteBackend}
	state, err := loaded.Migrate(context.Background(), migrations.Executor{Backend: sqliteBoundary}, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Set.Migrate(SQLite required relation port): %v", err)
	}
	article, exists := state.Model("blog", "article")
	if !exists || len(article.Fields) != 2 || article.Fields[1].Name != "author" ||
		article.Fields[1].Nullable || article.Fields[1].Relation == nil {
		t.Fatalf("Set.Migrate(SQLite required relation state) = %#v/%t", article, exists)
	}
	if sqliteBoundary.capabilityCalls != 1 || sqliteBoundary.openCalls != 1 || sqliteBoundary.beginCalls != 0 {
		t.Fatalf("loaded core SQLite calls: capabilities=%d open=%d legacy-begin=%d", sqliteBoundary.capabilityCalls, sqliteBoundary.openCalls, sqliteBoundary.beginCalls)
	}
	if records := readDefinitionSQLiteHistory(t, sqliteBackend); len(records) != 3 {
		t.Fatalf("required relation lifecycle history: %v", records)
	}
	if _, execErr := sqliteBackend.ExecContext(context.Background(), `INSERT INTO "authors_author" ("id") VALUES (1)`); execErr != nil {
		t.Fatalf("insert required relation target: %v", execErr)
	}
	if _, execErr := sqliteBackend.ExecContext(context.Background(), `INSERT INTO "blog_article" ("id", "author_id") VALUES (1, 1)`); execErr != nil {
		t.Fatalf("insert valid required relation row: %v", execErr)
	}
	if _, execErr := sqliteBackend.ExecContext(context.Background(), `INSERT INTO "blog_article" ("id", "author_id") VALUES (2, NULL)`); execErr == nil {
		t.Fatal("required relation port accepted NULL")
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
	path := filepath.Join(t.TempDir(), "loaded-relation-restart.sqlite")
	fullHistory := []backend.AppliedMigration{
		{App: "authors", Name: "0001_author"},
		{App: "blog", Name: "0001_article"},
		{App: "blog", Name: "0002_article_title"},
	}
	branchHistory := []backend.AppliedMigration{{App: "authors", Name: "0001_author"}}
	seededHistory := append(append([]backend.AppliedMigration(nil), fullHistory...), backend.AppliedMigration{
		App: "blog", Name: "0003_article_reviewer",
	})

	// The initial process owns both the backend and loaded Set only inside this
	// scope. Later phases reopen the file and decode fresh source bytes in a
	// different order; no ProjectState or private handoff crosses the boundary.
	initialSnapshot, setDigest := func() (definitionSQLiteRestartSnapshot, string) {
		database := openDefinitionSQLiteRestartBackend(t, path)
		defer closeDefinitionSQLiteRestartBackend(t, database)
		loaded := loadDefinitionSQLiteRestartSet(t, "authors", "blog", "tail")
		if loaded.handoff.IsZero() {
			t.Fatal("mixed legacy/relation restart set has no loader handoff")
		}
		state, err := loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
		if err != nil {
			t.Fatalf("initial file-backed Latest: %v", err)
		}
		assertDefinitionSQLiteRestartRelationState(t, state, false)
		insertDefinitionSQLiteRestartRows(t, database, true)
		assertDefinitionSQLiteRestartForeignKeyEnforcement(t, database)
		snapshot := readDefinitionSQLiteRestartSnapshot(t, path)
		assertDefinitionSQLiteRestartToken(t, snapshot, 3, "e2dfbdf7719c41466f78cef67396a2961a71745c85d2673a47dc3cbdfaa83507", fullHistory)
		assertDefinitionSQLiteRestartForeignKeys(t, snapshot)
		return snapshot, loaded.Digest()
	}()

	// First restart: the sources are re-created and permuted. Latest must be a
	// byte-for-byte recorder/schema/row/FK no-op before a branch target removes
	// the scalar tail then its relation-bearing blog table. Parent-first table
	// deletion would make the tail's reverse RemoveField fail, so success also
	// proves child-first DAG execution.
	branchSnapshot := func() definitionSQLiteRestartSnapshot {
		database := openDefinitionSQLiteRestartBackend(t, path)
		defer closeDefinitionSQLiteRestartBackend(t, database)
		loaded := loadDefinitionSQLiteRestartSet(t, "tail", "authors", "blog")
		if loaded.Digest() != setDigest {
			t.Fatalf("first restart digest = %q, want %q", loaded.Digest(), setDigest)
		}
		noOpState, err := loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
		if err != nil {
			t.Fatalf("first restart Latest no-op: %v", err)
		}
		assertDefinitionSQLiteRestartRelationState(t, noOpState, false)
		noOpSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
		if !reflect.DeepEqual(noOpSnapshot, initialSnapshot) {
			t.Fatalf("reopened Latest changed durable snapshot:\ninitial=%+v\nreopened=%+v", initialSnapshot, noOpSnapshot)
		}

		targetState, err := loaded.Migrate(
			ctx,
			migrations.Executor{Backend: database},
			migrations.TargetedLifecycleRequest(migrations.ZeroTarget("blog")),
		)
		if err != nil {
			t.Fatalf("first restart target blog zero: %v", err)
		}
		if targetState.FormatVersion() != migrations.StateFormatVersion ||
			!reflect.DeepEqual(targetState.Apps(), []string{"authors"}) {
			t.Fatalf("target branch state = format:%d apps:%v", targetState.FormatVersion(), targetState.Apps())
		}
		if _, exists := targetState.Model("authors", "author"); !exists {
			t.Fatal("target branch state lost the legacy authors root")
		}
		snapshot := readDefinitionSQLiteRestartSnapshot(t, path)
		assertDefinitionSQLiteRestartToken(t, snapshot, 5, "7f42d0b7c454db7954a6767a518a34f0db777a80a1dec0e5578bd403ef5e9b9c", branchHistory)
		if snapshot.Epoch != initialSnapshot.Epoch {
			t.Fatalf("target branch changed epoch: initial=%x target=%x", initialSnapshot.Epoch, snapshot.Epoch)
		}
		if len(snapshot.ForeignKeys) != 0 {
			t.Fatalf("target branch retained physical FKs: %+v", snapshot.ForeignKeys)
		}
		wantRows := []definitionSQLiteRestartRow{{Table: "authors_author", ID: 41}}
		if !reflect.DeepEqual(snapshot.Rows, wantRows) {
			t.Fatalf("target branch rows = %+v, want %+v", snapshot.Rows, wantRows)
		}
		return snapshot
	}()

	// Second restart: use a third source order, rebuild the actual plan from
	// recorder history, and return to the same full history/fingerprint and
	// physical bytes with a strictly newer revision in the same epoch (ABA).
	func() {
		database := openDefinitionSQLiteRestartBackend(t, path)
		defer closeDefinitionSQLiteRestartBackend(t, database)
		loaded := loadDefinitionSQLiteRestartSet(t, "blog", "tail", "authors")
		if loaded.Digest() != setDigest {
			t.Fatalf("second restart digest = %q, want %q", loaded.Digest(), setDigest)
		}
		reappliedState, err := loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
		if err != nil {
			t.Fatalf("second restart Latest reapply: %v", err)
		}
		assertDefinitionSQLiteRestartRelationState(t, reappliedState, false)
		insertDefinitionSQLiteRestartRows(t, database, false)
		assertDefinitionSQLiteRestartForeignKeyEnforcement(t, database)
		reappliedSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
		assertDefinitionSQLiteRestartToken(t, reappliedSnapshot, 7, "e2dfbdf7719c41466f78cef67396a2961a71745c85d2673a47dc3cbdfaa83507", fullHistory)
		assertDefinitionSQLiteRestartForeignKeys(t, reappliedSnapshot)
		if reappliedSnapshot.Epoch != initialSnapshot.Epoch ||
			reappliedSnapshot.Revision <= initialSnapshot.Revision ||
			reappliedSnapshot.Fingerprint != initialSnapshot.Fingerprint {
			t.Fatalf(
				"reapply ABA token = epoch:%x revision:%d fingerprint:%x, initial epoch:%x revision:%d fingerprint:%x",
				reappliedSnapshot.Epoch,
				reappliedSnapshot.Revision,
				reappliedSnapshot.Fingerprint,
				initialSnapshot.Epoch,
				initialSnapshot.Revision,
				initialSnapshot.Fingerprint,
			)
		}
		if branchSnapshot.Revision >= reappliedSnapshot.Revision || branchSnapshot.Epoch != reappliedSnapshot.Epoch {
			t.Fatalf("reapply did not advance branch token in one epoch: branch=%+v reapply=%+v", branchSnapshot, reappliedSnapshot)
		}
		if !reflect.DeepEqual(reappliedSnapshot.Schema, initialSnapshot.Schema) ||
			!reflect.DeepEqual(reappliedSnapshot.Rows, initialSnapshot.Rows) ||
			!reflect.DeepEqual(reappliedSnapshot.ForeignKeys, initialSnapshot.ForeignKeys) {
			t.Fatalf("reapply physical ABA mismatch:\ninitial=%+v\nreapplied=%+v", initialSnapshot, reappliedSnapshot)
		}

		// A forward required relation Add is supported only for an empty source.
		// This populated source must fail during the pinned physical preflight and
		// leave every durable byte observed by the read-only snapshot unchanged.
		beforeAdd := readDefinitionSQLiteRestartSnapshot(t, path)
		func() {
			addSet := loadDefinitionSQLiteRestartSet(t, "reviewer", "tail", "authors", "blog")
			addState, addErr := addSet.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
			assertDefinitionSQLiteRestartCapabilityError(
				t,
				addErr,
				migrations.DirectionForward,
				"sqlite_relation_migration",
				"contains rows",
			)
			assertDefinitionSQLiteRestartRelationState(t, addState, false)
		}()
		afterAdd := readDefinitionSQLiteRestartSnapshot(t, path)
		if !reflect.DeepEqual(afterAdd, beforeAdd) {
			t.Fatalf("unsupported relation Add changed snapshot:\nbefore=%+v\nafter=%+v", beforeAdd, afterAdd)
		}

		// Seed only the recorder transition. This deliberately advances the
		// token while leaving schema, rows, and existing FKs untouched, so a
		// freshly loaded set reconstructs the applied relation Add and must reject
		// its reverse Remove during physical preflight because the declared Before
		// shape was never actually installed.
		seedDefinitionSQLiteHistoryTransition(t, database, backend.HistoryTransition{
			Migration: backend.AppliedMigration{App: "blog", Name: "0003_article_reviewer"},
			Kind:      backend.HistoryTransitionApply,
		})
		seededSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
		assertDefinitionSQLiteRestartToken(t, seededSnapshot, 8, "e9b4004ca1d944d26393a59e4745c56308a281660341b3514e66b7461148049e", seededHistory)
		if seededSnapshot.Epoch != beforeAdd.Epoch || seededSnapshot.Revision != beforeAdd.Revision+1 ||
			!reflect.DeepEqual(seededSnapshot.Schema, beforeAdd.Schema) ||
			!reflect.DeepEqual(seededSnapshot.Rows, beforeAdd.Rows) ||
			!reflect.DeepEqual(seededSnapshot.ForeignKeys, beforeAdd.ForeignKeys) {
			t.Fatalf("recorder-only seed changed physical snapshot or wrong token:\nbefore=%+v\nseeded=%+v", beforeAdd, seededSnapshot)
		}

		func() {
			removeSet := loadDefinitionSQLiteRestartSet(t, "blog", "reviewer", "authors", "tail")
			removeState, removeErr := removeSet.Migrate(
				ctx,
				migrations.Executor{Backend: database},
				migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
					App: "blog", Name: "0002_article_title",
				})),
			)
			assertDefinitionSQLiteRestartCapabilityError(
				t,
				removeErr,
				migrations.DirectionBackward,
				"sqlite_relation_migration",
				"has 3 columns, want 4",
			)
			assertDefinitionSQLiteRestartRelationState(t, removeState, true)
		}()
		afterRemove := readDefinitionSQLiteRestartSnapshot(t, path)
		if !reflect.DeepEqual(afterRemove, seededSnapshot) {
			t.Fatalf("unsupported relation Remove changed snapshot:\nbefore=%+v\nafter=%+v", seededSnapshot, afterRemove)
		}
		assertDefinitionSQLiteRestartForeignKeyEnforcement(t, database)
	}()
}

func TestLoadedNullableRelationAddRemovesByRemakeAndReappliesAfterFileReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loaded-nullable-relation-add.sqlite")
	baseHistory := []backend.AppliedMigration{
		{App: "authors", Name: "0001_author"},
		{App: "blog", Name: "0001_article"},
		{App: "blog", Name: "0002_article_title"},
	}
	addHistory := append(append([]backend.AppliedMigration(nil), baseHistory...), backend.AppliedMigration{
		App: "blog", Name: "0003_article_reviewer",
	})

	database := openDefinitionSQLiteRestartBackend(t, path)
	base := loadDefinitionSQLiteRestartSet(t, "authors", "blog", "tail")
	state, err := base.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(nullable Add base): %v", err)
	}
	assertDefinitionSQLiteRestartRelationState(t, state, false)
	insertDefinitionSQLiteRestartRows(t, database, true)
	baseSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
	if !reflect.DeepEqual(baseSnapshot.History, baseHistory) {
		t.Fatalf("nullable Add base history = %v", baseSnapshot.History)
	}
	sequenceBefore := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`)
	authorSequenceBefore := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='authors_author'`)

	loaded := loadDefinitionSQLiteRestartSet(t, "nullable-reviewer", "tail", "authors", "blog")
	state, err = loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(nullable relation Add): %v", err)
	}
	assertDefinitionSQLiteRestartRelationState(t, state, true)
	articleState, exists := state.Model("blog", "article")
	if !exists || len(articleState.Fields) != 4 || articleState.Fields[3].Name != "reviewer" ||
		!articleState.Fields[3].Nullable || articleState.Fields[3].Default != nil {
		t.Fatalf("nullable reviewer historical state = %#v/%t", articleState, exists)
	}
	addSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
	if addSnapshot.Revision != baseSnapshot.Revision+1 || addSnapshot.Epoch != baseSnapshot.Epoch ||
		!reflect.DeepEqual(addSnapshot.History, addHistory) ||
		!reflect.DeepEqual(addSnapshot.Rows, baseSnapshot.Rows) {
		t.Fatalf("nullable Add durable transition: base=%+v add=%+v", baseSnapshot, addSnapshot)
	}
	if sequenceAfter := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`); sequenceAfter != sequenceBefore {
		t.Fatalf("nullable Add sequence = %d, want %d", sequenceAfter, sequenceBefore)
	}
	if authorSequenceAfter := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='authors_author'`); authorSequenceAfter != authorSequenceBefore {
		t.Fatalf("nullable Add author sequence = %d, want %d", authorSequenceAfter, authorSequenceBefore)
	}
	if reviewer := readDefinitionSQLiteNullableInteger(t, path, `SELECT "reviewer_id" FROM "blog_article" WHERE "id"=51`); reviewer.Valid {
		t.Fatalf("existing row reviewer after nullable Add = %v, want NULL", reviewer)
	}
	foreignKeys := make(map[string]definitionSQLiteRestartForeignKey, len(addSnapshot.ForeignKeys))
	for _, foreignKey := range addSnapshot.ForeignKeys {
		foreignKeys[foreignKey.FromColumn] = foreignKey
	}
	if len(foreignKeys) != 2 {
		t.Fatalf("nullable Add physical foreign keys = %+v", addSnapshot.ForeignKeys)
	}
	for _, column := range []string{"author_id", "reviewer_id"} {
		foreignKey, exists := foreignKeys[column]
		if !exists || foreignKey.SourceTable != "blog_article" || foreignKey.TargetTable != "authors_author" ||
			foreignKey.ToColumn != "id" || foreignKey.Sequence != 0 || foreignKey.OnUpdate != "NO ACTION" ||
			foreignKey.OnDelete != "NO ACTION" || foreignKey.Match != "NONE" {
			t.Fatalf("nullable Add foreign key %q = %+v", column, foreignKey)
		}
	}
	var articleSQL string
	for _, object := range addSnapshot.Schema {
		if object.Type == "table" && object.Name == "blog_article" {
			articleSQL = object.Definition
		}
	}
	wantArticleSQL := `CREATE TABLE "blog_article" (` +
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
		`"author_id" INTEGER NOT NULL, ` +
		`"title" VARCHAR(64) NOT NULL, ` +
		`"reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id") ON DELETE NO ACTION, ` +
		`FOREIGN KEY ("author_id") REFERENCES "authors_author" ("id") ON DELETE NO ACTION)`
	if articleSQL != wantArticleSQL {
		t.Fatalf("nullable Add canonical mixed CREATE SQL = %q, want %q", articleSQL, wantArticleSQL)
	}
	if _, err := database.ExecContext(ctx, `UPDATE "blog_article" SET "reviewer_id"=41 WHERE "id"=51`); err != nil {
		t.Fatalf("set valid loaded reviewer: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE "blog_article" SET "reviewer_id"=9999 WHERE "id"=51`); err == nil {
		t.Fatal("loaded nullable Add accepted orphan reviewer")
	}
	updatedSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
	updatedReviewer := readDefinitionSQLiteNullableInteger(t, path, `SELECT "reviewer_id" FROM "blog_article" WHERE "id"=51`)
	updatedAuthorSequence := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='authors_author'`)
	updatedArticleSequence := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`)
	if !updatedReviewer.Valid || updatedReviewer.Int64 != 41 ||
		updatedAuthorSequence != authorSequenceBefore || updatedArticleSequence != sequenceBefore ||
		!reflect.DeepEqual(updatedSnapshot, addSnapshot) {
		t.Fatalf("updated nullable Add values = reviewer:%v author-seq:%d article-seq:%d", updatedReviewer, updatedAuthorSequence, updatedArticleSequence)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close(nullable Add process): %v", err)
	}

	// Fresh backend + freshly decoded sources must treat Latest as an exact
	// no-op, then perform reverse Remove through the bounded remake path.
	database = openDefinitionSQLiteRestartBackend(t, path)
	defer closeDefinitionSQLiteRestartBackend(t, database)
	loaded = loadDefinitionSQLiteRestartSet(t, "blog", "nullable-reviewer", "authors", "tail")
	noOpState, err := loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("reopened nullable Add Latest: %v", err)
	}
	assertDefinitionSQLiteRestartRelationState(t, noOpState, true)
	beforeRemove := readDefinitionSQLiteRestartSnapshot(t, path)
	if !reflect.DeepEqual(beforeRemove, updatedSnapshot) ||
		readDefinitionSQLiteNullableInteger(t, path, `SELECT "reviewer_id" FROM "blog_article" WHERE "id"=51`) != updatedReviewer ||
		readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='authors_author'`) != updatedAuthorSequence ||
		readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`) != updatedArticleSequence {
		t.Fatalf("reopened Latest changed nullable Add durable state:\nupdated=%+v\nreopened=%+v", updatedSnapshot, beforeRemove)
	}
	removeState, removeErr := loaded.Migrate(
		ctx,
		migrations.Executor{Backend: database},
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
			App: "blog", Name: "0002_article_title",
		})),
	)
	if removeErr != nil {
		t.Fatalf("reverse nullable Remove by remake: %v", removeErr)
	}
	assertDefinitionSQLiteRestartRelationState(t, removeState, false)
	afterRemove := readDefinitionSQLiteRestartSnapshot(t, path)
	if afterRemove.Revision != beforeRemove.Revision+1 || afterRemove.Epoch != beforeRemove.Epoch ||
		!reflect.DeepEqual(afterRemove.History, baseHistory) ||
		!reflect.DeepEqual(afterRemove.Rows, beforeRemove.Rows) || len(afterRemove.ForeignKeys) != 1 ||
		afterRemove.ForeignKeys[0].FromColumn != "author_id" {
		t.Fatalf("reverse nullable Remove durable transition:\nbefore=%+v\nafter=%+v", beforeRemove, afterRemove)
	}
	if removed := readDefinitionSQLiteInteger(t, path, `SELECT COUNT(*) FROM pragma_table_xinfo('blog_article') WHERE "name"='reviewer_id'`); removed != 0 {
		t.Fatalf("reverse nullable Remove retained reviewer column count=%d", removed)
	}
	if authorSequence := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='authors_author'`); authorSequence != updatedAuthorSequence {
		t.Fatalf("reverse Remove changed author sequence = %d, want %d", authorSequence, updatedAuthorSequence)
	}
	if articleSequence := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`); articleSequence != updatedArticleSequence {
		t.Fatalf("reverse Remove changed article sequence = %d, want %d", articleSequence, updatedArticleSequence)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close(nullable Remove process): %v", err)
	}
	database = openDefinitionSQLiteRestartBackend(t, path)
	defer closeDefinitionSQLiteRestartBackend(t, database)
	loaded = loadDefinitionSQLiteRestartSet(t, "tail", "authors", "blog", "nullable-reviewer")
	reappliedState, reapplyErr := loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	if reapplyErr != nil {
		t.Fatalf("reapply nullable relation after reopen: %v", reapplyErr)
	}
	assertDefinitionSQLiteRestartRelationState(t, reappliedState, true)
	reapplied := readDefinitionSQLiteRestartSnapshot(t, path)
	if reapplied.Revision != afterRemove.Revision+1 || reapplied.Epoch != afterRemove.Epoch ||
		!reflect.DeepEqual(reapplied.History, addHistory) || !reflect.DeepEqual(reapplied.Rows, afterRemove.Rows) ||
		readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`) != updatedArticleSequence ||
		readDefinitionSQLiteNullableInteger(t, path, `SELECT "reviewer_id" FROM "blog_article" WHERE "id"=51`).Valid {
		t.Fatalf("nullable relation reapply durable transition:\nremoved=%+v\nreapplied=%+v", afterRemove, reapplied)
	}
}

func TestLoadedRequiredRelationAddMigratesOnlyOnEmptySQLiteSourceAndReopensAsNoOp(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loaded-required-relation-add.sqlite")
	baseHistory := []backend.AppliedMigration{
		{App: "authors", Name: "0001_author"},
		{App: "blog", Name: "0001_article"},
		{App: "blog", Name: "0002_article_title"},
	}
	addHistory := append(append([]backend.AppliedMigration(nil), baseHistory...), backend.AppliedMigration{
		App: "blog", Name: "0003_article_reviewer",
	})

	database := openDefinitionSQLiteRestartBackend(t, path)
	base := loadDefinitionSQLiteRestartSet(t, "authors", "blog", "tail")
	state, err := base.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(required Add base): %v", err)
	}
	assertDefinitionSQLiteRestartRelationState(t, state, false)
	// The target may be populated; only the source table must be empty at the
	// pinned BEGIN IMMEDIATE preflight.
	if _, err := database.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (41)`); err != nil {
		t.Fatalf("insert required Add target row: %v", err)
	}
	baseSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
	authorSequence := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='authors_author'`)

	loaded := loadDefinitionSQLiteRestartSet(t, "reviewer", "tail", "authors", "blog")
	state, err = loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(required relation Add): %v", err)
	}
	assertDefinitionSQLiteRestartRelationState(t, state, true)
	article, exists := state.Model("blog", "article")
	if !exists || len(article.Fields) != 4 || article.Fields[3].Name != "reviewer" ||
		article.Fields[3].Nullable || article.Fields[3].Default != nil ||
		article.Fields[3].Relation == nil || article.Fields[3].Relation.OnDelete != ir.DeleteProtect {
		t.Fatalf("required reviewer historical state = %#v/%t", article, exists)
	}
	addSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
	if addSnapshot.Revision != baseSnapshot.Revision+1 || addSnapshot.Epoch != baseSnapshot.Epoch ||
		!reflect.DeepEqual(addSnapshot.History, addHistory) ||
		!reflect.DeepEqual(addSnapshot.Rows, baseSnapshot.Rows) ||
		readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='authors_author'`) != authorSequence {
		t.Fatalf("required Add durable transition: base=%+v add=%+v", baseSnapshot, addSnapshot)
	}
	var articleSQL string
	for _, object := range addSnapshot.Schema {
		if object.Type == "table" && object.Name == "blog_article" {
			articleSQL = object.Definition
		}
	}
	wantArticleSQL := `CREATE TABLE "blog_article" (` +
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
		`"author_id" INTEGER NOT NULL, ` +
		`"title" VARCHAR(64) NOT NULL, ` +
		`"reviewer_id" INTEGER NOT NULL REFERENCES "authors_author" ("id") ON DELETE NO ACTION, ` +
		`FOREIGN KEY ("author_id") REFERENCES "authors_author" ("id") ON DELETE NO ACTION)`
	if articleSQL != wantArticleSQL ||
		readDefinitionSQLiteInteger(t, path, `SELECT "notnull" FROM pragma_table_xinfo('blog_article') WHERE "name"='reviewer_id'`) != 1 {
		t.Fatalf("required Add canonical SQL/notnull = %q", articleSQL)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "blog_article" ("id", "author_id", "title", "reviewer_id") VALUES (51, 41, 'valid', 41)`,
	); err != nil {
		t.Fatalf("insert valid required reviewer: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "blog_article" ("id", "author_id", "title", "reviewer_id") VALUES (52, 41, 'null', NULL)`,
	); err == nil || !strings.Contains(strings.ToLower(err.Error()), "not null") {
		t.Fatalf("required reviewer NULL error = %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "blog_article" ("id", "author_id", "title", "reviewer_id") VALUES (53, 41, 'orphan', 9999)`,
	); err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("required reviewer orphan error = %v", err)
	}
	committedSnapshot := readDefinitionSQLiteRestartSnapshot(t, path)
	if err := database.Close(); err != nil {
		t.Fatalf("Close(required Add process): %v", err)
	}

	database = openDefinitionSQLiteRestartBackend(t, path)
	defer closeDefinitionSQLiteRestartBackend(t, database)
	loaded = loadDefinitionSQLiteRestartSet(t, "blog", "reviewer", "authors", "tail")
	noOpState, err := loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("reopened required Add Latest: %v", err)
	}
	assertDefinitionSQLiteRestartRelationState(t, noOpState, true)
	beforeRemove := readDefinitionSQLiteRestartSnapshot(t, path)
	if !reflect.DeepEqual(beforeRemove, committedSnapshot) {
		t.Fatalf("reopened Latest changed required Add state:\ncommitted=%+v\nreopened=%+v", committedSnapshot, beforeRemove)
	}
	removeState, removeErr := loaded.Migrate(
		ctx,
		migrations.Executor{Backend: database},
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
			App: "blog", Name: "0002_article_title",
		})),
	)
	if removeErr != nil {
		t.Fatalf("reverse required Remove by remake: %v", removeErr)
	}
	assertDefinitionSQLiteRestartRelationState(t, removeState, false)
	afterRemove := readDefinitionSQLiteRestartSnapshot(t, path)
	articleSequence := readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`)
	if afterRemove.Revision != beforeRemove.Revision+1 || afterRemove.Epoch != beforeRemove.Epoch ||
		!reflect.DeepEqual(afterRemove.History, baseHistory) || !reflect.DeepEqual(afterRemove.Rows, beforeRemove.Rows) ||
		len(afterRemove.ForeignKeys) != 1 || afterRemove.ForeignKeys[0].FromColumn != "author_id" ||
		readDefinitionSQLiteInteger(t, path, `SELECT COUNT(*) FROM pragma_table_xinfo('blog_article') WHERE "name"='reviewer_id'`) != 0 {
		t.Fatalf("reverse required Remove durable transition:\nbefore=%+v\nafter=%+v", beforeRemove, afterRemove)
	}
	reapplyState, reapplyErr := loaded.Migrate(ctx, migrations.Executor{Backend: database}, migrations.LatestLifecycleRequest())
	assertDefinitionSQLiteRestartCapabilityError(
		t, reapplyErr, migrations.DirectionForward, "sqlite_relation_migration", "contains rows",
	)
	assertDefinitionSQLiteRestartRelationState(t, reapplyState, false)
	if afterRejectedReapply := readDefinitionSQLiteRestartSnapshot(t, path); !reflect.DeepEqual(afterRejectedReapply, afterRemove) ||
		readDefinitionSQLiteInteger(t, path, `SELECT "seq" FROM main.sqlite_sequence WHERE "name"='blog_article'`) != articleSequence {
		t.Fatalf("rejected required reapply changed remade state:\nremoved=%+v\nafter=%+v", afterRemove, afterRejectedReapply)
	}
}

const definitionSQLiteRestartEpochSize = 16

type definitionSQLiteRestartSnapshot struct {
	Epoch       [definitionSQLiteRestartEpochSize]byte
	Revision    int64
	Fingerprint [sha256.Size]byte
	History     []backend.AppliedMigration
	Schema      []definitionSQLiteRestartSchemaObject
	Rows        []definitionSQLiteRestartRow
	ForeignKeys []definitionSQLiteRestartForeignKey
}

type definitionSQLiteRestartSchemaObject struct {
	Type       string
	Name       string
	Table      string
	Definition string
}

type definitionSQLiteRestartRow struct {
	Table     string
	ID        int64
	RelatedID int64
	Related   bool
	Text      string
}

type definitionSQLiteRestartForeignKey struct {
	SourceTable string
	ID          int64
	Sequence    int64
	TargetTable string
	FromColumn  string
	ToColumn    string
	OnUpdate    string
	OnDelete    string
	Match       string
}

func openDefinitionSQLiteRestartBackend(t *testing.T, path string) *sqlite.Backend {
	t.Helper()
	database, err := sqlite.Open(context.Background(), "file:"+filepath.ToSlash(path)+"?mode=rwc")
	if err != nil {
		t.Fatalf("Open(file-backed relation restart): %v", err)
	}
	return database
}

func closeDefinitionSQLiteRestartBackend(t *testing.T, database *sqlite.Backend) {
	t.Helper()
	if err := database.Close(); err != nil {
		t.Errorf("Close(file-backed relation restart): %v", err)
	}
}

func loadDefinitionSQLiteRestartSet(t *testing.T, order ...string) Set {
	t.Helper()
	sources := make([]Source, 0, len(order))
	for _, name := range order {
		switch name {
		case "authors":
			sources = append(sources, Source{
				SourceID: "legacy-authors-create",
				Document: legacyRelationRestartAuthorDocument(),
			})
		case "blog":
			sources = append(sources, Source{
				SourceID: "relation-blog-create",
				Document: relationCreateModelDocument(),
			})
		case "tail":
			sources = append(sources, Source{
				SourceID: "legacy-blog-tail",
				Document: legacyRelationRestartTailDocument(),
			})
		case "reviewer":
			sources = append(sources, Source{
				SourceID: "relation-blog-reviewer",
				Document: relationRestartReviewerDocument(),
			})
		case "nullable-reviewer":
			sources = append(sources, Source{
				SourceID: "relation-blog-nullable-reviewer",
				Document: relationRestartNullableReviewerDocument(),
			})
		default:
			t.Fatalf("unknown relation restart source %q", name)
		}
	}
	loaded, report, err := Load(sources...)
	if err != nil {
		t.Fatalf("Load(file-backed relation restart %v): %v", order, err)
	}
	if report.DocumentsReceived != len(order) || report.HeadersValidated != len(order) ||
		report.OperationsDecoded != len(order) || report.PlannerConstruction != 1 ||
		report.DefinitionsPublished != len(order) || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Load(file-backed relation restart %v) report = %+v", order, report)
	}
	return loaded
}

func legacyRelationRestartAuthorDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},` +
		`"producer":{"name":"restart-legacy","version":"1"},` +
		`"migration":{"app":"authors","name":"0001_author","dependencies":[],"operations":[` +
		`{"kind":"create_model","app_label":"authors","model":{` +
		`"name":"author","go_name":"Author","db_table":"authors_author","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`)
}

func legacyRelationRestartTailDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},` +
		`"producer":{"name":"restart-legacy","version":"1"},` +
		`"migration":{"app":"blog","name":"0002_article_title",` +
		`"dependencies":[{"app":"blog","name":"0001_article"}],"operations":[` +
		`{"kind":"add_field","app_label":"blog","model_name":"article","field":{` +
		`"name":"title","go_name":"Title","column":"title","kind":"char",` +
		`"primary_key":false,"nullable":false,"max_length":64,` +
		`"default":{"kind":"string","string":"untitled"}}}]}}`)
}

func relationRestartReviewerDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":2,"operation_codec":2,"schema_ir":3},` +
		`"producer":{"name":"restart-relation","version":"1"},` +
		`"migration":{"app":"blog","name":"0003_article_reviewer",` +
		`"dependencies":[{"app":"blog","name":"0002_article_title"}],"operations":[` +
		`{"kind":"add_field","app_label":"blog","model_name":"article","field":{` +
		`"name":"reviewer","go_name":"Reviewer","column":"reviewer_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":false,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"authors","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"review_comments","disabled":false},` +
		`"on_delete":"protect"}}}]}}`)
}

func relationRestartNullableReviewerDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":2,"operation_codec":2,"schema_ir":3},` +
		`"producer":{"name":"restart-relation","version":"1"},` +
		`"migration":{"app":"blog","name":"0003_article_reviewer",` +
		`"dependencies":[{"app":"blog","name":"0002_article_title"}],"operations":[` +
		`{"kind":"add_field","app_label":"blog","model_name":"article","field":{` +
		`"name":"reviewer","go_name":"Reviewer","column":"reviewer_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":true,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"authors","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"review_comments","disabled":false},` +
		`"on_delete":"protect"}}}]}}`)
}

func assertDefinitionSQLiteRestartRelationState(t *testing.T, state migrations.ProjectState, wantReviewer bool) {
	t.Helper()
	if state.FormatVersion() != migrations.RelationStateFormatVersion ||
		!reflect.DeepEqual(state.Apps(), []string{"authors", "blog"}) {
		t.Fatalf("relation restart state = format:%d apps:%v", state.FormatVersion(), state.Apps())
	}
	author, authorExists := state.Model("authors", "author")
	article, articleExists := state.Model("blog", "article")
	wantArticleFields := 3
	if wantReviewer {
		wantArticleFields = 4
	}
	if !authorExists || len(author.Fields) != 1 || !articleExists || len(article.Fields) != wantArticleFields ||
		article.Fields[1].Relation == nil || article.Fields[2].Name != "title" {
		t.Fatalf(
			"relation restart models = author:%#v/%t article:%#v/%t",
			author,
			authorExists,
			article,
			articleExists,
		)
	}
	if wantReviewer && (article.Fields[3].Name != "reviewer" || article.Fields[3].Relation == nil) {
		t.Fatalf("seeded reviewer state = %#v", article.Fields[3])
	}
}

func insertDefinitionSQLiteRestartRows(t *testing.T, database *sqlite.Backend, includeAuthor bool) {
	t.Helper()
	ctx := context.Background()
	if includeAuthor {
		if _, err := database.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (41)`); err != nil {
			t.Fatalf("insert restart author: %v", err)
		}
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id", "title") VALUES (51, 41, 'kept')`); err != nil {
		t.Fatalf("insert restart article: %v", err)
	}
}

func assertDefinitionSQLiteRestartForeignKeyEnforcement(t *testing.T, database *sqlite.Backend) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `INSERT INTO "blog_article" ("id", "author_id", "title") VALUES (52, 9999, 'orphan')`); err == nil {
		t.Fatal("file-backed restart accepted an orphan blog author")
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM "authors_author" WHERE "id" = 41`); err == nil {
		t.Fatal("file-backed restart deleted an author with a blog child")
	}
}

func assertDefinitionSQLiteRestartCapabilityError(
	t *testing.T,
	err error,
	direction migrations.Direction,
	feature,
	detail string,
) {
	t.Helper()
	var migrationError *migrations.Error
	var capabilityError *backend.CapabilityError
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
		migrationError.Code != migrations.CodeUnsupported || migrationError.Direction != direction ||
		migrationError.App != "blog" || migrationError.Migration != "0003_article_reviewer" ||
		migrationError.OperationIndex != migrations.NoOperation || !errors.As(err, &capabilityError) ||
		capabilityError.Feature != feature || !strings.Contains(capabilityError.Detail, detail) {
		t.Fatalf("relation restart capability error = %#v capability=%#v, want %s %s", err, capabilityError, direction, detail)
	}
}

func readDefinitionSQLiteRestartSnapshot(t *testing.T, path string) definitionSQLiteRestartSnapshot {
	t.Helper()
	ctx := context.Background()
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatalf("open read-only restart snapshot: %v", err)
	}
	reader.SetMaxOpenConns(1)
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close read-only restart snapshot: %v", err)
		}
	}()
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read-only restart snapshot: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	snapshot := definitionSQLiteRestartSnapshot{
		History:     make([]backend.AppliedMigration, 0),
		Schema:      make([]definitionSQLiteRestartSchemaObject, 0),
		Rows:        make([]definitionSQLiteRestartRow, 0),
		ForeignKeys: make([]definitionSQLiteRestartForeignKey, 0),
	}
	var formatVersion int64
	var epoch, fingerprint []byte
	if err := tx.QueryRowContext(
		ctx,
		`SELECT "format_version", "epoch", "revision", "history_fingerprint" `+
			`FROM "godj_migration_revision" WHERE "singleton" = 1`,
	).Scan(&formatVersion, &epoch, &snapshot.Revision, &fingerprint); err != nil {
		t.Fatalf("read restart revision token: %v", err)
	}
	if formatVersion != 1 || len(epoch) != definitionSQLiteRestartEpochSize || len(fingerprint) != sha256.Size {
		t.Fatalf("restart revision token shape = format:%d epoch:%d fingerprint:%d", formatVersion, len(epoch), len(fingerprint))
	}
	copy(snapshot.Epoch[:], epoch)
	copy(snapshot.Fingerprint[:], fingerprint)

	historyRows, err := tx.QueryContext(
		ctx,
		`SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`,
	)
	if err != nil {
		t.Fatalf("read full restart history: %v", err)
	}
	for historyRows.Next() {
		var migration backend.AppliedMigration
		if err := historyRows.Scan(&migration.App, &migration.Name); err != nil {
			_ = historyRows.Close()
			t.Fatalf("scan full restart history: %v", err)
		}
		snapshot.History = append(snapshot.History, migration)
	}
	if err := historyRows.Err(); err != nil {
		_ = historyRows.Close()
		t.Fatalf("iterate full restart history: %v", err)
	}
	if err := historyRows.Close(); err != nil {
		t.Fatalf("close full restart history: %v", err)
	}
	for index := 1; index < len(snapshot.History); index++ {
		previous := snapshot.History[index-1]
		current := snapshot.History[index]
		if previous.App > current.App || previous.App == current.App && previous.Name >= current.Name {
			t.Fatalf("restart history is not strictly sorted: %v", snapshot.History)
		}
	}
	computed := definitionSQLiteRestartHistoryFingerprint(snapshot.History)
	if computed != snapshot.Fingerprint {
		t.Fatalf("restart fingerprint = %x, independently computed %x", snapshot.Fingerprint, computed)
	}

	schemaRows, err := tx.QueryContext(
		ctx,
		`SELECT "type", "name", "tbl_name", COALESCE("sql", '') FROM main.sqlite_schema `+
			`WHERE "name" NOT LIKE 'sqlite_%' ORDER BY "type", "name", "tbl_name", "sql"`,
	)
	if err != nil {
		t.Fatalf("read restart schema: %v", err)
	}
	tables := make(map[string]struct{})
	for schemaRows.Next() {
		var object definitionSQLiteRestartSchemaObject
		if err := schemaRows.Scan(&object.Type, &object.Name, &object.Table, &object.Definition); err != nil {
			_ = schemaRows.Close()
			t.Fatalf("scan restart schema: %v", err)
		}
		snapshot.Schema = append(snapshot.Schema, object)
		if object.Type == "table" {
			tables[object.Name] = struct{}{}
		}
	}
	if err := schemaRows.Err(); err != nil {
		_ = schemaRows.Close()
		t.Fatalf("iterate restart schema: %v", err)
	}
	if err := schemaRows.Close(); err != nil {
		t.Fatalf("close restart schema: %v", err)
	}

	if _, exists := tables["authors_author"]; exists {
		readDefinitionSQLiteRestartRows(t, tx, &snapshot, "authors_author", `SELECT "id" FROM "authors_author" ORDER BY "id"`, false, false)
	}
	if _, exists := tables["blog_article"]; exists {
		readDefinitionSQLiteRestartRows(t, tx, &snapshot, "blog_article", `SELECT "id", "author_id", "title" FROM "blog_article" ORDER BY "id"`, true, true)
	}
	readDefinitionSQLiteRestartForeignKeys(t, tx, &snapshot, tables)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit read-only restart snapshot: %v", err)
	}
	committed = true
	return snapshot
}

func readDefinitionSQLiteInteger(t *testing.T, path, statement string) int64 {
	t.Helper()
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatalf("open read-only SQLite integer: %v", err)
	}
	defer func() { _ = reader.Close() }()
	var value int64
	if err := reader.QueryRowContext(context.Background(), statement).Scan(&value); err != nil {
		t.Fatalf("read SQLite integer with %q: %v", statement, err)
	}
	return value
}

func readDefinitionSQLiteNullableInteger(t *testing.T, path, statement string) sql.NullInt64 {
	t.Helper()
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatalf("open read-only SQLite nullable integer: %v", err)
	}
	defer func() { _ = reader.Close() }()
	var value sql.NullInt64
	if err := reader.QueryRowContext(context.Background(), statement).Scan(&value); err != nil {
		t.Fatalf("read SQLite nullable integer with %q: %v", statement, err)
	}
	return value
}

func readDefinitionSQLiteRestartRows(
	t *testing.T,
	tx *sql.Tx,
	snapshot *definitionSQLiteRestartSnapshot,
	table string,
	statement string,
	related bool,
	text bool,
) {
	t.Helper()
	rows, err := tx.QueryContext(context.Background(), statement)
	if err != nil {
		t.Fatalf("read restart rows for %s: %v", table, err)
	}
	for rows.Next() {
		row := definitionSQLiteRestartRow{Table: table, Related: related}
		if related && text {
			err = rows.Scan(&row.ID, &row.RelatedID, &row.Text)
		} else if related {
			err = rows.Scan(&row.ID, &row.RelatedID)
		} else {
			err = rows.Scan(&row.ID)
		}
		if err != nil {
			_ = rows.Close()
			t.Fatalf("scan restart rows for %s: %v", table, err)
		}
		snapshot.Rows = append(snapshot.Rows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatalf("iterate restart rows for %s: %v", table, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close restart rows for %s: %v", table, err)
	}
}

func readDefinitionSQLiteRestartForeignKeys(
	t *testing.T,
	tx *sql.Tx,
	snapshot *definitionSQLiteRestartSnapshot,
	tables map[string]struct{},
) {
	t.Helper()
	checks := []struct {
		table     string
		statement string
	}{
		{table: "authors_author", statement: `PRAGMA main.foreign_key_list("authors_author")`},
		{table: "blog_article", statement: `PRAGMA main.foreign_key_list("blog_article")`},
	}
	for _, check := range checks {
		if _, exists := tables[check.table]; !exists {
			continue
		}
		rows, err := tx.QueryContext(context.Background(), check.statement)
		if err != nil {
			t.Fatalf("read restart foreign keys for %s: %v", check.table, err)
		}
		for rows.Next() {
			foreignKey := definitionSQLiteRestartForeignKey{SourceTable: check.table}
			if err := rows.Scan(
				&foreignKey.ID,
				&foreignKey.Sequence,
				&foreignKey.TargetTable,
				&foreignKey.FromColumn,
				&foreignKey.ToColumn,
				&foreignKey.OnUpdate,
				&foreignKey.OnDelete,
				&foreignKey.Match,
			); err != nil {
				_ = rows.Close()
				t.Fatalf("scan restart foreign keys for %s: %v", check.table, err)
			}
			snapshot.ForeignKeys = append(snapshot.ForeignKeys, foreignKey)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("iterate restart foreign keys for %s: %v", check.table, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close restart foreign keys for %s: %v", check.table, err)
		}
	}
}

func definitionSQLiteRestartHistoryFingerprint(records []backend.AppliedMigration) [sha256.Size]byte {
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(records)))
	_, _ = hash.Write(length[:])
	for _, record := range records {
		for _, value := range []string{record.App, record.Name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

func assertDefinitionSQLiteRestartToken(
	t *testing.T,
	snapshot definitionSQLiteRestartSnapshot,
	wantRevision int64,
	wantFingerprint string,
	wantHistory []backend.AppliedMigration,
) {
	t.Helper()
	if snapshot.Epoch == ([definitionSQLiteRestartEpochSize]byte{}) {
		t.Fatal("restart epoch is all zero")
	}
	if snapshot.Revision != wantRevision || hex.EncodeToString(snapshot.Fingerprint[:]) != wantFingerprint ||
		!reflect.DeepEqual(snapshot.History, wantHistory) {
		t.Fatalf(
			"restart token/history = revision:%d fingerprint:%x history:%v, want revision:%d fingerprint:%s history:%v",
			snapshot.Revision,
			snapshot.Fingerprint,
			snapshot.History,
			wantRevision,
			wantFingerprint,
			wantHistory,
		)
	}
}

func assertDefinitionSQLiteRestartForeignKeys(t *testing.T, snapshot definitionSQLiteRestartSnapshot) {
	t.Helper()
	want := []definitionSQLiteRestartForeignKey{
		{
			SourceTable: "blog_article", ID: 0, Sequence: 0,
			TargetTable: "authors_author", FromColumn: "author_id", ToColumn: "id",
			OnUpdate: "NO ACTION", OnDelete: "NO ACTION", Match: "NONE",
		},
	}
	if !reflect.DeepEqual(snapshot.ForeignKeys, want) {
		t.Fatalf("restart physical foreign keys = %+v, want %+v", snapshot.ForeignKeys, want)
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
