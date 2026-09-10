//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"

	"github.com/progresshans/godj/conformance/internal/protocol"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const migrationStatusSecretCanary = "godj-migration-status-private-secret-canary"

type migrationStatusRegistration struct {
	id      string
	phase   protocol.Phase
	handler scenarioHandler
}

var migrationStatusScenarioRegistry = map[string]migrationStatusRegistration{
	"godj.migration.status.empty_catalog": {
		id: "MIG-111", phase: protocol.PhaseEvaluation, handler: migrationStatusEmptyCatalog,
	},
	"django.migration.status.fresh_unapplied": {
		id: "MIG-112", phase: protocol.PhaseEvaluation, handler: migrationStatusFreshUnapplied,
	},
	"django.migration.status.applied_prefix": {
		id: "MIG-113", phase: protocol.PhaseEvaluation, handler: migrationStatusAppliedPrefix,
	},
	"django.migration.status.fully_applied_restart": {
		id: "MIG-114", phase: protocol.PhaseEvaluation, handler: migrationStatusFullyAppliedRestart,
	},
	"django.migration.status.cross_app_branch_order": {
		id: "MIG-115", phase: protocol.PhaseEvaluation, handler: migrationStatusCrossAppBranchOrder,
	},
	"godj.migration.status.unknown_record_visible": {
		id: "MIG-116", phase: protocol.PhaseEvaluation, handler: migrationStatusUnknownRecordVisible,
	},
	"godj.migration.status.inconsistent_known_history": {
		id: "MIG-117", phase: protocol.PhaseEvaluation, handler: migrationStatusInconsistentKnownHistory,
	},
	"godj.migration.status.project_boundary": {
		id: "MIG-118", phase: protocol.PhaseEnvironment, handler: migrationStatusProjectBoundary,
	},
}

func migrationStatusScenarioHandler(scenario string) (scenarioHandler, bool) {
	registration, ok := migrationStatusScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		if ctx == nil {
			return protocol.Observation{}, errors.New("migration-status scenario context is nil")
		}
		if contract.ID != registration.id {
			return protocol.Observation{}, fmt.Errorf("migration-status scenario %q contract id %q; want %q", scenario, contract.ID, registration.id)
		}
		if contract.Scenario != scenario {
			return protocol.Observation{}, fmt.Errorf("migration-status scenario %q contract scenario %q", scenario, contract.Scenario)
		}
		if contract.Phase != registration.phase {
			return protocol.Observation{}, fmt.Errorf("migration-status scenario %q phase %q; want %q", scenario, contract.Phase, registration.phase)
		}
		return registration.handler(ctx, contract)
	}, true
}

type migrationStatusProject struct {
	universe    string
	root        string
	environment []string
}

func newMigrationStatusProject() (migrationStatusProject, error) {
	universe, err := os.MkdirTemp("", "godj-migration-status-actual-")
	if err != nil {
		return migrationStatusProject{}, fmt.Errorf("create migration-status project: %w", err)
	}
	temporaryUniverse := universe
	universe, err = filepath.EvalSymlinks(universe)
	if err == nil {
		universe, err = filepath.Abs(universe)
	}
	if err != nil {
		return migrationStatusProject{}, errors.Join(err, os.RemoveAll(temporaryUniverse))
	}
	project := migrationStatusProject{universe: universe, root: filepath.Join(universe, "project")}
	fail := func(cause error) (migrationStatusProject, error) {
		return migrationStatusProject{}, errors.Join(cause, os.RemoveAll(universe))
	}
	for _, relative := range []string{"project", "home", "config", "cache", "tmp"} {
		if err := os.Mkdir(filepath.Join(universe, relative), 0o700); err != nil {
			return fail(fmt.Errorf("create migration-status directory: %w", err))
		}
	}
	if err := os.WriteFile(
		filepath.Join(project.root, "godj.toml"),
		[]byte("format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n"),
		0o600,
	); err != nil {
		return fail(fmt.Errorf("write migration-status descriptor: %w", err))
	}
	project.environment = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(universe, "home"),
		"XDG_CONFIG_HOME=" + filepath.Join(universe, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(universe, "cache"),
		"TMPDIR=" + filepath.Join(universe, "tmp"),
		"GOTELEMETRY=off",
	}
	return project, nil
}

func (project migrationStatusProject) close() error { return os.RemoveAll(project.universe) }

type migrationStatusDatabase struct {
	session              *migrationStatusSession
	trace                *migrationStatusEventTrace
	returnSession        bool
	openSessionCalls     int
	sessionAcquisitions  int
	closeCalls           int
	openSessionErr       error
	closeErr             error
	schemaMutations      int
	recorderMutations    int
	revisionMutations    int
	applicationMutations int
}

func newMigrationStatusDatabase(records []backend.AppliedMigration) *migrationStatusDatabase {
	database := &migrationStatusDatabase{
		session:       &migrationStatusSession{records: append([]backend.AppliedMigration(nil), records...)},
		returnSession: true,
	}
	database.session.database = database
	return database
}

func (*migrationStatusDatabase) MigrationCapabilities() backend.MigrationCapabilities {
	return backend.MigrationCapabilities{}
}

func (database *migrationStatusDatabase) OpenRevisionFencedSession(context.Context) (backend.RevisionFencedSession, error) {
	database.openSessionCalls++
	database.trace.record("revision_session_open")
	if !database.returnSession {
		return nil, database.openSessionErr
	}
	database.sessionAcquisitions++
	return database.session, database.openSessionErr
}

func (database *migrationStatusDatabase) Close() error {
	database.closeCalls++
	database.trace.record("backend_close")
	return database.closeErr
}

type migrationStatusSession struct {
	database   *migrationStatusDatabase
	trace      *migrationStatusEventTrace
	records    []backend.AppliedMigration
	readCalls  int
	beginCalls int
	closeCalls int
	readErr    error
	closeErr   error
}

func (session *migrationStatusSession) ReadAppliedMigrations(context.Context) ([]backend.AppliedMigration, error) {
	session.readCalls++
	session.trace.record("history_read")
	return append([]backend.AppliedMigration(nil), session.records...), session.readErr
}

func (session *migrationStatusSession) BeginMigration(context.Context, backend.HistoryTransition, backend.MigrationIntent) (backend.RevisionFencedTransaction, error) {
	session.beginCalls++
	session.trace.record("migration_begin")
	if session.database != nil {
		// BeginMigration is the only mutation-capable entry on this read-session
		// double. Count an attempted write against every durable domain so the
		// published zeroes cannot remain true if showmigrations crosses it.
		session.database.schemaMutations++
		session.database.recorderMutations++
		session.database.revisionMutations++
		session.database.applicationMutations++
	}
	return nil, errors.New("migration-status read-only session attempted a migration")
}

func (session *migrationStatusSession) Close(context.Context) error {
	session.closeCalls++
	session.trace.record("revision_session_close")
	return session.closeErr
}

type migrationStatusEventTrace struct {
	events []string
}

func (trace *migrationStatusEventTrace) record(event string) {
	if trace != nil {
		trace.events = append(trace.events, event)
	}
}

type migrationStatusOpenResult struct {
	database *migrationStatusDatabase
	err      error
}

type migrationStatusProcessBackend struct {
	project       *migrationStatusProject
	sources       []definition.Source
	openResult    migrationStatusOpenResult
	openCalls     int
	acquisitions  int
	linkedReports []linked.Report
	wires         [][]byte
	afterRunner   func()
	err           error
	trace         *migrationStatusEventTrace
}

func (owner *migrationStatusProcessBackend) Execute(
	ctx context.Context,
	_ <-chan struct{},
	stage productcheck.ProcessStage,
	command productcheck.Command,
) productcheck.ProcessResult {
	switch stage {
	case productcheck.BuildStage:
		owner.trace.record("build")
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case productcheck.ShowMigrationsRunnerStage:
		owner.trace.record("show_runner")
		if len(command.Argv) != 2 || command.Argv[1] != showmigrationsprotocol.PrivateArgument ||
			!bytes.Equal(command.Stdin, showmigrationsprotocol.RequestDocument()) {
			owner.err = errors.New("invalid migration-status runner command")
			return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
		}
		wire := &migrationStatusWriter{trace: owner.trace, event: "response_publication"}
		report, err := linked.RunShowMigrations(
			ctx,
			linked.ShowMigrationsConfig{
				ProjectRoot:                owner.project.root,
				MigrationDefinitionSources: cloneMigrationStatusSources(owner.sources),
				OpenMigrationBackend: func(context.Context) (linked.MigrationBackend, error) {
					owner.openCalls++
					owner.trace.record("backend_open")
					if owner.openResult.database != nil {
						owner.acquisitions++
					}
					return owner.openResult.database, owner.openResult.err
				},
			},
			append([]string(nil), command.Argv[1:]...),
			bytes.NewReader(command.Stdin),
			wire,
		)
		document := append([]byte(nil), wire.buffer.Bytes()...)
		owner.linkedReports = append(owner.linkedReports, report)
		owner.wires = append(owner.wires, document)
		if err != nil {
			owner.err = err
			return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
		}
		if owner.afterRunner != nil {
			owner.afterRunner()
		}
		return productcheck.ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: document,
			StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(document)},
		}
	default:
		owner.err = fmt.Errorf("unexpected migration-status process stage %d", stage)
		return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	}
}

func cloneMigrationStatusSources(sources []definition.Source) []definition.Source {
	result := make([]definition.Source, len(sources))
	for index := range sources {
		result[index] = definition.Source{
			SourceID: sources[index].SourceID,
			Document: append([]byte(nil), sources[index].Document...),
		}
	}
	return result
}

type migrationStatusWriterMode uint8

const (
	migrationStatusWriteNormal migrationStatusWriterMode = iota
	migrationStatusWriteShort
	migrationStatusWriteError
)

type migrationStatusWriter struct {
	mode              migrationStatusWriterMode
	writes            int
	errors            int
	buffer            bytes.Buffer
	beforeFirstWrite  func() bool
	closedBeforeWrite bool
	trace             *migrationStatusEventTrace
	event             string
}

func (writer *migrationStatusWriter) Write(payload []byte) (int, error) {
	writer.writes++
	writer.trace.record(writer.event)
	if writer.writes == 1 && writer.beforeFirstWrite != nil {
		writer.closedBeforeWrite = writer.beforeFirstWrite()
	}
	switch writer.mode {
	case migrationStatusWriteNormal:
		return writer.buffer.Write(payload)
	case migrationStatusWriteShort:
		if len(payload) == 0 {
			return 0, nil
		}
		return writer.buffer.Write(payload[:len(payload)-1])
	case migrationStatusWriteError:
		writer.errors++
		return 0, errors.New(migrationStatusSecretCanary)
	default:
		writer.errors++
		return 0, errors.New("invalid migration-status writer mode")
	}
}

type migrationStatusExecution struct {
	report       productcheck.ShowMigrationsReport
	linkedReport linked.Report
	database     *migrationStatusDatabase
	backend      *migrationStatusProcessBackend
	stdout       *migrationStatusWriter
	stderr       *migrationStatusWriter
	wire         []byte
	trace        []string
}

type migrationStatusExecutionInput struct {
	args        []string
	sources     []definition.Source
	database    *migrationStatusDatabase
	openErr     error
	context     context.Context
	afterRunner func()
	stdoutMode  migrationStatusWriterMode
}

func runMigrationStatus(
	project *migrationStatusProject,
	input migrationStatusExecutionInput,
) (migrationStatusExecution, error) {
	ctx := input.context
	if ctx == nil {
		ctx = context.Background()
	}
	trace := &migrationStatusEventTrace{}
	if input.database != nil {
		input.database.trace = trace
		if input.database.session != nil {
			input.database.session.trace = trace
		}
	}
	owner := &migrationStatusProcessBackend{
		project: project, sources: cloneMigrationStatusSources(input.sources),
		openResult:  migrationStatusOpenResult{database: input.database, err: input.openErr},
		afterRunner: input.afterRunner,
		trace:       trace,
	}
	stdout := &migrationStatusWriter{mode: input.stdoutMode, trace: trace, event: "stdout_publication"}
	stderr := &migrationStatusWriter{trace: trace, event: "stderr_publication"}
	if input.database != nil {
		stdout.beforeFirstWrite = func() bool {
			return input.database.closeCalls == 1 && input.database.session.closeCalls == 1
		}
	}
	report := productcheck.RunShowMigrations(productcheck.ShowMigrationsInvocation{
		Context: ctx, CWD: project.root, Args: append([]string(nil), input.args...),
		Environment: append([]string(nil), project.environment...), Stdout: stdout, Stderr: stderr, Backend: owner,
	})
	if owner.err != nil {
		return migrationStatusExecution{}, owner.err
	}
	execution := migrationStatusExecution{
		report: report, database: input.database, backend: owner, stdout: stdout, stderr: stderr,
		trace: append([]string(nil), trace.events...),
	}
	if len(owner.linkedReports) > 0 {
		execution.linkedReport = owner.linkedReports[len(owner.linkedReports)-1]
	}
	if len(owner.wires) > 0 {
		execution.wire = append([]byte(nil), owner.wires[len(owner.wires)-1]...)
	}
	return execution, nil
}

func migrationStatusDefinitions() ([]definition.Source, error) {
	return migrationStatusEncode(
		migrations.Migration{
			App: "blog", Name: "0002_publish",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_article"}},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: "blog", ModelName: "article",
				Field: ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}},
			}},
		},
		migrations.Migration{
			App: "authors", Name: "0001_author",
			Operations: []migrations.Operation{migrationStatusCreateModel("authors", "author", "Author")},
		},
		migrations.Migration{
			App: "blog", Name: "0001_article",
			Dependencies: []migrations.MigrationKey{{App: "authors", Name: "0001_author"}},
			Operations:   []migrations.Operation{migrationStatusCreateModel("blog", "article", "Article")},
		},
	)
}

func migrationStatusBranchDefinitions() ([]definition.Source, error) {
	return migrationStatusEncode(
		migrations.Migration{
			App: "alpha", Name: "0001_child",
			Dependencies: []migrations.MigrationKey{{App: "alpha", Name: "0099_parent"}},
			Operations:   []migrations.Operation{migrationStatusCreateModel("alpha", "child", "Child")},
		},
		migrations.Migration{
			App: "zeta", Name: "0001_root",
			Operations: []migrations.Operation{migrationStatusCreateModel("zeta", "root", "Root")},
		},
		migrations.Migration{
			App: "alpha", Name: "0099_parent",
			Dependencies: []migrations.MigrationKey{{App: "zeta", Name: "0001_root"}},
			Operations:   []migrations.Operation{migrationStatusCreateModel("alpha", "parent", "Parent")},
		},
	)
}

func migrationStatusCreateModel(app, name, goName string) migrations.Operation {
	return migrations.CreateModel{
		AppLabel: app,
		Model: ir.Model{Name: name, GoName: goName, DBTable: app + "_" + name, Fields: []ir.Field{{
			Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
		}}},
	}
}

func migrationStatusEncode(values ...migrations.Migration) ([]definition.Source, error) {
	sources := make([]definition.Source, len(values))
	for index, migration := range values {
		document, err := definition.Encode(definition.Producer{Name: "godj-migration-status-actual", Version: "1"}, migration)
		if err != nil {
			return nil, err
		}
		sources[index] = definition.Source{SourceID: fmt.Sprintf("actual-%02d", index), Document: document}
	}
	return sources, nil
}

func migrationStatusRows(rows []showmigrationsprotocol.Row) protocol.Value {
	values := make([]protocol.Value, len(rows))
	for index, row := range rows {
		values[index] = protocol.Object(map[string]protocol.Value{
			"app": protocol.String(row.App), "name": protocol.String(row.Name), "status": protocol.String(row.Status),
		})
	}
	return protocol.List(values...)
}

func migrationStatusKeys(keys []backend.AppliedMigration) protocol.Value {
	values := make([]protocol.Value, len(keys))
	for index, key := range keys {
		values[index] = protocol.Object(map[string]protocol.Value{
			"app": protocol.String(key.App), "name": protocol.String(key.Name),
		})
	}
	return protocol.List(values...)
}

func migrationStatusApps(rows []showmigrationsprotocol.Row) protocol.Value {
	apps := make([]protocol.Value, 0)
	previous := ""
	for _, row := range rows {
		if row.App == previous {
			continue
		}
		apps = append(apps, protocol.String(row.App))
		previous = row.App
	}
	return protocol.List(apps...)
}

func migrationStatusInt(value int) protocol.Value { return protocol.Integer(strconv.Itoa(value)) }

func migrationStatusMutationDBState(database *migrationStatusDatabase, history string) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"application_mutations": migrationStatusInt(database.applicationMutations),
		"history":               protocol.String(history),
		"recorder_mutations":    migrationStatusInt(database.recorderMutations),
		"revision_mutations":    migrationStatusInt(database.revisionMutations),
		"schema_mutations":      migrationStatusInt(database.schemaMutations),
	})
}

func migrationStatusStandardMetrics(execution migrationStatusExecution, knownRows, unknownRows *int) protocol.Value {
	fields := map[string]protocol.Value{
		"application_mutations":   migrationStatusInt(execution.database.applicationMutations),
		"applied_history_reads":   migrationStatusInt(execution.database.session.readCalls),
		"backend_closes":          migrationStatusInt(execution.database.closeCalls),
		"backend_opens":           migrationStatusInt(execution.backend.openCalls),
		"recorder_mutations":      migrationStatusInt(execution.database.recorderMutations),
		"revision_mutations":      migrationStatusInt(execution.database.revisionMutations),
		"revision_session_closes": migrationStatusInt(execution.database.session.closeCalls),
		"revision_session_opens":  migrationStatusInt(execution.database.openSessionCalls),
		"schema_mutations":        migrationStatusInt(execution.database.schemaMutations),
		"stdout_writes":           migrationStatusInt(execution.stdout.writes),
	}
	if knownRows != nil {
		fields["known_rows"] = migrationStatusInt(*knownRows)
	}
	if unknownRows != nil {
		fields["unknown_rows"] = migrationStatusInt(*unknownRows)
	}
	return protocol.Object(fields)
}

func migrationStatusObservation(contract protocol.Contract, result, dbState, metrics *protocol.Value) protocol.Observation {
	return protocol.Observation{
		ID: contract.ID, Status: protocol.StatusObserved, Phase: contract.Phase,
		Result: result, DBState: dbState, Metrics: metrics,
	}
}

func migrationStatusRequireSuccess(execution migrationStatusExecution) error {
	if execution.report.ExitCode != 0 || !execution.report.HasShowMigrationsResult || execution.report.HasShowMigrationsFailure ||
		execution.stdout.writes != 1 || execution.stderr.writes != 0 || execution.linkedReport.LoadCalls != 1 ||
		execution.backend.openCalls != 1 || execution.database == nil || execution.database.openSessionCalls != 1 ||
		execution.database.session.readCalls != 1 || execution.database.session.beginCalls != 0 ||
		execution.database.session.closeCalls != 1 || execution.database.closeCalls != 1 {
		return fmt.Errorf("migration-status success boundary = report:%+v linked:%+v backend:%+v", execution.report, execution.linkedReport, execution.database)
	}
	return nil
}

func migrationStatusEmptyCatalog(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	project, err := newMigrationStatusProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	database := newMigrationStatusDatabase(nil)
	execution, err := runMigrationStatus(&project, migrationStatusExecutionInput{
		args: []string{"showmigrations"}, database: database, context: ctx,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationStatusRequireSuccess(execution); err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"point_in_time_snapshot": protocol.Boolean(execution.stdout.closedBeforeWrite),
		"rows":                   migrationStatusRows(execution.report.ShowMigrationsResult.Rows),
		"stdout":                 protocol.String(execution.stdout.buffer.String()),
	})
	dbState := migrationStatusMutationDBState(database, func() string {
		if len(database.session.records) == 0 {
			return "empty"
		}
		return "nonempty"
	}())
	metrics := migrationStatusStandardMetrics(execution, nil, nil)
	return migrationStatusObservation(contract, &result, &dbState, &metrics), nil
}

func migrationStatusFreshUnapplied(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	sources, err := migrationStatusDefinitions()
	if err != nil {
		return protocol.Observation{}, err
	}
	return migrationStatusSimpleListing(ctx, contract, sources, nil)
}

func migrationStatusAppliedPrefix(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	sources, err := migrationStatusDefinitions()
	if err != nil {
		return protocol.Observation{}, err
	}
	records := []backend.AppliedMigration{{App: "authors", Name: "0001_author"}, {App: "blog", Name: "0001_article"}}
	return migrationStatusSimpleListing(ctx, contract, sources, records)
}

func migrationStatusSimpleListing(
	ctx context.Context,
	contract protocol.Contract,
	sources []definition.Source,
	records []backend.AppliedMigration,
) (observation protocol.Observation, resultErr error) {
	project, err := newMigrationStatusProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	database := newMigrationStatusDatabase(records)
	execution, err := runMigrationStatus(&project, migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: database, context: ctx,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationStatusRequireSuccess(execution); err != nil {
		return protocol.Observation{}, err
	}
	rows := execution.report.ShowMigrationsResult.Rows
	result := protocol.Object(map[string]protocol.Value{
		"app_order": migrationStatusApps(rows), "rows": migrationStatusRows(rows),
		"stdout": protocol.String(execution.stdout.buffer.String()),
	})
	return migrationStatusObservation(contract, &result, nil, nil), nil
}

func migrationStatusFullyAppliedRestart(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	sources, err := migrationStatusDefinitions()
	if err != nil {
		return protocol.Observation{}, err
	}
	records := []backend.AppliedMigration{
		{App: "authors", Name: "0001_author"}, {App: "blog", Name: "0001_article"}, {App: "blog", Name: "0002_publish"},
	}
	project, err := newMigrationStatusProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	first, err := runMigrationStatus(&project, migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(records), context: ctx,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	secondDatabase := newMigrationStatusDatabase(records)
	second, err := runMigrationStatus(&project, migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: secondDatabase, context: ctx,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationStatusRequireSuccess(first); err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationStatusRequireSuccess(second); err != nil {
		return protocol.Observation{}, err
	}
	firstRows := first.report.ShowMigrationsResult.Rows
	secondRows := second.report.ShowMigrationsResult.Rows
	identical := bytes.Equal(first.stdout.buffer.Bytes(), second.stdout.buffer.Bytes()) && reflect.DeepEqual(firstRows, secondRows) && first.database != second.database
	result := protocol.Object(map[string]protocol.Value{
		"app_order": migrationStatusApps(firstRows), "first_rows": migrationStatusRows(firstRows),
		"first_stdout": protocol.String(first.stdout.buffer.String()),
		"independent_observations_byte_identical": protocol.Boolean(identical),
		"second_app_order":                        migrationStatusApps(secondRows), "second_rows": migrationStatusRows(secondRows),
		"second_stdout": protocol.String(second.stdout.buffer.String()),
	})
	return migrationStatusObservation(contract, &result, nil, nil), nil
}

func migrationStatusCrossAppBranchOrder(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	sources, err := migrationStatusBranchDefinitions()
	if err != nil {
		return protocol.Observation{}, err
	}
	project, err := newMigrationStatusProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	execution, err := runMigrationStatus(&project, migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(nil), context: ctx,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationStatusRequireSuccess(execution); err != nil {
		return protocol.Observation{}, err
	}
	rows := execution.report.ShowMigrationsResult.Rows
	positions := make(map[migrations.MigrationKey]int, len(rows))
	for index, row := range rows {
		positions[migrations.MigrationKey{App: row.App, Name: row.Name}] = index
	}
	parent := migrations.MigrationKey{App: "alpha", Name: "0099_parent"}
	child := migrations.MigrationKey{App: "alpha", Name: "0001_child"}
	root := migrations.MigrationKey{App: "zeta", Name: "0001_root"}
	perAppValid := positions[parent] < positions[child]
	labelBeforeCrossAppDependency := positions[parent] < positions[root]
	result := protocol.Object(map[string]protocol.Value{
		"app_order": migrationStatusApps(rows),
		"dependency_order_precedes_lexicographic_name":    protocol.Boolean(perAppValid && child.Name < parent.Name),
		"global_topological_order_claimed":                protocol.Boolean(!labelBeforeCrossAppDependency),
		"label_grouping_can_precede_cross_app_dependency": protocol.Boolean(labelBeforeCrossAppDependency),
		"per_app_dependency_valid":                        protocol.Boolean(perAppValid),
		"rows":                                            migrationStatusRows(rows),
		"same_app_dependencies": protocol.List(protocol.Object(map[string]protocol.Value{
			"child": protocol.String(child.Name), "parent": protocol.String(parent.Name),
		})),
		"stdout": protocol.String(execution.stdout.buffer.String()),
	})
	return migrationStatusObservation(contract, &result, nil, nil), nil
}

func migrationStatusUnknownRecordVisible(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	sources, err := migrationStatusDefinitions()
	if err != nil {
		return protocol.Observation{}, err
	}
	unknownInput := []backend.AppliedMigration{
		{App: "blog", Name: "9999_removed"},
		{App: "blog", Name: "0000_removed"},
		{App: "legacy", Name: "0001_gone"},
	}
	records := append([]backend.AppliedMigration{
		{App: "authors", Name: "0001_author"}, {App: "blog", Name: "0001_article"},
	}, unknownInput...)
	project, err := newMigrationStatusProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	database := newMigrationStatusDatabase(records)
	execution, err := runMigrationStatus(&project, migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: database, context: ctx,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationStatusRequireSuccess(execution); err != nil {
		return protocol.Observation{}, err
	}
	rows := execution.report.ShowMigrationsResult.Rows
	loaded, _, err := definition.Load(cloneMigrationStatusSources(sources)...)
	if err != nil {
		return protocol.Observation{}, err
	}
	knownKeys := make(map[migrations.MigrationKey]struct{})
	for _, migration := range loaded.Definitions() {
		knownKeys[migration.Key()] = struct{}{}
	}
	recordedUnknown := make([]backend.AppliedMigration, 0, len(unknownInput))
	appliedKnown := make(map[migrations.MigrationKey]struct{}, len(knownKeys))
	for _, record := range database.session.records {
		key := migrations.MigrationKey{App: record.App, Name: record.Name}
		if _, known := knownKeys[key]; !known {
			recordedUnknown = append(recordedUnknown, record)
		} else {
			appliedKnown[key] = struct{}{}
		}
	}
	if len(recordedUnknown) != len(unknownInput) || recordedUnknown[0].Name <= recordedUnknown[1].Name || !reflect.DeepEqual(recordedUnknown, unknownInput) {
		return protocol.Observation{}, fmt.Errorf("migration-status unknown input did not retain an unsorted actual snapshot: %v", recordedUnknown)
	}
	known, unknown := 0, 0
	knownRowsPreserved := len(knownKeys) == 3
	unknownOnlyVisible := false
	unknownNames := make(map[string][]string)
	observedKnown := make(map[migrations.MigrationKey]string, len(knownKeys))
	for _, row := range rows {
		if row.Status == showmigrationsprotocol.StatusUnknown {
			unknown++
			unknownNames[row.App] = append(unknownNames[row.App], row.Name)
			if row.App == "legacy" {
				unknownOnlyVisible = true
			}
			continue
		}
		known++
		observedKnown[migrations.MigrationKey{App: row.App, Name: row.Name}] = row.Status
	}
	for key := range knownKeys {
		status, exists := observedKnown[key]
		_, applied := appliedKnown[key]
		wantStatus := showmigrationsprotocol.StatusUnapplied
		if applied {
			wantStatus = showmigrationsprotocol.StatusApplied
		}
		if !exists || status != wantStatus {
			knownRowsPreserved = false
		}
	}
	if known != len(knownKeys) {
		knownRowsPreserved = false
	}
	unknownSorted := true
	for _, names := range unknownNames {
		if !sort.StringsAreSorted(names) {
			unknownSorted = false
		}
	}
	result := protocol.Object(map[string]protocol.Value{
		"known_rows_preserved":         protocol.Boolean(knownRowsPreserved),
		"recorded_unknown_input_order": migrationStatusKeys(recordedUnknown),
		"rows":                         migrationStatusRows(rows),
		"stdout":                       protocol.String(execution.stdout.buffer.String()),
		"unknown_only_apps_visible":    protocol.Boolean(unknownOnlyVisible),
		"unknown_rows_fail_visible":    protocol.Boolean(unknown == len(unknownInput)),
		"unknown_tail_names_sorted":    protocol.Boolean(unknownSorted),
	})
	dbState := migrationStatusMutationDBState(database, func() string {
		if known > 0 && unknown > 0 {
			return "known_and_valid_unknown_records"
		}
		return "unexpected"
	}())
	metrics := migrationStatusStandardMetrics(execution, &known, &unknown)
	return migrationStatusObservation(contract, &result, &dbState, &metrics), nil
}

func migrationStatusInconsistentKnownHistory(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	sources, err := migrationStatusDefinitions()
	if err != nil {
		return protocol.Observation{}, err
	}
	records := []backend.AppliedMigration{{App: "blog", Name: "0002_publish"}}
	project, err := newMigrationStatusProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	database := newMigrationStatusDatabase(records)
	execution, err := runMigrationStatus(&project, migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: database, context: ctx,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if execution.report.ExitCode != 1 || !execution.report.HasShowMigrationsFailure ||
		execution.report.ShowMigrationsFailure.Category != showmigrationsprotocol.CategoryHistory ||
		execution.report.ShowMigrationsFailure.Code != string(migrations.CodeInconsistentAppliedHistory) ||
		execution.stdout.writes != 0 || database.session.beginCalls != 0 || database.session.readCalls != 1 ||
		database.session.closeCalls != 1 || database.closeCalls != 1 || !reflect.DeepEqual(records, database.session.records) {
		return protocol.Observation{}, fmt.Errorf("migration-status inconsistent history boundary = report:%+v database:%+v", execution.report, database)
	}
	dbState := migrationStatusMutationDBState(database, "preserved_inconsistent_known_history")
	messageIsContract := false
	observedError := &protocol.ObservedError{
		Category:          execution.report.ShowMigrationsFailure.Category,
		Code:              execution.report.ShowMigrationsFailure.Code,
		MessageIsContract: &messageIsContract,
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"application_mutations":   migrationStatusInt(database.applicationMutations),
		"applied_history_reads":   migrationStatusInt(database.session.readCalls),
		"backend_closes":          migrationStatusInt(database.closeCalls),
		"backend_opens":           migrationStatusInt(execution.backend.openCalls),
		"migration_begins":        migrationStatusInt(database.session.beginCalls),
		"recorder_mutations":      migrationStatusInt(database.recorderMutations),
		"revision_mutations":      migrationStatusInt(database.revisionMutations),
		"revision_session_closes": migrationStatusInt(database.session.closeCalls),
		"revision_session_opens":  migrationStatusInt(database.openSessionCalls),
		"schema_mutations":        migrationStatusInt(database.schemaMutations),
		"stderr_writes":           migrationStatusInt(execution.stderr.writes),
		"stdout_writes":           migrationStatusInt(execution.stdout.writes),
	})
	observation = migrationStatusObservation(contract, nil, &dbState, &metrics)
	observation.Error = observedError
	return observation, nil
}

type migrationStatusBoundaryCase struct {
	name                       string
	execution                  migrationStatusExecution
	automaticRetries           int
	terminalPublicationFailure bool
	revisionFenceFailure       bool
	secretProtocolOccurrences  int
	secretStdoutOccurrences    int
	secretStderrOccurrences    int
}

var migrationStatusBoundaryCaseOrder = []string{
	"invalid_arguments", "invalid_definition", "pre_acquisition_cancel", "success",
	"partial_backend_acquisition", "partial_session_acquisition", "history_read_failure",
	"revision_fence_adoption_required", "stale_history_revision", "history_revision_contended",
	"history_revision_integrity", "session_close_failure", "outer_close_failure",
	"closed_snapshot_then_cancel", "terminal_stdout_short_write", "terminal_stdout_error",
}

func migrationStatusBoundaryCaseValue(current migrationStatusBoundaryCase) protocol.Value {
	execution := current.execution
	database := execution.database
	backendCloses, sessionOpenCalls, sessionAcquisitions, sessionCloses, historyReads, migrationBegins := 0, 0, 0, 0, 0, 0
	if database != nil {
		backendCloses = database.closeCalls
		sessionOpenCalls = database.openSessionCalls
		sessionAcquisitions = database.sessionAcquisitions
		if database.session != nil {
			sessionCloses = database.session.closeCalls
			historyReads = database.session.readCalls
			migrationBegins = database.session.beginCalls
		}
	}
	category, code := protocol.Null(), protocol.Null()
	outcome := "success"
	if execution.report.HasShowMigrationsFailure {
		category = protocol.String(execution.report.ShowMigrationsFailure.Category)
		code = protocol.String(execution.report.ShowMigrationsFailure.Code)
		outcome = "error"
	}
	stderrRepublications := execution.stderr.writes
	if stderrRepublications > 0 {
		stderrRepublications--
	}
	return protocol.Object(map[string]protocol.Value{
		"automatic_retries":     migrationStatusInt(current.automaticRetries),
		"backend_acquisitions":  migrationStatusInt(execution.backend.acquisitions),
		"backend_closes":        migrationStatusInt(backendCloses),
		"backend_open_calls":    migrationStatusInt(execution.backend.openCalls),
		"build_calls":           migrationStatusInt(execution.report.BuildCalls),
		"category":              category,
		"cleanup_failed":        protocol.Boolean(execution.report.CleanupFailed != 0 || (execution.report.HasShowMigrationsFailure && execution.report.ShowMigrationsFailure.CleanupFailed)),
		"code":                  code,
		"definition_loads":      migrationStatusInt(execution.linkedReport.LoadCalls),
		"exit_code":             migrationStatusInt(execution.report.ExitCode),
		"history_reads":         migrationStatusInt(historyReads),
		"migration_begins":      migrationStatusInt(migrationBegins),
		"name":                  protocol.String(current.name),
		"outcome":               protocol.String(outcome),
		"partial_stdout_writes": migrationStatusInt(execution.report.PartialStdoutWrites),
		"project_selections":    migrationStatusInt(execution.report.DescriptorReads),
		"session_acquisitions":  migrationStatusInt(sessionAcquisitions),
		"session_closes":        migrationStatusInt(sessionCloses),
		"session_open_calls":    migrationStatusInt(sessionOpenCalls),
		"snapshot_published":    protocol.Boolean(execution.report.ExitCode == 0 && execution.report.HasShowMigrationsResult),
		"stderr_republications": migrationStatusInt(stderrRepublications),
		"stdout_write_attempts": migrationStatusInt(execution.stdout.writes),
		"stdout_write_errors":   migrationStatusInt(execution.stdout.errors),
	})
}

func migrationStatusVerifyBoundaryOrder(cases []migrationStatusBoundaryCase) error {
	if len(cases) != len(migrationStatusBoundaryCaseOrder) {
		return fmt.Errorf("migration-status boundary produced %d cases; want %d", len(cases), len(migrationStatusBoundaryCaseOrder))
	}
	seen := make(map[string]struct{}, len(cases))
	for index, want := range migrationStatusBoundaryCaseOrder {
		if cases[index].name != want {
			return fmt.Errorf("migration-status boundary case %d = %q; want %q", index, cases[index].name, want)
		}
		if _, duplicate := seen[cases[index].name]; duplicate {
			return fmt.Errorf("migration-status boundary case %q is duplicated", cases[index].name)
		}
		seen[cases[index].name] = struct{}{}
	}
	return nil
}

func migrationStatusFailurePrecedence(cases []migrationStatusBoundaryCase) ([]string, error) {
	if err := migrationStatusVerifyBoundaryOrder(cases); err != nil {
		return nil, err
	}
	invalidArguments := cases[0].execution
	invalidDefinition := cases[1].execution
	success := cases[3].execution
	partialBackend := cases[4].execution
	partialSession := cases[5].execution
	historyRead := cases[6].execution
	sessionClose := cases[11].execution
	backendClose := cases[12].execution
	shortWrite := cases[14].execution
	errorWrite := cases[15].execution
	fullLifecycle := []string{"revision_session_open", "history_read", "revision_session_close", "backend_close"}
	partialSessionLifecycle := []string{"revision_session_open", "revision_session_close", "backend_close"}
	partialBackendLifecycle := []string{"backend_close"}
	lifecycle := func(execution migrationStatusExecution) []string {
		result := make([]string, 0, len(execution.trace))
		for _, event := range execution.trace {
			switch event {
			case "revision_session_open", "history_read", "migration_begin", "revision_session_close", "backend_close":
				result = append(result, event)
			}
		}
		return result
	}
	lastEvent := func(execution migrationStatusExecution) string {
		if len(execution.trace) == 0 {
			return ""
		}
		return execution.trace[len(execution.trace)-1]
	}
	responsePublication := func(execution migrationStatusExecution, requireBackendClose bool) ([]string, error) {
		closeIndex, responseIndex, publicIndex := -1, -1, -1
		responseCount, publicCount := 0, 0
		for index, event := range execution.trace {
			switch event {
			case "backend_close":
				closeIndex = index
			case "response_publication":
				responseIndex = index
				responseCount++
			case "stdout_publication", "stderr_publication":
				publicIndex = index
				publicCount++
			}
		}
		if responseCount != 1 || publicCount != 1 || responseIndex >= publicIndex {
			return nil, fmt.Errorf("migration-status response trace = %v", execution.trace)
		}
		if requireBackendClose && (closeIndex < 0 || closeIndex >= responseIndex) {
			return nil, fmt.Errorf("migration-status cleanup did not precede private response: %v", execution.trace)
		}
		return []string{"response_publication"}, nil
	}
	if invalidArguments.report.BuildCalls != 0 || invalidArguments.report.DescriptorReads != 0 ||
		invalidArguments.linkedReport.LoadCalls != 0 || invalidArguments.backend.openCalls != 0 {
		return nil, errors.New("migration-status argument validation did not precede project or backend work")
	}
	if invalidDefinition.report.BuildCalls != 1 || invalidDefinition.report.DescriptorReads != 1 ||
		invalidDefinition.linkedReport.LoadCalls != 1 || invalidDefinition.backend.openCalls != 0 {
		return nil, errors.New("migration-status definition load did not precede backend open")
	}
	if partialBackend.backend.openCalls != 1 || partialBackend.backend.acquisitions != 1 ||
		partialBackend.database.closeCalls != 1 || partialBackend.database.openSessionCalls != 0 ||
		!reflect.DeepEqual(lifecycle(partialBackend), partialBackendLifecycle) {
		return nil, errors.New("migration-status backend-open cleanup boundary drifted")
	}
	if partialSession.database.openSessionCalls != 1 || partialSession.database.sessionAcquisitions != 1 ||
		partialSession.database.session.readCalls != 0 || partialSession.database.session.closeCalls != 1 ||
		partialSession.database.closeCalls != 1 || !reflect.DeepEqual(lifecycle(partialSession), partialSessionLifecycle) {
		return nil, errors.New("migration-status revision-session-open cleanup boundary drifted")
	}
	if historyRead.database.session.readCalls != 1 || historyRead.database.session.closeCalls != 1 || historyRead.database.closeCalls != 1 ||
		!reflect.DeepEqual(lifecycle(historyRead), fullLifecycle) {
		return nil, errors.New("migration-status history-read cleanup boundary drifted")
	}
	if !sessionClose.report.ShowMigrationsFailure.CleanupFailed || sessionClose.database.session.closeCalls != 1 || sessionClose.database.closeCalls != 1 ||
		!reflect.DeepEqual(lifecycle(sessionClose), fullLifecycle) {
		return nil, errors.New("migration-status revision-session-close failure boundary drifted")
	}
	if !backendClose.report.ShowMigrationsFailure.CleanupFailed || backendClose.database.session.closeCalls != 1 || backendClose.database.closeCalls != 1 ||
		!reflect.DeepEqual(lifecycle(backendClose), fullLifecycle) {
		return nil, errors.New("migration-status backend-close failure boundary drifted")
	}
	if shortWrite.stdout.writes != 1 || shortWrite.report.PartialStdoutWrites != 1 || shortWrite.report.ExitCode != 3 ||
		errorWrite.stdout.writes != 1 || errorWrite.stdout.errors != 1 || errorWrite.report.ExitCode != 3 {
		return nil, errors.New("migration-status response-publication failure boundary drifted")
	}
	for index := range cases {
		execution := cases[index].execution
		if cases[index].terminalPublicationFailure {
			if execution.stdout.writes != 1 || execution.stderr.writes != 0 || lastEvent(execution) != "stdout_publication" {
				return nil, fmt.Errorf("migration-status terminal failure %q publication boundary drifted", cases[index].name)
			}
			continue
		}
		if execution.report.HasShowMigrationsFailure {
			if execution.stdout.writes != 0 || execution.stderr.writes != 1 || lastEvent(execution) != "stderr_publication" {
				return nil, fmt.Errorf("migration-status logical failure %q publication boundary drifted", cases[index].name)
			}
			continue
		}
		if execution.stdout.writes != 1 || execution.stderr.writes != 0 || lastEvent(execution) != "stdout_publication" {
			return nil, fmt.Errorf("migration-status successful %q publication boundary drifted", cases[index].name)
		}
	}
	if _, err := responsePublication(invalidDefinition, false); err != nil {
		return nil, fmt.Errorf("migration-status definition failure response boundary drifted: %w", err)
	}
	for _, index := range []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15} {
		if _, err := responsePublication(cases[index].execution, true); err != nil {
			return nil, fmt.Errorf("migration-status response boundary %q drifted: %w", cases[index].name, err)
		}
	}
	for _, index := range []int{3, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15} {
		if !reflect.DeepEqual(lifecycle(cases[index].execution), fullLifecycle) {
			return nil, fmt.Errorf("migration-status full lifecycle %q = %v, want %v", cases[index].name, lifecycle(cases[index].execution), fullLifecycle)
		}
	}
	precedence := []string{"argument_validation", "definition_load", "backend_open"}
	precedence = append(precedence, lifecycle(success)...)
	responseEvents, err := responsePublication(success, true)
	if err != nil {
		return nil, err
	}
	precedence = append(precedence, responseEvents...)
	return precedence, nil
}

func migrationStatusStringList(values []string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = protocol.String(value)
	}
	return protocol.List(items...)
}

func migrationStatusProjectBoundary(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	project, err := newMigrationStatusProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	sources, err := migrationStatusDefinitions()
	if err != nil {
		return protocol.Observation{}, err
	}
	private := errors.New(migrationStatusSecretCanary)
	var cases []migrationStatusBoundaryCase
	run := func(name string, input migrationStatusExecutionInput) error {
		execution, runErr := runMigrationStatus(&project, input)
		if runErr != nil {
			return fmt.Errorf("migration-status boundary %s: %w", name, runErr)
		}
		automaticRetries := execution.report.RunnerCalls - 1
		if automaticRetries < 0 {
			automaticRetries = 0
		}
		cases = append(cases, migrationStatusBoundaryCase{
			name: name, execution: execution, automaticRetries: automaticRetries,
			secretProtocolOccurrences: bytes.Count(execution.wire, []byte(migrationStatusSecretCanary)),
			secretStdoutOccurrences:   bytes.Count(execution.stdout.buffer.Bytes(), []byte(migrationStatusSecretCanary)),
			secretStderrOccurrences:   bytes.Count(execution.stderr.buffer.Bytes(), []byte(migrationStatusSecretCanary)),
		})
		return nil
	}
	if err := run("invalid_arguments", migrationStatusExecutionInput{
		args: []string{"showmigrations", "--plan"}, sources: sources, database: newMigrationStatusDatabase(nil), context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	if err := run("invalid_definition", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: []definition.Source{{SourceID: "private", Document: []byte(`{"password":"` + migrationStatusSecretCanary + `"}`)}},
		database: newMigrationStatusDatabase(nil), context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := run("pre_acquisition_cancel", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(nil), context: canceled,
	}); err != nil {
		return protocol.Observation{}, err
	}
	if err := run("success", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(nil), context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	if err := run("partial_backend_acquisition", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(nil), openErr: private, context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	partialSession := newMigrationStatusDatabase(nil)
	partialSession.openSessionErr = private
	if err := run("partial_session_acquisition", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: partialSession, context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	historyFailure := newMigrationStatusDatabase(nil)
	historyFailure.session.readErr = private
	if err := run("history_read_failure", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: historyFailure, context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	fenceCases := []struct {
		name string
		kind backend.RevisionFenceFailureKind
	}{
		{name: "revision_fence_adoption_required", kind: backend.RevisionFenceFailureAdoptionRequired},
		{name: "stale_history_revision", kind: backend.RevisionFenceFailureStale},
		{name: "history_revision_contended", kind: backend.RevisionFenceFailureContended},
		{name: "history_revision_integrity", kind: backend.RevisionFenceFailureIntegrity},
	}
	for _, fault := range fenceCases {
		database := newMigrationStatusDatabase(nil)
		database.session.readErr = &backend.RevisionFenceError{Kind: fault.kind, Cause: private}
		if err := run(fault.name, migrationStatusExecutionInput{
			args: []string{"showmigrations"}, sources: sources, database: database, context: ctx,
		}); err != nil {
			return protocol.Observation{}, err
		}
		cases[len(cases)-1].revisionFenceFailure = true
	}
	sessionClose := newMigrationStatusDatabase(nil)
	sessionClose.session.closeErr = private
	if err := run("session_close_failure", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: sessionClose, context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	outerClose := newMigrationStatusDatabase(nil)
	outerClose.closeErr = private
	if err := run("outer_close_failure", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: outerClose, context: ctx,
	}); err != nil {
		return protocol.Observation{}, err
	}
	closedContext, closedCancel := context.WithCancel(ctx)
	defer closedCancel()
	if err := run("closed_snapshot_then_cancel", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(nil),
		context: closedContext, afterRunner: closedCancel,
	}); err != nil {
		return protocol.Observation{}, err
	}
	if !errors.Is(closedContext.Err(), context.Canceled) {
		return protocol.Observation{}, errors.New("migration-status closed-snapshot cancellation callback did not run")
	}
	if err := run("terminal_stdout_short_write", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(nil), context: ctx,
		stdoutMode: migrationStatusWriteShort,
	}); err != nil {
		return protocol.Observation{}, err
	}
	cases[len(cases)-1].terminalPublicationFailure = true
	if err := run("terminal_stdout_error", migrationStatusExecutionInput{
		args: []string{"showmigrations"}, sources: sources, database: newMigrationStatusDatabase(nil), context: ctx,
		stdoutMode: migrationStatusWriteError,
	}); err != nil {
		return protocol.Observation{}, err
	}
	cases[len(cases)-1].terminalPublicationFailure = true
	if err := migrationStatusVerifyBoundaryOrder(cases); err != nil {
		return protocol.Observation{}, err
	}
	precedence, err := migrationStatusFailurePrecedence(cases)
	if err != nil {
		return protocol.Observation{}, err
	}

	caseValues := make([]protocol.Value, len(cases))
	cleanupFailures, fenceFailures, successfulSnapshots, terminalFailures := 0, 0, 0, 0
	protocolSecrets, stdoutSecrets, stderrSecrets, artifactSecrets := 0, 0, 0, 0
	applicationMutations, recorderMutations, revisionMutations, schemaMutations := 0, 0, 0, 0
	allPreserveSchema, snapshotsClosedBeforePublication := true, true
	for index := range cases {
		caseValues[index] = migrationStatusBoundaryCaseValue(cases[index])
		execution := cases[index].execution
		reportArtifact, marshalErr := json.Marshal(struct {
			Name   string                            `json:"name"`
			Global productcheck.ShowMigrationsReport `json:"global"`
			Linked linked.Report                     `json:"linked"`
			Case   protocol.Value                    `json:"case"`
		}{
			Name: cases[index].name, Global: execution.report,
			Linked: execution.linkedReport, Case: caseValues[index],
		})
		if marshalErr != nil {
			return protocol.Observation{}, fmt.Errorf("marshal migration-status actual report artifact %s: %w", cases[index].name, marshalErr)
		}
		artifactSecrets += bytes.Count(reportArtifact, []byte(migrationStatusSecretCanary))
		cleanupFailed := execution.report.CleanupFailed != 0 || (execution.report.HasShowMigrationsFailure && execution.report.ShowMigrationsFailure.CleanupFailed)
		if cleanupFailed {
			cleanupFailures++
		}
		if cases[index].revisionFenceFailure {
			fenceFailures++
		}
		if execution.report.ExitCode == 0 && execution.report.HasShowMigrationsResult {
			successfulSnapshots++
			snapshotsClosedBeforePublication = snapshotsClosedBeforePublication && execution.stdout.closedBeforeWrite
		}
		if cases[index].terminalPublicationFailure {
			terminalFailures++
		}
		protocolSecrets += cases[index].secretProtocolOccurrences
		stdoutSecrets += cases[index].secretStdoutOccurrences
		stderrSecrets += cases[index].secretStderrOccurrences
		if execution.database != nil {
			applicationMutations += execution.database.applicationMutations
			recorderMutations += execution.database.recorderMutations
			revisionMutations += execution.database.revisionMutations
			schemaMutations += execution.database.schemaMutations
			allPreserveSchema = allPreserveSchema && execution.database.session.beginCalls == 0
		}
	}
	allPreserveSchema = allPreserveSchema && applicationMutations == 0 && recorderMutations == 0 && revisionMutations == 0 && schemaMutations == 0
	failurePrecedence := migrationStatusStringList(precedence)
	caseList := protocol.List(caseValues...)
	result := protocol.Object(map[string]protocol.Value{
		"cases":                                 caseList,
		"closed_snapshot_survives_later_cancel": protocol.Boolean(cases[13].execution.report.ExitCode == 0 && cases[13].execution.report.HasShowMigrationsResult),
		"failure_precedence":                    failurePrecedence,
		"private_causes_published":              protocol.Boolean(protocolSecrets+stdoutSecrets+stderrSecrets+artifactSecrets != 0),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"all_cases_preserve_schema":                     protocol.Boolean(allPreserveSchema),
		"application_mutations":                         migrationStatusInt(applicationMutations),
		"recorder_mutations":                            migrationStatusInt(recorderMutations),
		"revision_mutations":                            migrationStatusInt(revisionMutations),
		"schema_mutations":                              migrationStatusInt(schemaMutations),
		"successful_snapshot_closed_before_publication": protocol.Boolean(snapshotsClosedBeforePublication),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"artifact_secret_occurrences":        migrationStatusInt(artifactSecrets),
		"cases":                              migrationStatusInt(len(cases)),
		"cleanup_failure_cases":              migrationStatusInt(cleanupFailures),
		"protocol_secret_occurrences":        migrationStatusInt(protocolSecrets),
		"revision_fence_failure_cases":       migrationStatusInt(fenceFailures),
		"stderr_secret_occurrences":          migrationStatusInt(stderrSecrets),
		"stdout_secret_occurrences":          migrationStatusInt(stdoutSecrets),
		"successful_snapshot_cases":          migrationStatusInt(successfulSnapshots),
		"terminal_publication_failure_cases": migrationStatusInt(terminalFailures),
	})
	observation = migrationStatusObservation(contract, &result, &dbState, &metrics)
	if err := observation.Validate(); err != nil {
		return protocol.Observation{}, fmt.Errorf("validate migration-status boundary observation: %w", err)
	}
	return observation, nil
}

var _ linked.MigrationBackend = (*migrationStatusDatabase)(nil)
