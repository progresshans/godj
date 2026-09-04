//go:build darwin || linux

package projectmigrateproduct_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

const (
	failureResumeApp               = "failure_resume"
	failureResumePrefixMigration   = "0001_prefix"
	failureResumeMiddleMigration   = "0002_middle"
	failureResumeTailMigration     = "0003_tail"
	failureResumePrefixTable       = "failure_resume_prefix"
	failureResumeMiddleEffectTable = "failure_resume_middle_effect"
	failureResumeBadMiddleTable    = "failure_resume_oversized_middle"
	failureResumeGoodMiddleTable   = "failure_resume_middle"
	failureResumeTailTable         = "failure_resume_tail"
	failureResumeSentinelColumn    = "value"
	failureResumeSentinel          = "durable prefix row"
	failureResumeProjectModule     = "example.com/godj-failure-resume"
	failureResumeDefinitionSource  = "failure-resume-product"
	failureResumeObservationEnv    = "GODJ_FAILURE_RESUME_OBSERVATION"
)

func TestGlobalMigrateSQLiteMiddleFailureAndFreshResume(t *testing.T) {
	// This is a focused, instrumented product regression for the MIG-093/094
	// observations. It deliberately does not register a product adapter or
	// change either contract from oracle_locked.
	repository := repositoryRoot(t)
	projectRoot, descriptor := failureResumeCreateProject(t, repository)
	globalBinary := buildGlobalGodj(t, repository)
	databasePath := filepath.Join(t.TempDir(), "middle-failure-resume.sqlite3")
	workspaceBase := newWorkspaceBase(t)
	observationPath := filepath.Join(t.TempDir(), "migration-observations.log")
	environmentValues := environmentMap(articleEnvironment(t, databasePath, workspaceBase))
	environmentValues[failureResumeObservationEnv] = observationPath
	environment := sortedEnvironment(environmentValues)

	// The first catalog is a valid three-step chain. Its middle CreateModel is
	// accepted by the backend-neutral IR, but its 2,001 columns exceed SQLite's
	// execution limit when the operation actually executes. Lock that exact
	// setup before invoking the product so a loader/preflight failure, an error
	// in the prefix, or a missing tail cannot make this regression pass.
	failureResumeAssertCatalog(t, projectRoot, failureResumeBadMiddleTable)
	prefixSourceDigest := digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumePrefixMigration))
	badMiddleSourceDigest := digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeMiddleMigration))
	tailSourceDigest := digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeTailMigration))

	// runMigrate starts the actual global CLI, which builds and starts a fresh
	// project-linked child. Step 0001 commits. Step 0002 then fails inside its
	// fenced transaction after creating its first table. That rollback-sensitive
	// table must disappear, directly observing the one current-step rollback;
	// the executor must not begin 0003.
	failed := runMigrate(t, globalBinary, repository, descriptor, environment)
	assertOutputSanitized(t, failed, databasePath, observationPath, projectRoot)
	assertMigrateFailure(t, failed, 3, "migration_execution_error/operation_failed\n")
	assertWorkspaceEmpty(t, workspaceBase)
	failureResumeAssertObservations(t, observationPath, []string{
		"begin failure_resume.0001_prefix",
		"create failure_resume_prefix",
		"record failure_resume.0001_prefix",
		"begin failure_resume.0002_middle",
		"create failure_resume_middle_effect",
		"create failure_resume_oversized_middle",
		"rollback failure_resume.0002_middle",
	})
	failureResumeAssertCommittedPrefixOnly(t, databasePath, "")
	if digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumePrefixMigration)) != prefixSourceDigest ||
		digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeMiddleMigration)) != badMiddleSourceDigest ||
		digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeTailMigration)) != tailSourceDigest {
		t.Fatal("failed migrate changed a definition source")
	}

	// Durable application data makes the second observation stronger than a
	// schema-only check. Only the failed and therefore unapplied 0002 document
	// is corrected; mutating it is safe in this pre-release test because history
	// proves its key was never committed. The loaded catalog/source digest must
	// change, while the already-applied 0001 document and durable marker remain
	// byte-for-byte/logically unchanged.
	sentinelID := failureResumeInsertSentinel(t, databasePath)
	badDigest := failureResumeLoadCatalog(t, projectRoot).Digest()
	failureResumeWriteMiddleDefinition(t, projectRoot, failureResumeGoodMiddleTable)
	goodMiddleSourceDigest := digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeMiddleMigration))
	expected := failureResumeExpectedCatalog(t, projectRoot)
	if expected.Command.DefinitionSetDigest == badDigest {
		t.Fatal("corrected unapplied middle definition did not change catalog digest")
	}
	if goodMiddleSourceDigest == badMiddleSourceDigest {
		t.Fatal("correcting unapplied middle definition did not change middle source bytes")
	}
	if digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumePrefixMigration)) != prefixSourceDigest ||
		digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeTailMigration)) != tailSourceDigest {
		t.Fatal("correcting unapplied middle definition changed prefix or tail source bytes")
	}
	prefixWithSentinel := failureResumeAssertCommittedPrefixOnly(t, databasePath, failureResumeSentinel)
	prefixEpoch := failureResumeReadEpoch(t, databasePath)
	failureResumeResetObservations(t, observationPath)

	// This is a second global process and a second project-linked child. The
	// existing prefix table would make a replay fail. Success plus revision 3,
	// exact history/schema, and the preserved row therefore prove prefix writes
	// are zero, resume starts at 0002, and the previously unstarted 0003 tail is
	// applied exactly once.
	resumed := runMigrate(t, globalBinary, repository, descriptor, environment)
	assertMigrateSuccess(t, resumed, expected, databasePath, observationPath, projectRoot)
	assertWorkspaceEmpty(t, workspaceBase)
	failureResumeAssertObservations(t, observationPath, []string{
		"begin failure_resume.0002_middle",
		"create failure_resume_middle_effect",
		"create failure_resume_middle",
		"record failure_resume.0002_middle",
		"begin failure_resume.0003_tail",
		"create failure_resume_tail",
		"record failure_resume.0003_tail",
	})
	failureResumeAssertLatest(t, databasePath, expected, prefixWithSentinel, prefixEpoch, sentinelID)
	if digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumePrefixMigration)) != prefixSourceDigest ||
		digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeMiddleMigration)) != goodMiddleSourceDigest ||
		digestFile(t, failureResumeDefinitionPath(projectRoot, failureResumeTailMigration)) != tailSourceDigest {
		t.Fatal("resumed migrate changed a definition source")
	}
}

func failureResumeCreateProject(t *testing.T, repository string) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{filepath.Join(root, "cmd", "projectrunner"), filepath.Join(root, "migrations")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create failure-resume project directory: %v", err)
		}
	}
	repositoryGoMod, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		t.Fatalf("read repository go.mod: %v", err)
	}
	goMod := strings.Replace(string(repositoryGoMod), "module github.com/progresshans/godj", "module "+failureResumeProjectModule, 1)
	goMod += "\nrequire github.com/progresshans/godj v0.0.0\n\n" +
		"replace github.com/progresshans/godj => " + strconv.Quote(filepath.ToSlash(repository)) + "\n"
	failureResumeWriteFile(t, filepath.Join(root, "go.mod"), []byte(goMod))
	goSum, err := os.ReadFile(filepath.Join(repository, "go.sum"))
	if err != nil {
		t.Fatalf("read repository go.sum: %v", err)
	}
	failureResumeWriteFile(t, filepath.Join(root, "go.sum"), goSum)
	failureResumeWriteFile(t, filepath.Join(root, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n"))
	failureResumeWriteFile(t, filepath.Join(root, "cmd", "projectrunner", "main.go"), []byte(`package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/progresshans/godj/examples/article/databaseconfig"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/schema/ir"
)

type observationBackend struct {
	godjproject.MigrationBackend
	path string
}

func (backend *observationBackend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	session, err := backend.MigrationBackend.OpenRevisionFencedSession(ctx)
	if session == nil {
		return nil, err
	}
	return &observationSession{RevisionFencedSession: session, path: backend.path}, err
}

type observationSession struct {
	migrationbackend.RevisionFencedSession
	path string
}

func (session *observationSession) BeginMigration(ctx context.Context, transition migrationbackend.HistoryTransition, intent migrationbackend.MigrationIntent) (migrationbackend.RevisionFencedTransaction, error) {
	name := transition.Migration.App + "." + transition.Migration.Name
	if err := appendObservation(session.path, "begin "+name+"\n"); err != nil {
		return nil, err
	}
	transaction, err := session.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if transaction == nil {
		return nil, err
	}
	return &observationTransaction{RevisionFencedTransaction: transaction, path: session.path, name: name}, err
}

type observationTransaction struct {
	migrationbackend.RevisionFencedTransaction
	path string
	name string
}

func (transaction *observationTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	if err := appendObservation(transaction.path, "create "+model.DBTable+"\n"); err != nil {
		return err
	}
	return transaction.RevisionFencedTransaction.CreateModel(ctx, model)
}

func (transaction *observationTransaction) RecordApplied(ctx context.Context, app, name string) error {
	if err := appendObservation(transaction.path, "record "+app+"."+name+"\n"); err != nil {
		return err
	}
	return transaction.RevisionFencedTransaction.RecordApplied(ctx, app, name)
}

func (transaction *observationTransaction) Rollback(ctx context.Context) error {
	markerErr := appendObservation(transaction.path, "rollback "+transaction.name+"\n")
	return errors.Join(markerErr, transaction.RevisionFencedTransaction.Rollback(ctx))
}

func appendObservation(path, value string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	return errors.Join(writeErr, file.Close())
}

func main() {
	err := godjproject.Run(context.Background(), godjproject.Config{
		MigrationDefinitionRoots: []string{"migrations"},
		OpenMigrationBackend: func(ctx context.Context) (godjproject.MigrationBackend, error) {
			config, err := databaseconfig.FromEnvironment(os.LookupEnv)
			if err != nil {
				return nil, err
			}
			backend, err := databaseconfig.Open(ctx, config)
			if err != nil {
				return nil, err
			}
			return &observationBackend{MigrationBackend: backend, path: os.Getenv("GODJ_FAILURE_RESUME_OBSERVATION")}, nil
		},
	}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		os.Exit(1)
	}
}
`))
	failureResumeWriteDefinition(t, root, failureResumePrefixMigration, "", "prefix", "Prefix", failureResumePrefixTable, true)
	failureResumeWriteMiddleDefinition(t, root, failureResumeBadMiddleTable)
	failureResumeWriteDefinition(t, root, failureResumeTailMigration, failureResumeMiddleMigration, "tail", "Tail", failureResumeTailTable, false)
	return root, filepath.Join(root, "godj.toml")
}

func failureResumeWriteMiddleDefinition(t *testing.T, root, table string) {
	t.Helper()
	dependencies := fmt.Sprintf(`[{"app":%q,"name":%q}]`, failureResumeApp, failureResumePrefixMigration)
	idField := `[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}]`
	middleFields := idField
	if table == failureResumeBadMiddleTable {
		var fields strings.Builder
		fields.WriteString(`[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}`)
		for index := 0; index < 2_000; index++ {
			_, _ = fmt.Fprintf(
				&fields,
				`,{"name":"field_%04d","go_name":"Field%04d","column":"field_%04d","kind":"char","primary_key":false,"nullable":true,"max_length":1,"default":null}`,
				index,
				index,
				index,
			)
		}
		fields.WriteByte(']')
		middleFields = fields.String()
	}
	document := fmt.Sprintf(`{"format_version":1,"producer":{"name":%q,"version":"1"},"migration":{"app":%q,"name":%q,"dependencies":%s,"operations":[{"kind":"create_model","app_label":%q,"model":{"name":"middle_effect","go_name":"MiddleEffect","db_table":%q,"fields":%s}},{"kind":"create_model","app_label":%q,"model":{"name":"middle","go_name":"Middle","db_table":%q,"fields":%s}}]}}`,
		failureResumeDefinitionSource,
		failureResumeApp,
		failureResumeMiddleMigration,
		dependencies,
		failureResumeApp,
		failureResumeMiddleEffectTable,
		idField,
		failureResumeApp,
		table,
		middleFields,
	)
	failureResumeWriteFile(t, failureResumeDefinitionPath(root, failureResumeMiddleMigration), []byte(document+"\n"))
}

func failureResumeWriteDefinition(
	t *testing.T,
	root, name, dependency, modelName, goName, table string,
	withSentinel bool,
) {
	t.Helper()
	dependencies := "[]"
	if dependency != "" {
		dependencies = fmt.Sprintf(`[{"app":%q,"name":%q}]`, failureResumeApp, dependency)
	}
	fields := `[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}]`
	if withSentinel {
		fields = `[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null},{"name":"value","go_name":"Value","column":"value","kind":"char","primary_key":false,"nullable":false,"max_length":128,"default":null}]`
	}
	document := fmt.Sprintf(`{"format_version":1,"producer":{"name":%q,"version":"1"},"migration":{"app":%q,"name":%q,"dependencies":%s,"operations":[{"kind":"create_model","app_label":%q,"model":{"name":%q,"go_name":%q,"db_table":%q,"fields":%s}}]}}`,
		failureResumeDefinitionSource, failureResumeApp, name, dependencies, failureResumeApp, modelName, goName, table, fields)
	document += "\n"
	failureResumeWriteFile(t, failureResumeDefinitionPath(root, name), []byte(document))
}

func failureResumeDefinitionPath(root, name string) string {
	return filepath.Join(root, "migrations", name+".godj.json")
}

func failureResumeWriteFile(t *testing.T, path string, document []byte) {
	t.Helper()
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatalf("write failure-resume project file %s: %v", filepath.Base(path), err)
	}
}

func failureResumeAssertCatalog(t *testing.T, root, middleTable string) {
	t.Helper()
	loaded := failureResumeLoadCatalog(t, root)
	definitions := loaded.Definitions()
	wantKeys := []migrations.MigrationKey{
		{App: failureResumeApp, Name: failureResumePrefixMigration},
		{App: failureResumeApp, Name: failureResumeMiddleMigration},
		{App: failureResumeApp, Name: failureResumeTailMigration},
	}
	wantDependencies := [][]migrations.MigrationKey{
		{},
		{{App: failureResumeApp, Name: failureResumePrefixMigration}},
		{{App: failureResumeApp, Name: failureResumeMiddleMigration}},
	}
	wantTables := [][]string{
		{failureResumePrefixTable},
		{failureResumeMiddleEffectTable, middleTable},
		{failureResumeTailTable},
	}
	if len(definitions) != len(wantKeys) {
		t.Fatalf("failure-resume definition count = %d, want %d", len(definitions), len(wantKeys))
	}
	for index, definition := range definitions {
		if definition.Key() != wantKeys[index] || !reflect.DeepEqual(definition.Dependencies, wantDependencies[index]) || len(definition.Operations) != len(wantTables[index]) {
			t.Fatalf("failure-resume definition[%d] = key:%+v dependencies:%+v operations:%d", index, definition.Key(), definition.Dependencies, len(definition.Operations))
		}
		for operationIndex, operation := range definition.Operations {
			create, ok := operation.(migrations.CreateModel)
			if !ok || create.Model.DBTable != wantTables[index][operationIndex] {
				t.Fatalf("failure-resume operation[%d][%d] = %T/%+v, want CreateModel table %q", index, operationIndex, operation, operation, wantTables[index][operationIndex])
			}
			wantFields := 1
			if index == 0 {
				wantFields = 2
			} else if middleTable == failureResumeBadMiddleTable && index == 1 && operationIndex == 1 {
				wantFields = 2_001
			}
			if len(create.Model.Fields) != wantFields {
				t.Fatalf("failure-resume operation[%d][%d] fields = %d, want %d", index, operationIndex, len(create.Model.Fields), wantFields)
			}
		}
	}
}

func failureResumeLoadCatalog(t *testing.T, root string) migrations.LoadedDefinitionSet {
	t.Helper()
	names := []string{failureResumePrefixMigration, failureResumeMiddleMigration, failureResumeTailMigration}
	sources := make([]migrationdefinition.Source, len(names))
	for index, name := range names {
		path := failureResumeDefinitionPath(root, name)
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read failure-resume definition %s: %v", name, err)
		}
		sources[index] = migrationdefinition.Source{SourceID: "migrations/" + name + ".godj.json", Document: document}
	}
	loaded, report, err := migrationdefinition.Load(sources...)
	if err != nil {
		t.Fatalf("load failure-resume catalog: %v", err)
	}
	if report.DocumentsReceived != 3 || report.HeadersValidated != 3 || report.OperationsDecoded != 4 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 3 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("failure-resume catalog report = %+v", report)
	}
	return loaded
}

func failureResumeExpectedCatalog(t *testing.T, root string) articleCatalogExpectation {
	t.Helper()
	failureResumeAssertCatalog(t, root, failureResumeGoodMiddleTable)
	loaded := failureResumeLoadCatalog(t, root)
	definitions := loaded.Definitions()
	history := make([]historyRow, len(definitions))
	for index, definition := range definitions {
		history[index] = historyRow{App: definition.App, Name: definition.Name}
	}
	sort.Slice(history, func(left, right int) bool {
		if history[left].App != history[right].App {
			return history[left].App < history[right].App
		}
		return history[left].Name < history[right].Name
	})
	return articleCatalogExpectation{
		Command: migrateResult{
			SourceCount:         len(loaded.Sources()),
			DefinitionCount:     len(definitions),
			DefinitionSetDigest: loaded.Digest(),
		},
		History:            history,
		HistoryFingerprint: fingerprintHistory(history),
		DefinitionDigest:   decodeSHA256Digest(t, loaded.Digest()),
	}
}

func failureResumeAssertCommittedPrefixOnly(t *testing.T, databasePath, sentinel string) revisionRow {
	t.Helper()
	snapshot := inspectDatabase(t, databasePath)
	wantTables := []string{failureResumePrefixTable, "godj_migration_revision", "godj_migrations"}
	if !reflect.DeepEqual(snapshot.Tables, wantTables) {
		t.Fatalf("middle-failure tables = %v, want exact committed prefix %v", snapshot.Tables, wantTables)
	}
	wantHistory := []historyRow{{App: failureResumeApp, Name: failureResumePrefixMigration}}
	if !reflect.DeepEqual(snapshot.History, wantHistory) {
		t.Fatalf("middle-failure history = %+v, want durable prefix %+v", snapshot.History, wantHistory)
	}
	assertRevision(t, snapshot.Revision, 1, wantHistory, fingerprintHistory(wantHistory))
	failureResumeAssertColumns(t, snapshot, map[string][]columnSnapshot{
		failureResumePrefixTable: {
			{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1},
			{Name: failureResumeSentinelColumn, Type: "VARCHAR(128)", NotNull: 1},
		},
		"godj_migration_revision": expectedColumns()["godj_migration_revision"],
		"godj_migrations":         expectedColumns()["godj_migrations"],
	})
	failureResumeAssertTableAbsent(t, databasePath, failureResumeBadMiddleTable)
	failureResumeAssertTableAbsent(t, databasePath, failureResumeGoodMiddleTable)
	failureResumeAssertTableAbsent(t, databasePath, failureResumeMiddleEffectTable)
	failureResumeAssertTableAbsent(t, databasePath, failureResumeTailTable)
	failureResumeAssertSentinel(t, databasePath, sentinel)
	return snapshot.Revision
}

func failureResumeAssertLatest(
	t *testing.T,
	databasePath string,
	expected articleCatalogExpectation,
	prefix revisionRow,
	prefixEpoch []byte,
	sentinelID int64,
) {
	t.Helper()
	snapshot := inspectDatabase(t, databasePath)
	wantTables := []string{
		failureResumeGoodMiddleTable,
		failureResumeMiddleEffectTable,
		failureResumePrefixTable,
		failureResumeTailTable,
		"godj_migration_revision",
		"godj_migrations",
	}
	if !reflect.DeepEqual(snapshot.Tables, wantTables) {
		t.Fatalf("resumed latest tables = %v, want %v", snapshot.Tables, wantTables)
	}
	if !reflect.DeepEqual(snapshot.History, expected.History) {
		t.Fatalf("resumed latest history = %+v, want %+v", snapshot.History, expected.History)
	}
	assertRevision(t, snapshot.Revision, 3, expected.History, expected.HistoryFingerprint)
	if snapshot.Revision.EpochBytes != prefix.EpochBytes || !reflect.DeepEqual(failureResumeReadEpoch(t, databasePath), prefixEpoch) {
		t.Fatalf("resumed revision epoch differs from durable prefix epoch")
	}
	failureResumeAssertColumns(t, snapshot, map[string][]columnSnapshot{
		failureResumeGoodMiddleTable:   {{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1}},
		failureResumeMiddleEffectTable: {{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1}},
		failureResumePrefixTable: {
			{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1},
			{Name: failureResumeSentinelColumn, Type: "VARCHAR(128)", NotNull: 1},
		},
		failureResumeTailTable:    {{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1}},
		"godj_migration_revision": expectedColumns()["godj_migration_revision"],
		"godj_migrations":         expectedColumns()["godj_migrations"],
	})
	failureResumeAssertTableAbsent(t, databasePath, failureResumeBadMiddleTable)
	failureResumeAssertSentinel(t, databasePath, failureResumeSentinel, sentinelID)
}

func failureResumeAssertColumns(t *testing.T, snapshot databaseSnapshot, want map[string][]columnSnapshot) {
	t.Helper()
	if len(snapshot.Columns) != len(want) {
		t.Fatalf("failure-resume column table count = %d, want %d: %+v", len(snapshot.Columns), len(want), snapshot.Columns)
	}
	for table, columns := range want {
		if !reflect.DeepEqual(snapshot.Columns[table], columns) {
			t.Fatalf("failure-resume columns for %s = %+v, want %+v", table, snapshot.Columns[table], columns)
		}
	}
}

func failureResumeAssertObservations(t *testing.T, path string, want []string) {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failure-resume process observations: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(document), "\n"), "\n")
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("failure-resume process observations = %v, want %v", lines, want)
	}
}

func failureResumeResetObservations(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("reset failure-resume process observations: %v", err)
	}
}

func failureResumeInsertSentinel(t *testing.T, databasePath string) int64 {
	t.Helper()
	database := failureResumeOpenDatabase(t, databasePath)
	defer failureResumeCloseDatabase(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := database.ExecContext(ctx, `INSERT INTO "failure_resume_prefix" ("value") VALUES (?)`, failureResumeSentinel)
	if err != nil {
		t.Fatalf("insert failure-resume durable prefix sentinel: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil || id != 1 {
		t.Fatalf("failure-resume durable prefix sentinel id = %d, error=%v, want 1", id, err)
	}
	return id
}

func failureResumeAssertSentinel(t *testing.T, databasePath, want string, wantID ...int64) {
	t.Helper()
	database := failureResumeOpenDatabase(t, databasePath)
	defer failureResumeCloseDatabase(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := database.QueryContext(ctx, `SELECT "id", "value" FROM "failure_resume_prefix" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("read failure-resume durable prefix rows: %v", err)
	}
	defer rows.Close()
	var values []string
	var ids []int64
	for rows.Next() {
		var id int64
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatalf("scan failure-resume durable prefix row: %v", err)
		}
		ids = append(ids, id)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate failure-resume durable prefix rows: %v", err)
	}
	wantValues := []string(nil)
	if want != "" {
		wantValues = []string{want}
	}
	if !reflect.DeepEqual(values, wantValues) {
		t.Fatalf("failure-resume durable prefix rows = %v, want %v", values, wantValues)
	}
	if len(wantID) != 0 && !reflect.DeepEqual(ids, wantID) {
		t.Fatalf("failure-resume durable prefix row ids = %v, want %v", ids, wantID)
	}
}

func failureResumeReadEpoch(t *testing.T, databasePath string) []byte {
	t.Helper()
	database := failureResumeOpenDatabase(t, databasePath)
	defer failureResumeCloseDatabase(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var epoch []byte
	if err := database.QueryRowContext(ctx, `SELECT "epoch" FROM "godj_migration_revision" WHERE "singleton" = 1`).Scan(&epoch); err != nil {
		t.Fatalf("read failure-resume revision epoch: %v", err)
	}
	if len(epoch) != 16 {
		t.Fatalf("failure-resume revision epoch bytes = %d, want 16", len(epoch))
	}
	return append([]byte(nil), epoch...)
}

func failureResumeAssertTableAbsent(t *testing.T, databasePath, table string) {
	t.Helper()
	database := failureResumeOpenDatabase(t, databasePath)
	defer failureResumeCloseDatabase(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "sqlite_schema" WHERE "name" = ?`, table).Scan(&count); err != nil {
		t.Fatalf("inspect failure-resume table %q: %v", table, err)
	}
	if count != 0 {
		t.Fatalf("failure-resume table %q exists after failed/finished lifecycle", table)
	}
}

func failureResumeOpenDatabase(t *testing.T, databasePath string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open failure-resume SQLite database: %v", err)
	}
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping failure-resume SQLite database: %v", err)
	}
	return database
}

func failureResumeCloseDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	if err := database.Close(); err != nil {
		t.Errorf("close failure-resume SQLite database: %v", err)
	}
}
