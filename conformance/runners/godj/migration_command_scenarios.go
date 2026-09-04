//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/conformance/runners/godj/migrationcommandworker"
	"github.com/progresshans/godj/db/sqlite"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const (
	migrationCommandApp         = "command"
	migrationCommandPrefix      = "0001_prefix"
	migrationCommandMiddle      = "0002_middle"
	migrationCommandTail        = "0003_tail"
	migrationCommandPrefixTable = "godj_command_prefix"
	migrationCommandMiddleTable = "godj_command_middle"
	migrationCommandTailTable   = "godj_command_tail"
)

type migrationCommandRegistration struct {
	id      string
	phase   protocol.Phase
	handler scenarioHandler
}

var migrationCommandScenarioRegistry = map[string]migrationCommandRegistration{
	"godj.migration.command.fresh_latest": {
		id: "MIG-087", phase: protocol.PhaseCommit, handler: migrationCommandFreshLatest,
	},
	"godj.migration.command.applied_prefix_tail": {
		id: "MIG-088", phase: protocol.PhaseCommit, handler: migrationCommandAppliedPrefixTail,
	},
	"godj.migration.command.fully_applied_fresh_noop": {
		id: "MIG-089", phase: protocol.PhaseCommit, handler: migrationCommandFullyAppliedNoop,
	},
	"godj.migration.command.definition_preflight_before_backend": {
		id: "MIG-090", phase: protocol.PhaseEvaluation, handler: migrationCommandDefinitionPreflight,
	},
	"godj.migration.command.inconsistent_history_preflight": {
		id: "MIG-091", phase: protocol.PhaseEvaluation, handler: migrationCommandInconsistentHistory,
	},
	"godj.migration.command.capability_preflight_before_begin": {
		id: "MIG-092", phase: protocol.PhaseEvaluation, handler: migrationCommandCapabilityPreflight,
	},
	"godj.migration.command.middle_failure_durable_prefix": {
		id: "MIG-093", phase: protocol.PhaseRollback, handler: migrationCommandMiddleFailure,
	},
	"godj.migration.command.fresh_resume_after_failure": {
		id: "MIG-094", phase: protocol.PhaseCommit, handler: migrationCommandFreshResume,
	},
	"godj.migration.command.commit_outcome_unknown": {
		id: "MIG-095", phase: protocol.PhaseCommit, handler: migrationCommandCommitUnknown,
	},
	"godj.migration.command.concurrent_latest_fenced": {
		id: "MIG-096", phase: protocol.PhaseCommit, handler: migrationCommandConcurrentLatest,
	},
	"godj.migration.command.backend_configuration_secret_boundary": {
		id: "MIG-097", phase: protocol.PhaseEnvironment, handler: migrationCommandBackendSecrets,
	},
	"godj.migration.command.interrupt_rollback_cleanup": {
		id: "MIG-098", phase: protocol.PhaseRollback, handler: migrationCommandInterruptCleanup,
	},
}

func migrationCommandScenarioHandler(scenario string) (scenarioHandler, bool) {
	registration, ok := migrationCommandScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		if contract.ID != registration.id {
			return protocol.Observation{}, fmt.Errorf("migration-command scenario %q contract id %q; want %q", scenario, contract.ID, registration.id)
		}
		if contract.Scenario != scenario {
			return protocol.Observation{}, fmt.Errorf("migration-command scenario %q contract scenario %q", scenario, contract.Scenario)
		}
		if contract.Phase != registration.phase {
			return protocol.Observation{}, fmt.Errorf("migration-command scenario %q phase %q; want %q", scenario, contract.Phase, registration.phase)
		}
		return registration.handler(ctx, contract)
	}, true
}

type migrationCommandProject struct {
	universe  string
	root      string
	workspace string
	home      string
	trace     string
}

func newMigrationCommandProject() (migrationCommandProject, error) {
	universe, err := os.MkdirTemp("", "godj-migration-command-actual-")
	if err != nil {
		return migrationCommandProject{}, fmt.Errorf("create migration-command project: %w", err)
	}
	root := filepath.Join(universe, "project")
	workspace := filepath.Join(universe, "workspace")
	home := filepath.Join(universe, "home")
	for _, directory := range []string{root, workspace, home} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return migrationCommandProject{}, errors.Join(fmt.Errorf("create migration-command directory: %w", err), os.RemoveAll(universe))
		}
	}
	document := []byte("format_version = 1\n[project]\npackage = \"./cmd/site\"\n")
	if err := os.WriteFile(filepath.Join(root, "godj.toml"), document, 0o600); err != nil {
		return migrationCommandProject{}, errors.Join(fmt.Errorf("write migration-command descriptor: %w", err), os.RemoveAll(universe))
	}
	return migrationCommandProject{universe: universe, root: root, workspace: workspace, home: home}, nil
}

func (project migrationCommandProject) close() error {
	return os.RemoveAll(project.universe)
}

type migrationCommandGlobalExecution struct {
	global   productcheck.MigrateReport
	linked   linked.Report
	stdout   []byte
	stderr   []byte
	wire     []byte
	stages   []productcheck.ProcessStage
	commands []productcheck.Command
	err      error
}

type migrationCommandProcessBackend struct {
	config linked.MigrateConfig

	mu       sync.Mutex
	linked   linked.Report
	wire     []byte
	stages   []productcheck.ProcessStage
	commands []productcheck.Command
	err      error

	runner func(context.Context, <-chan struct{}, productcheck.Command) productcheck.ProcessResult
}

func (backend *migrationCommandProcessBackend) Execute(
	ctx context.Context,
	interrupt <-chan struct{},
	stage productcheck.ProcessStage,
	command productcheck.Command,
) productcheck.ProcessResult {
	backend.mu.Lock()
	backend.stages = append(backend.stages, stage)
	backend.commands = append(backend.commands, productcheck.Command{
		Dir:   command.Dir,
		Argv:  append([]string(nil), command.Argv...),
		Env:   append([]string(nil), command.Env...),
		Stdin: append([]byte(nil), command.Stdin...),
	})
	backend.mu.Unlock()
	if stage == productcheck.BuildStage {
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	}
	if stage != productcheck.MigrateRunnerStage {
		backend.recordError(fmt.Errorf("migration-command process stage = %d", stage))
		return productcheck.ProcessResult{}
	}
	if backend.runner != nil {
		return backend.runner(ctx, interrupt, command)
	}
	var response bytes.Buffer
	report, err := linked.RunMigrate(
		ctx,
		backend.config,
		command.Argv[1:],
		bytes.NewReader(command.Stdin),
		&response,
	)
	backend.mu.Lock()
	backend.linked = report
	backend.wire = append([]byte(nil), response.Bytes()...)
	backend.mu.Unlock()
	if err != nil {
		backend.recordError(fmt.Errorf("migration-command linked runner: %w", err))
		return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	}
	wire := append([]byte(nil), response.Bytes()...)
	return productcheck.ProcessResult{
		Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
		StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(wire)},
	}
}

func (backend *migrationCommandProcessBackend) recordError(err error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.err = errors.Join(backend.err, err)
}

func (backend *migrationCommandProcessBackend) snapshot() (linked.Report, []byte, []productcheck.ProcessStage, []productcheck.Command, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	commands := make([]productcheck.Command, len(backend.commands))
	for index := range backend.commands {
		commands[index] = productcheck.Command{
			Dir:   backend.commands[index].Dir,
			Argv:  append([]string(nil), backend.commands[index].Argv...),
			Env:   append([]string(nil), backend.commands[index].Env...),
			Stdin: append([]byte(nil), backend.commands[index].Stdin...),
		}
	}
	return backend.linked, append([]byte(nil), backend.wire...), append([]productcheck.ProcessStage(nil), backend.stages...), commands, backend.err
}

func (project migrationCommandProject) run(
	ctx context.Context,
	config linked.MigrateConfig,
	interrupt <-chan struct{},
	configure func(*migrationCommandProcessBackend),
) migrationCommandGlobalExecution {
	backend := &migrationCommandProcessBackend{config: config}
	if configure != nil {
		configure(backend)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	report := productcheck.RunMigrate(productcheck.MigrateInvocation{
		Context: ctx,
		CWD:     project.root,
		Args:    []string{"migrate"},
		Environment: []string{
			"PATH=" + os.Getenv("PATH"),
			"TMPDIR=" + project.workspace,
			"HOME=" + project.home,
		},
		Stdout:    &stdout,
		Stderr:    &stderr,
		Interrupt: interrupt,
		Backend:   backend,
	})
	linkedReport, wire, stages, commands, backendErr := backend.snapshot()
	return migrationCommandGlobalExecution{
		global: report, linked: linkedReport,
		stdout: append([]byte(nil), stdout.Bytes()...),
		stderr: append([]byte(nil), stderr.Bytes()...), wire: wire,
		stages: stages, commands: commands, err: backendErr,
	}
}

func migrationCommandSources() []definition.Source {
	return []definition.Source{
		migrationCommandCreateModelSource("command-prefix", migrationCommandPrefix, "", "prefix", "Prefix", migrationCommandPrefixTable),
		migrationCommandCreateModelSource("command-middle", migrationCommandMiddle, migrationCommandPrefix, "middle", "Middle", migrationCommandMiddleTable),
		migrationCommandCreateModelSource("command-tail", migrationCommandTail, migrationCommandMiddle, "tail", "Tail", migrationCommandTailTable),
	}
}

func migrationCommandCreateModelSource(sourceID, name, dependency, model, goName, table string) definition.Source {
	dependencies := "[]"
	if dependency != "" {
		dependencies = fmt.Sprintf(`[{"app":%q,"name":%q}]`, migrationCommandApp, dependency)
	}
	document := fmt.Sprintf(
		`{"format_version":1,"producer":{"name":"godj-migration-command-actual","version":"1"},"migration":{"app":%q,"name":%q,"dependencies":%s,"operations":[{"kind":"create_model","app_label":%q,"model":{"name":%q,"go_name":%q,"db_table":%q,"fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`,
		migrationCommandApp,
		name,
		dependencies,
		migrationCommandApp,
		model,
		goName,
		table,
	)
	return definition.Source{SourceID: sourceID, Document: []byte(document)}
}

func migrationCommandRelationSources() []definition.Source {
	target := migrationCommandCreateModelSource("command-relation-target", "0001_target", "", "target", "Target", "godj_command_target")
	document := `{"format_version":1,"producer":{"name":"godj-migration-command-actual","version":"1"},"migration":{"app":"command_relation","name":"0002_source","dependencies":[{"app":"command","name":"0001_target"}],"operations":[{"kind":"create_model","app_label":"command_relation","model":{"name":"source","go_name":"Source","db_table":"godj_command_source","fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null},{"name":"target","go_name":"Target","column":"target_id","kind":"foreign_key","primary_key":false,"nullable":false,"max_length":0,"default":null,"relation":{"target":{"app_label":"command","model_name":"target"},"cardinality":"many_to_one","reverse":{"name":"sources","disabled":false},"on_delete":"protect"}}]}}]}}`
	return []definition.Source{target, {SourceID: "command-relation-source", Document: []byte(document)}}
}

func migrationCommandLoaded(sources []definition.Source) (migrations.LoadedDefinitionSet, definition.LoadReport, error) {
	owned := make([]definition.Source, len(sources))
	for index := range sources {
		owned[index] = definition.Source{SourceID: sources[index].SourceID, Document: append([]byte(nil), sources[index].Document...)}
	}
	return definition.Load(owned...)
}

func migrationCommandSQLiteConfig(
	databasePath string,
	sources []definition.Source,
	trace *migrationCommandTrace,
) linked.MigrateConfig {
	return linked.MigrateConfig{
		MigrationDefinitionSources: sources,
		OpenMigrationBackend: func(ctx context.Context) (linked.MigrationBackend, error) {
			opened, err := sqlite.Open(ctx, databasePath)
			if err != nil {
				return nil, err
			}
			if trace == nil {
				return opened, nil
			}
			return &migrationCommandObservedBackend{MigrationBackend: opened, trace: trace}, nil
		},
	}
}

type migrationCommandTrace struct {
	mu sync.Mutex

	beginKeys          []migrations.MigrationKey
	createTables       []string
	recorderWrites     []migrations.MigrationKey
	commitCalls        int
	rollbackCalls      int
	sessionCloseCalls  int
	backendCloseCalls  int
	failCreateKey      migrations.MigrationKey
	failCreateAfter    bool
	interruptCreateKey migrations.MigrationKey
	interruptReady     chan<- struct{}
	interruptOnce      sync.Once
}

type migrationCommandTraceSnapshot struct {
	beginKeys         []migrations.MigrationKey
	createTables      []string
	recorderWrites    []migrations.MigrationKey
	commitCalls       int
	rollbackCalls     int
	sessionCloseCalls int
	backendCloseCalls int
}

func (trace *migrationCommandTrace) snapshot() migrationCommandTraceSnapshot {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return migrationCommandTraceSnapshot{
		beginKeys:      append([]migrations.MigrationKey(nil), trace.beginKeys...),
		createTables:   append([]string(nil), trace.createTables...),
		recorderWrites: append([]migrations.MigrationKey(nil), trace.recorderWrites...),
		commitCalls:    trace.commitCalls, rollbackCalls: trace.rollbackCalls,
		sessionCloseCalls: trace.sessionCloseCalls, backendCloseCalls: trace.backendCloseCalls,
	}
}

type migrationCommandObservedBackend struct {
	linked.MigrationBackend
	trace *migrationCommandTrace
}

func (backend *migrationCommandObservedBackend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	session, err := backend.MigrationBackend.OpenRevisionFencedSession(ctx)
	if err != nil || session == nil {
		return session, err
	}
	return &migrationCommandObservedSession{RevisionFencedSession: session, trace: backend.trace}, nil
}

func (backend *migrationCommandObservedBackend) Close() error {
	err := backend.MigrationBackend.Close()
	if err == nil {
		backend.trace.mu.Lock()
		backend.trace.backendCloseCalls++
		backend.trace.mu.Unlock()
	}
	return err
}

type migrationCommandObservedSession struct {
	migrationbackend.RevisionFencedSession
	trace *migrationCommandTrace
}

func (session *migrationCommandObservedSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	key := migrations.MigrationKey{App: transition.Migration.App, Name: transition.Migration.Name}
	session.trace.mu.Lock()
	session.trace.beginKeys = append(session.trace.beginKeys, key)
	session.trace.mu.Unlock()
	transaction, err := session.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if err != nil || transaction == nil {
		return transaction, err
	}
	return &migrationCommandObservedTransaction{RevisionFencedTransaction: transaction, trace: session.trace, key: key}, nil
}

func (session *migrationCommandObservedSession) Close(ctx context.Context) error {
	err := session.RevisionFencedSession.Close(ctx)
	if err == nil {
		session.trace.mu.Lock()
		session.trace.sessionCloseCalls++
		session.trace.mu.Unlock()
	}
	return err
}

type migrationCommandObservedTransaction struct {
	migrationbackend.RevisionFencedTransaction
	trace *migrationCommandTrace
	key   migrations.MigrationKey
}

func (transaction *migrationCommandObservedTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	transaction.trace.mu.Lock()
	transaction.trace.createTables = append(transaction.trace.createTables, model.DBTable)
	fail := transaction.trace.failCreateKey == transaction.key
	failAfter := transaction.trace.failCreateAfter
	interrupt := transaction.trace.interruptCreateKey == transaction.key
	ready := transaction.trace.interruptReady
	transaction.trace.mu.Unlock()
	if fail && !failAfter {
		return errors.New("migration-command injected create failure")
	}
	if err := transaction.RevisionFencedTransaction.CreateModel(ctx, model); err != nil {
		return err
	}
	if interrupt {
		transaction.trace.interruptOnce.Do(func() {
			if ready != nil {
				close(ready)
			}
		})
		<-ctx.Done()
		return ctx.Err()
	}
	if fail {
		return errors.New("migration-command injected create failure after mutation")
	}
	return nil
}

func (transaction *migrationCommandObservedTransaction) RecordApplied(ctx context.Context, app, name string) error {
	err := transaction.RevisionFencedTransaction.RecordApplied(ctx, app, name)
	if err == nil {
		transaction.trace.mu.Lock()
		transaction.trace.recorderWrites = append(transaction.trace.recorderWrites, migrations.MigrationKey{App: app, Name: name})
		transaction.trace.mu.Unlock()
	}
	return err
}

func (transaction *migrationCommandObservedTransaction) CommitFenced(ctx context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.trace.mu.Lock()
	transaction.trace.commitCalls++
	transaction.trace.mu.Unlock()
	return transaction.RevisionFencedTransaction.CommitFenced(ctx)
}

func (transaction *migrationCommandObservedTransaction) Rollback(ctx context.Context) error {
	err := transaction.RevisionFencedTransaction.Rollback(ctx)
	if err == nil {
		transaction.trace.mu.Lock()
		transaction.trace.rollbackCalls++
		transaction.trace.mu.Unlock()
	}
	return err
}

func migrationCommandHistoryFingerprint(history []migrations.MigrationKey) []byte {
	canonical := append([]migrations.MigrationKey(nil), history...)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].App != canonical[right].App {
			return canonical[left].App < canonical[right].App
		}
		return canonical[left].Name < canonical[right].Name
	})
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	for _, key := range canonical {
		for _, value := range []string{key.App, key.Name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	return hash.Sum(nil)
}

func migrationCommandExpectedHistory(sources []definition.Source) ([]migrations.MigrationKey, string, error) {
	loaded, _, err := migrationCommandLoaded(sources)
	if err != nil {
		return nil, "", err
	}
	definitions := loaded.Definitions()
	history := make([]migrations.MigrationKey, len(definitions))
	for index := range definitions {
		history[index] = definitions[index].Key()
	}
	sort.Slice(history, func(left, right int) bool {
		if history[left].App != history[right].App {
			return history[left].App < history[right].App
		}
		return history[left].Name < history[right].Name
	})
	return history, loaded.Digest(), nil
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func migrationCommandKeysEqual(left, right []migrations.MigrationKey) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func migrationCommandDuplicateHistory(history []migrations.MigrationKey) int {
	seen := make(map[migrations.MigrationKey]int, len(history))
	duplicates := 0
	for _, key := range history {
		seen[key]++
		if seen[key] > 1 {
			duplicates++
		}
	}
	return duplicates
}

func migrationCommandInt(value int) protocol.Value {
	return protocol.Integer(strconv.Itoa(value))
}

func migrationCommandObservation(
	contract protocol.Contract,
	result *protocol.Value,
	observedError *protocol.ObservedError,
	dbState *protocol.Value,
	metrics protocol.Value,
) protocol.Observation {
	return protocol.Observation{
		ID: contract.ID, Status: protocol.StatusObserved, Phase: contract.Phase,
		Result: result, Error: observedError, DBState: dbState, Metrics: valuePointer(metrics),
	}
}

func migrationCommandObservedError(category, code string) *protocol.ObservedError {
	return &protocol.ObservedError{Category: category, Code: code, MessageIsContract: boolPointer(false)}
}

func migrationCommandSuccess(execution migrationCommandGlobalExecution) error {
	if execution.err != nil {
		return execution.err
	}
	if execution.global.ExitCode != 0 || !execution.global.HasMigrateResult || execution.global.HasMigrateFailure || len(execution.stdout) == 0 || len(execution.stderr) != 0 {
		return fmt.Errorf("migration-command success envelope = exit:%d result:%t failure:%t stdout:%d stderr:%d", execution.global.ExitCode, execution.global.HasMigrateResult, execution.global.HasMigrateFailure, len(execution.stdout), len(execution.stderr))
	}
	if len(execution.stages) != 2 || execution.stages[0] != productcheck.BuildStage || execution.stages[1] != productcheck.MigrateRunnerStage {
		return fmt.Errorf("migration-command stages = %v", execution.stages)
	}
	return nil
}

func migrationCommandFailure(execution migrationCommandGlobalExecution, category, code string) error {
	if execution.err != nil {
		return execution.err
	}
	if execution.global.ExitCode == 0 || execution.global.HasMigrateResult || !execution.global.HasMigrateFailure || execution.global.MigrateFailure.Category != category || execution.global.MigrateFailure.Code != code || len(execution.stdout) != 0 || len(execution.stderr) == 0 {
		return fmt.Errorf("migration-command failure envelope = exit:%d result:%t failure:%+v stdout:%d stderr:%d", execution.global.ExitCode, execution.global.HasMigrateResult, execution.global.MigrateFailure, len(execution.stdout), len(execution.stderr))
	}
	if len(execution.stages) != 2 || execution.stages[0] != productcheck.BuildStage || execution.stages[1] != productcheck.MigrateRunnerStage {
		return fmt.Errorf("migration-command stages = %v", execution.stages)
	}
	return nil
}

func migrationCommandContainsKey(keys []migrations.MigrationKey, want migrations.MigrationKey) int {
	count := 0
	for _, key := range keys {
		if key == want {
			count++
		}
	}
	return count
}

func migrationCommandFreshLatest(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	sources := migrationCommandSources()
	history, digest, err := migrationCommandExpectedHistory(sources)
	if err != nil {
		return protocol.Observation{}, err
	}
	databasePath := filepath.Join(project.root, "fresh.sqlite3")
	trace := &migrationCommandTrace{}
	execution := project.run(ctx, migrationCommandSQLiteConfig(databasePath, sources, trace), nil, nil)
	if err := migrationCommandSuccess(execution); err != nil {
		return protocol.Observation{}, err
	}
	snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	exactErr := migrationCommandAssertExactSQLiteLatest(snapshot, history)
	latest := exactErr == nil
	digestMatches := execution.global.MigrateResult.DefinitionSetDigest == digest
	if !latest || !digestMatches {
		return protocol.Observation{}, fmt.Errorf("migration-command fresh latest = latest:%t digest:%t exact_error:%v", latest, digestMatches, exactErr)
	}
	result := protocol.Object(map[string]protocol.Value{
		"definition_snapshot":           protocol.String("loaded_once_before_backend_open"),
		"history_digest_matches_loaded": protocol.Boolean(digestMatches),
		"outcome":                       protocol.String("latest"),
		"target":                        protocol.String("latest_leaves"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"history": protocol.String("exact_latest"),
		"schema":  protocol.String("exact_latest"),
	})
	return migrationCommandObservation(contract, &result, nil, &dbState, protocol.Object(map[string]protocol.Value{
		"backend_closes":   migrationCommandInt(execution.linked.BackendCloseCalls),
		"backend_opens":    migrationCommandInt(execution.linked.BackendOpenCalls),
		"definition_loads": migrationCommandInt(execution.linked.LoadCalls),
		"migrate_calls":    migrationCommandInt(execution.linked.RevisionLifecycleCalls),
	})), nil
}

func migrationCommandApplySources(ctx context.Context, databasePath string, sources []definition.Source) error {
	loaded, _, err := migrationCommandLoaded(sources)
	if err != nil {
		return err
	}
	opened, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return err
	}
	_, migrateErr := (migrations.Executor{Backend: opened}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	return errors.Join(migrateErr, opened.Close())
}

func migrationCommandAppliedPrefixTail(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	sources := migrationCommandSources()
	history, _, err := migrationCommandExpectedHistory(sources)
	if err != nil {
		return protocol.Observation{}, err
	}
	databasePath := filepath.Join(project.root, "prefix.sqlite3")
	if err := migrationCommandApplySources(ctx, databasePath, sources[:1]); err != nil {
		return protocol.Observation{}, fmt.Errorf("seed migration-command prefix: %w", err)
	}
	trace := &migrationCommandTrace{}
	execution := project.run(ctx, migrationCommandSQLiteConfig(databasePath, sources, trace), nil, nil)
	if err := migrationCommandSuccess(execution); err != nil {
		return protocol.Observation{}, err
	}
	snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	observed := trace.snapshot()
	prefix := migrations.MigrationKey{App: migrationCommandApp, Name: migrationCommandPrefix}
	prefixWrites := migrationCommandContainsKey(observed.recorderWrites, prefix)
	wantTail := []migrations.MigrationKey{
		{App: migrationCommandApp, Name: migrationCommandMiddle},
		{App: migrationCommandApp, Name: migrationCommandTail},
	}
	exactTail := migrationCommandKeysEqual(observed.beginKeys, wantTail) && migrationCommandKeysEqual(observed.recorderWrites, wantTail)
	exactErr := migrationCommandAssertExactSQLiteLatest(snapshot, history)
	latest := exactErr == nil
	if !latest || !exactTail || prefixWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("migration-command prefix/tail = latest:%t exact_tail:%t prefix_writes:%d exact_error:%v", latest, exactTail, prefixWrites, exactErr)
	}
	result := protocol.Object(map[string]protocol.Value{
		"committed_prefix_preserved": protocol.Boolean(migrationCommandContainsKey(snapshot.history, prefix) == 1),
		"outcome":                    protocol.String("latest"),
		"prefix_reapplied":           protocol.Boolean(prefixWrites != 0),
		"remaining_tail_applied":     protocol.Boolean(exactTail),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"duplicate_history_rows": migrationCommandInt(migrationCommandDuplicateHistory(snapshot.history)),
		"history":                protocol.String("exact_latest"),
		"schema":                 protocol.String("exact_latest"),
	})
	return migrationCommandObservation(contract, &result, nil, &dbState, protocol.Object(map[string]protocol.Value{
		"backend_closes":   migrationCommandInt(execution.linked.BackendCloseCalls),
		"backend_opens":    migrationCommandInt(execution.linked.BackendOpenCalls),
		"definition_loads": migrationCommandInt(execution.linked.LoadCalls),
		"migrate_calls":    migrationCommandInt(execution.linked.RevisionLifecycleCalls),
		"prefix_writes":    migrationCommandInt(prefixWrites),
	})), nil
}

func migrationCommandFullyAppliedNoop(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandActualProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	sources := migrationCommandSources()
	history, digest, err := migrationCommandExpectedHistory(sources)
	if err != nil {
		return protocol.Observation{}, err
	}
	databasePath := filepath.Join(project.root, "noop.sqlite3")
	first := project.runActual(ctx, migrationcommandworker.ModeNormal, databasePath, "", nil)
	if err := migrationCommandActualSuccess(first); err != nil {
		return protocol.Observation{}, fmt.Errorf("migration-command no-op seed: %w", err)
	}
	seedParticipants, err := migrationCommandActualParticipants(project)
	if err != nil || len(seedParticipants) != 1 {
		return protocol.Observation{}, fmt.Errorf("migration-command no-op seed participants=%d error=%v", len(seedParticipants), err)
	}
	if err := migrationCommandAssertActualParticipant(seedParticipants[0]); err != nil {
		return protocol.Observation{}, err
	}
	before, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationCommandAssertExactSQLiteLatest(before, history); err != nil {
		return protocol.Observation{}, err
	}
	beforeDigest, err := migrationCommandExactSQLiteFileHash(databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	second := project.runActual(ctx, migrationcommandworker.ModeNormal, databasePath, "", nil)
	if err := migrationCommandActualSuccess(second); err != nil {
		return protocol.Observation{}, err
	}
	after, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationCommandAssertExactSQLiteLatest(after, history); err != nil {
		return protocol.Observation{}, err
	}
	afterDigest, err := migrationCommandExactSQLiteFileHash(databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	participants, err := migrationCommandActualParticipants(project)
	if err != nil || len(participants) != 2 {
		return protocol.Observation{}, fmt.Errorf("migration-command no-op participants=%d error=%v", len(participants), err)
	}
	var noop migrationCommandActualParticipant
	for _, participant := range participants {
		if err := migrationCommandAssertActualParticipant(participant); err != nil {
			return protocol.Observation{}, err
		}
		if participant.pid != seedParticipants[0].pid {
			noop = participant
		}
	}
	if noop.pid == 0 || noop.pid == seedParticipants[0].pid {
		return protocol.Observation{}, errors.New("migration-command no-op did not use a fresh project child")
	}
	beginAttempts := migrationCommandTracePrefix(noop.trace, "begin_attempt:")
	schemaMutations := migrationCommandTracePrefix(noop.trace, "create_complete:")
	historyWrites := migrationCommandTracePrefix(noop.trace, "record_complete:")
	exactLifecycle := migrationCommandTraceCount(noop.trace, "backend_open_complete") == 1 &&
		migrationCommandTraceCount(noop.trace, "session_attempt") == 1 &&
		migrationCommandTraceCount(noop.trace, "session_open_complete") == 1 &&
		migrationCommandTraceCount(noop.trace, "snapshot_attempt") == 1 &&
		migrationCommandTraceCount(noop.trace, "snapshot_complete:3") == 1 &&
		migrationCommandTraceCount(noop.trace, "session_close_complete") == 1 &&
		migrationCommandTraceCount(noop.trace, "backend_close_complete") == 1
	noopErr := migrationCommandAssertExactSQLiteNoop(before, after, beforeDigest, afterDigest)
	unchanged := noopErr == nil
	if !unchanged || !exactLifecycle || len(schemaMutations) != 0 || len(historyWrites) != 0 || len(beginAttempts) != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command no-op changed state: unchanged=%t lifecycle=%t begins=%d schema=%d history=%d before=%s after=%s",
			unchanged, exactLifecycle, len(beginAttempts), len(schemaMutations), len(historyWrites),
			migrationCommandActualDigestText(beforeDigest), migrationCommandActualDigestText(afterDigest),
		)
	}
	definitionProbePath := filepath.Join(project.root, "noop-definition-load.sqlite3")
	definitionSeed := project.run(
		ctx,
		migrationCommandSQLiteConfig(definitionProbePath, sources, &migrationCommandTrace{}),
		nil,
		nil,
	)
	if err := migrationCommandSuccess(definitionSeed); err != nil {
		return protocol.Observation{}, fmt.Errorf("migration-command no-op definition-load seed: %w", err)
	}
	definitionProbeTrace := &migrationCommandTrace{}
	definitionProbe := project.run(
		ctx,
		migrationCommandSQLiteConfig(definitionProbePath, sources, definitionProbeTrace),
		nil,
		nil,
	)
	if err := migrationCommandSuccess(definitionProbe); err != nil {
		return protocol.Observation{}, fmt.Errorf("migration-command no-op definition-load probe: %w", err)
	}
	definitionLoads := definitionProbe.linked.LoadCalls
	definitionProbeObserved := definitionProbeTrace.snapshot()
	if definitionSeed.linked.LoadCalls != 1 || definitionLoads != 1 ||
		definitionProbe.linked.RevisionLifecycleCalls != 1 || len(definitionProbeObserved.beginKeys) != 0 ||
		len(definitionProbeObserved.recorderWrites) != 0 || len(definitionProbeObserved.createTables) != 0 ||
		definitionProbeObserved.commitCalls != 0 || definitionProbeObserved.rollbackCalls != 0 ||
		definitionProbeObserved.sessionCloseCalls != 1 || definitionProbeObserved.backendCloseCalls != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command no-op definition-load proof = seed:%d second:%d migrate:%d begins:%d writes:%d schema:%d commit:%d rollback:%d session_close:%d backend_close:%d",
			definitionSeed.linked.LoadCalls, definitionLoads, definitionProbe.linked.RevisionLifecycleCalls,
			len(definitionProbeObserved.beginKeys), len(definitionProbeObserved.recorderWrites), len(definitionProbeObserved.createTables),
			definitionProbeObserved.commitCalls, definitionProbeObserved.rollbackCalls,
			definitionProbeObserved.sessionCloseCalls, definitionProbeObserved.backendCloseCalls,
		)
	}
	result := protocol.Object(map[string]protocol.Value{
		"fresh_process":                 protocol.Boolean(noop.pid != seedParticipants[0].pid),
		"history_digest_matches_loaded": protocol.Boolean(second.report.MigrateResult.DefinitionSetDigest == digest),
		"outcome":                       protocol.String("no_op"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"duplicate_history_rows": migrationCommandInt(migrationCommandDuplicateHistory(after.history)),
		"history":                protocol.String("unchanged_latest"),
		"schema":                 protocol.String("unchanged_latest"),
	})
	return migrationCommandObservation(contract, &result, nil, &dbState, protocol.Object(map[string]protocol.Value{
		"backend_closes":   migrationCommandInt(migrationCommandTraceCount(noop.trace, "backend_close_complete")),
		"backend_opens":    migrationCommandInt(migrationCommandTraceCount(noop.trace, "backend_open_complete")),
		"definition_loads": migrationCommandInt(definitionLoads),
		"history_writes":   migrationCommandInt(len(historyWrites)),
		"migrate_calls":    migrationCommandInt(migrationCommandTraceCount(noop.trace, "session_attempt")),
		"schema_mutations": migrationCommandInt(len(schemaMutations)),
	})), nil
}

func migrationCommandDefinitionPreflight(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	const secret = "migration-command-definition-secret-canary"
	valid := migrationCommandSources()[0]
	unknown := definition.Source{
		SourceID: secret + "-format",
		Document: bytes.Replace(valid.Document, []byte(`"format_version":1`), []byte(`"format_version":2`), 1),
	}
	tests := []struct {
		name     string
		source   definition.Source
		wantCode string
	}{
		{name: "invalid_definition_document", source: definition.Source{SourceID: secret + "-invalid", Document: []byte(`{"format_version":`)}, wantCode: "invalid_definition_document"},
		{name: "unknown_current_format", source: unknown, wantCode: "definition_format_incompatible"},
	}
	cases := make([]protocol.Value, 0, len(tests))
	definitionLoads := 0
	backendOpens := 0
	migrateCalls := 0
	causesPublished := false
	for _, test := range tests {
		openCalls := 0
		execution := project.run(ctx, linked.MigrateConfig{
			MigrationDefinitionSources: []definition.Source{test.source},
			OpenMigrationBackend: func(context.Context) (linked.MigrationBackend, error) {
				openCalls++
				return nil, errors.New(secret)
			},
		}, nil, nil)
		if err := migrationCommandFailure(execution, migrateprotocol.CategorySource, test.wantCode); err != nil {
			return protocol.Observation{}, fmt.Errorf("migration-command definition case %s: %w", test.name, err)
		}
		definitionLoads += execution.linked.LoadCalls
		backendOpens += execution.linked.BackendOpenCalls + openCalls
		migrateCalls += execution.linked.RevisionLifecycleCalls
		visible := append(append(append([]byte(nil), execution.stdout...), execution.stderr...), execution.wire...)
		causesPublished = causesPublished || bytes.Contains(visible, []byte(secret))
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"backend_opens": migrationCommandInt(execution.linked.BackendOpenCalls),
			"case":          protocol.String(test.name),
			"category":      protocol.String(execution.global.MigrateFailure.Category),
			"code":          protocol.String(execution.global.MigrateFailure.Code),
		}))
	}
	if backendOpens != 0 || migrateCalls != 0 || causesPublished {
		return protocol.Observation{}, fmt.Errorf("migration-command definition preflight = backend:%d migrate:%d causes:%t", backendOpens, migrateCalls, causesPublished)
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases":                  protocol.List(cases...),
		"causes_published":       protocol.Boolean(causesPublished),
		"definition_publication": protocol.String("atomic"),
	})
	return migrationCommandObservation(contract, &result, nil, nil, protocol.Object(map[string]protocol.Value{
		"backend_opens":    migrationCommandInt(backendOpens),
		"cases":            migrationCommandInt(len(cases)),
		"definition_loads": migrationCommandInt(definitionLoads),
		"migrate_calls":    migrationCommandInt(migrateCalls),
	})), nil
}

type migrationCommandSyntheticBackend struct {
	mu sync.Mutex

	records      []migrationbackend.AppliedMigration
	capabilities migrationbackend.MigrationCapabilities
	commit       migrationbackend.CommitOutcome
	commitErr    error

	sessionOpenCalls  int
	snapshotCalls     int
	beginCalls        int
	schemaMutations   int
	recorderWrites    int
	commitCalls       int
	rollbackCalls     int
	sessionCloseCalls int
	backendCloseCalls int
}

func (backend *migrationCommandSyntheticBackend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	return backend.capabilities
}

func (backend *migrationCommandSyntheticBackend) OpenRevisionFencedSession(context.Context) (migrationbackend.RevisionFencedSession, error) {
	backend.mu.Lock()
	backend.sessionOpenCalls++
	backend.mu.Unlock()
	return &migrationCommandSyntheticSession{backend: backend}, nil
}

func (backend *migrationCommandSyntheticBackend) Close() error {
	backend.mu.Lock()
	backend.backendCloseCalls++
	backend.mu.Unlock()
	return nil
}

type migrationCommandSyntheticSession struct {
	backend *migrationCommandSyntheticBackend
}

func (session *migrationCommandSyntheticSession) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	session.backend.mu.Lock()
	defer session.backend.mu.Unlock()
	session.backend.snapshotCalls++
	return append([]migrationbackend.AppliedMigration(nil), session.backend.records...), nil
}

func (session *migrationCommandSyntheticSession) BeginMigration(
	context.Context,
	migrationbackend.HistoryTransition,
	migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.backend.mu.Lock()
	session.backend.beginCalls++
	session.backend.mu.Unlock()
	return &migrationCommandSyntheticTransaction{backend: session.backend}, nil
}

func (session *migrationCommandSyntheticSession) Close(context.Context) error {
	session.backend.mu.Lock()
	session.backend.sessionCloseCalls++
	session.backend.mu.Unlock()
	return nil
}

type migrationCommandSyntheticTransaction struct {
	backend *migrationCommandSyntheticBackend
}

func (transaction *migrationCommandSyntheticTransaction) CreateModel(context.Context, ir.Model) error {
	transaction.backend.mu.Lock()
	transaction.backend.schemaMutations++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationCommandSyntheticTransaction) DeleteModel(context.Context, ir.Model) error {
	transaction.backend.mu.Lock()
	transaction.backend.schemaMutations++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationCommandSyntheticTransaction) AddField(context.Context, ir.Model, ir.Field) error {
	transaction.backend.mu.Lock()
	transaction.backend.schemaMutations++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationCommandSyntheticTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	transaction.backend.mu.Lock()
	transaction.backend.schemaMutations++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationCommandSyntheticTransaction) RecordApplied(context.Context, string, string) error {
	transaction.backend.mu.Lock()
	transaction.backend.recorderWrites++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationCommandSyntheticTransaction) RecordUnapplied(context.Context, string, string) error {
	transaction.backend.mu.Lock()
	transaction.backend.recorderWrites++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationCommandSyntheticTransaction) CommitFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.backend.mu.Lock()
	transaction.backend.commitCalls++
	outcome := transaction.backend.commit
	err := transaction.backend.commitErr
	transaction.backend.mu.Unlock()
	if outcome.Durability == 0 {
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, nil
	}
	return outcome, err
}

func (transaction *migrationCommandSyntheticTransaction) Rollback(context.Context) error {
	transaction.backend.mu.Lock()
	transaction.backend.rollbackCalls++
	transaction.backend.mu.Unlock()
	return nil
}

type migrationCommandSyntheticSnapshot struct {
	sessionOpenCalls  int
	snapshotCalls     int
	beginCalls        int
	schemaMutations   int
	recorderWrites    int
	commitCalls       int
	rollbackCalls     int
	sessionCloseCalls int
	backendCloseCalls int
}

func (backend *migrationCommandSyntheticBackend) snapshot() migrationCommandSyntheticSnapshot {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return migrationCommandSyntheticSnapshot{
		sessionOpenCalls: backend.sessionOpenCalls, snapshotCalls: backend.snapshotCalls,
		beginCalls: backend.beginCalls, schemaMutations: backend.schemaMutations,
		recorderWrites: backend.recorderWrites, commitCalls: backend.commitCalls,
		rollbackCalls: backend.rollbackCalls, sessionCloseCalls: backend.sessionCloseCalls,
		backendCloseCalls: backend.backendCloseCalls,
	}
}

func migrationCommandSyntheticConfig(sources []definition.Source, backend *migrationCommandSyntheticBackend) linked.MigrateConfig {
	return linked.MigrateConfig{
		MigrationDefinitionSources: sources,
		OpenMigrationBackend: func(context.Context) (linked.MigrationBackend, error) {
			return backend, nil
		},
	}
}

func migrationCommandInconsistentHistory(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	backend := &migrationCommandSyntheticBackend{records: []migrationbackend.AppliedMigration{{
		App: migrationCommandApp, Name: migrationCommandTail,
	}}}
	execution := project.run(ctx, migrationCommandSyntheticConfig(migrationCommandSources(), backend), nil, nil)
	if err := migrationCommandFailure(execution, migrateprotocol.CategoryHistory, string(migrations.CodeInconsistentAppliedHistory)); err != nil {
		return protocol.Observation{}, err
	}
	observed := backend.snapshot()
	if observed.beginCalls != 0 || observed.schemaMutations != 0 || observed.recorderWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("migration-command inconsistent history crossed mutation boundary: %+v", observed)
	}
	dbState := protocol.Object(map[string]protocol.Value{
		"history":                 protocol.String("preserved_inconsistent"),
		"reconciliation_required": protocol.Boolean(true),
		"schema":                  protocol.String("unchanged"),
	})
	return migrationCommandObservation(contract, nil, migrationCommandObservedError(execution.global.MigrateFailure.Category, execution.global.MigrateFailure.Code), &dbState, protocol.Object(map[string]protocol.Value{
		"backend_closes":   migrationCommandInt(execution.linked.BackendCloseCalls),
		"backend_opens":    migrationCommandInt(execution.linked.BackendOpenCalls),
		"migration_begins": migrationCommandInt(observed.beginCalls),
		"recorder_writes":  migrationCommandInt(observed.recorderWrites),
		"schema_mutations": migrationCommandInt(observed.schemaMutations),
	})), nil
}

func migrationCommandCapabilityPreflight(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	backend := &migrationCommandSyntheticBackend{}
	execution := project.run(ctx, migrationCommandSyntheticConfig(migrationCommandRelationSources(), backend), nil, nil)
	if err := migrationCommandFailure(execution, migrateprotocol.CategoryCapability, string(migrations.CodeUnsupported)); err != nil {
		return protocol.Observation{}, err
	}
	observed := backend.snapshot()
	ignored := observed.beginCalls != 0 || observed.schemaMutations != 0 || observed.recorderWrites != 0
	if ignored {
		return protocol.Observation{}, fmt.Errorf("migration-command capability preflight crossed mutation boundary: %+v", observed)
	}
	dbState := protocol.Object(map[string]protocol.Value{
		"history": protocol.String("unchanged"),
		"schema":  protocol.String("unchanged"),
	})
	return migrationCommandObservation(contract, nil, migrationCommandObservedError(execution.global.MigrateFailure.Category, execution.global.MigrateFailure.Code), &dbState, protocol.Object(map[string]protocol.Value{
		"backend_closes":                migrationCommandInt(execution.linked.BackendCloseCalls),
		"backend_opens":                 migrationCommandInt(execution.linked.BackendOpenCalls),
		"migration_begins":              migrationCommandInt(observed.beginCalls),
		"recorder_writes":               migrationCommandInt(observed.recorderWrites),
		"schema_mutations":              migrationCommandInt(observed.schemaMutations),
		"unsupported_operation_ignored": protocol.Boolean(ignored),
	})), nil
}

func migrationCommandMiddleFailure(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	databasePath := filepath.Join(project.root, "middle-failure.sqlite3")
	trace := &migrationCommandTrace{
		failCreateKey:   migrations.MigrationKey{App: migrationCommandApp, Name: migrationCommandMiddle},
		failCreateAfter: true,
	}
	execution := project.run(ctx, migrationCommandSQLiteConfig(databasePath, migrationCommandSources(), trace), nil, nil)
	if err := migrationCommandFailure(execution, migrateprotocol.CategoryExecution, string(migrations.CodeOperationFailed)); err != nil {
		return protocol.Observation{}, err
	}
	snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	observed := trace.snapshot()
	tailBegins := migrationCommandContainsKey(observed.beginKeys, migrations.MigrationKey{App: migrationCommandApp, Name: migrationCommandTail})
	exactErr := migrationCommandAssertExactSQLitePrefix(snapshot)
	prefixOnly := exactErr == nil
	wantBegins := []migrations.MigrationKey{
		{App: migrationCommandApp, Name: migrationCommandPrefix},
		{App: migrationCommandApp, Name: migrationCommandMiddle},
	}
	wantWrites := []migrations.MigrationKey{{App: migrationCommandApp, Name: migrationCommandPrefix}}
	exactAttempts := migrationCommandKeysEqual(observed.beginKeys, wantBegins) &&
		migrationCommandKeysEqual(observed.recorderWrites, wantWrites) &&
		execution.linked.RevisionLifecycleCalls == 1 && observed.rollbackCalls == 1 &&
		observed.sessionCloseCalls == 1 && observed.backendCloseCalls == 1
	automaticRetries := (execution.linked.RevisionLifecycleCalls - 1) +
		(observed.sessionCloseCalls - 1) + (observed.backendCloseCalls - 1)
	if !prefixOnly || !exactAttempts || automaticRetries != 0 || tailBegins != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command middle failure = prefix:%t attempts:%t retries:%d rollback:%d tail:%d exact_error:%v",
			prefixOnly, exactAttempts, automaticRetries, observed.rollbackCalls, tailBegins, exactErr,
		)
	}
	dbState := protocol.Object(map[string]protocol.Value{
		"committed_prefix_preserved": protocol.Boolean(prefixOnly),
		"current_step":               protocol.String("rolled_back"),
		"current_step_effects":       protocol.String("absent"),
		"history":                    protocol.String("exact_committed_prefix"),
		"schema":                     protocol.String("exact_committed_prefix"),
		"tail_executed":              protocol.Boolean(tailBegins != 0),
	})
	return migrationCommandObservation(contract, nil, migrationCommandObservedError(execution.global.MigrateFailure.Category, execution.global.MigrateFailure.Code), &dbState, protocol.Object(map[string]protocol.Value{
		"automatic_retries":     migrationCommandInt(automaticRetries),
		"rollbacks":             migrationCommandInt(observed.rollbackCalls),
		"tail_migration_begins": migrationCommandInt(tailBegins),
	})), nil
}

func migrationCommandFreshResume(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandActualProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	sources := migrationCommandSources()
	history, _, err := migrationCommandExpectedHistory(sources)
	if err != nil {
		return protocol.Observation{}, err
	}
	databasePath := filepath.Join(project.root, "resume.sqlite3")
	failed := project.runActual(ctx, migrationcommandworker.ModeFailMiddle, databasePath, "", nil)
	if err := migrationCommandActualFailure(failed, migrateprotocol.CategoryExecution, string(migrations.CodeOperationFailed), 0, 0); err != nil {
		return protocol.Observation{}, fmt.Errorf("migration-command resume seed: %w", err)
	}
	failedParticipants, err := migrationCommandActualParticipants(project)
	if err != nil || len(failedParticipants) != 1 {
		return protocol.Observation{}, fmt.Errorf("migration-command resume failed participants=%d error=%v", len(failedParticipants), err)
	}
	failedParticipant := failedParticipants[0]
	if err := migrationCommandAssertActualParticipant(failedParticipant); err != nil {
		return protocol.Observation{}, err
	}
	failedBegins := migrationCommandTracePrefix(failedParticipant.trace, "begin_attempt:")
	failedCreates := migrationCommandTracePrefix(failedParticipant.trace, "create_complete:")
	failedWrites := migrationCommandTracePrefix(failedParticipant.trace, "record_complete:")
	failedCommits := migrationCommandTracePrefix(failedParticipant.trace, "commit_complete:")
	wantFailedBegins := []string{"command/" + migrationCommandPrefix, "command/" + migrationCommandMiddle}
	wantFailedCreates := []string{migrationCommandPrefixTable, migrationCommandMiddleTable}
	wantFailedWrites := []string{"command/" + migrationCommandPrefix}
	wantFailedCommits := []string{"command/" + migrationCommandPrefix + ":2"}
	if !slicesEqual(failedBegins, wantFailedBegins) ||
		!slicesEqual(failedCreates, wantFailedCreates) ||
		!slicesEqual(failedWrites, wantFailedWrites) ||
		!slicesEqual(failedCommits, wantFailedCommits) ||
		migrationCommandTraceCount(failedParticipant.trace, "session_attempt") != 1 ||
		migrationCommandTraceCount(failedParticipant.trace, "snapshot_attempt") != 1 ||
		migrationCommandTraceCount(failedParticipant.trace, "failure_injected:command/"+migrationCommandMiddle) != 1 ||
		migrationCommandTraceCount(failedParticipant.trace, "rollback_complete:command/"+migrationCommandMiddle) != 1 ||
		len(migrationCommandTracePrefix(failedParticipant.trace, "begin_attempt:command/"+migrationCommandTail)) != 0 {
		return protocol.Observation{}, fmt.Errorf("migration-command resume failed trace = %v", failedParticipant.trace)
	}
	before, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationCommandAssertExactSQLitePrefix(before); err != nil {
		return protocol.Observation{}, fmt.Errorf("migration-command resume prefix: %w", err)
	}
	resumed := project.runActual(ctx, migrationcommandworker.ModeNormal, databasePath, "", nil)
	if err := migrationCommandActualSuccess(resumed); err != nil {
		return protocol.Observation{}, err
	}
	after, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	participants, err := migrationCommandActualParticipants(project)
	if err != nil || len(participants) != 2 {
		return protocol.Observation{}, fmt.Errorf("migration-command resume participants=%d error=%v", len(participants), err)
	}
	var resumedParticipant migrationCommandActualParticipant
	for _, participant := range participants {
		if err := migrationCommandAssertActualParticipant(participant); err != nil {
			return protocol.Observation{}, err
		}
		if participant.pid != failedParticipant.pid {
			resumedParticipant = participant
		}
	}
	if resumedParticipant.pid == 0 || resumedParticipant.pid == failedParticipant.pid {
		return protocol.Observation{}, errors.New("migration-command resume did not use a fresh project child")
	}
	resumeBegins := migrationCommandTracePrefix(resumedParticipant.trace, "begin_attempt:")
	resumeCreates := migrationCommandTracePrefix(resumedParticipant.trace, "create_complete:")
	resumeWrites := migrationCommandTracePrefix(resumedParticipant.trace, "record_complete:")
	resumeCommits := migrationCommandTracePrefix(resumedParticipant.trace, "commit_complete:")
	wantResume := []string{"command/" + migrationCommandMiddle, "command/" + migrationCommandTail}
	wantResumeCreates := []string{migrationCommandMiddleTable, migrationCommandTailTable}
	wantResumeCommits := []string{
		"command/" + migrationCommandMiddle + ":2",
		"command/" + migrationCommandTail + ":2",
	}
	prefixWrites := 0
	for _, key := range resumeWrites {
		if key == "command/"+migrationCommandPrefix {
			prefixWrites++
		}
	}
	exactErr := migrationCommandAssertExactSQLiteLatest(after, history)
	latest := exactErr == nil
	failedSessionAttempts := migrationCommandTraceCount(failedParticipant.trace, "session_attempt")
	failedSnapshotAttempts := migrationCommandTraceCount(failedParticipant.trace, "snapshot_attempt")
	resumeSessionAttempts := migrationCommandTraceCount(resumedParticipant.trace, "session_attempt")
	resumeSnapshotAttempts := migrationCommandTraceCount(resumedParticipant.trace, "snapshot_attempt")
	exactAttempts := slicesEqual(resumeBegins, wantResume) && slicesEqual(resumeCreates, wantResumeCreates) &&
		slicesEqual(resumeWrites, wantResume) && slicesEqual(resumeCommits, wantResumeCommits) &&
		resumeSessionAttempts == 1 && resumeSnapshotAttempts == 1
	automaticRetries := (failedSessionAttempts - 1) + (failedSnapshotAttempts - 1) +
		(resumeSessionAttempts - 1) + (resumeSnapshotAttempts - 1)
	freshProcess := resumedParticipant.pid != failedParticipant.pid
	freshInvocations := len(participants) - len(failedParticipants)
	resumeAtFailedMigration := len(failedBegins) == 2 && len(resumeBegins) == 2 &&
		failedBegins[1] == "command/"+migrationCommandMiddle && resumeBegins[0] == failedBegins[1]
	if !latest || prefixWrites != 0 || !exactAttempts || automaticRetries != 0 ||
		!freshProcess || freshInvocations != 1 || !resumeAtFailedMigration {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command resume = latest:%t prefix_writes:%d begins:%v creates:%v writes:%v commits:%v retries:%d fresh:%t/%d resume:%t exact:%v",
			latest, prefixWrites, resumeBegins, resumeCreates, resumeWrites, resumeCommits, automaticRetries,
			freshProcess, freshInvocations, resumeAtFailedMigration, exactErr,
		)
	}
	result := protocol.Object(map[string]protocol.Value{
		"committed_prefix_reapplied": protocol.Boolean(prefixWrites != 0),
		"fresh_process":              protocol.Boolean(freshProcess),
		"outcome":                    protocol.String("latest"),
		"resume_point":               protocol.String("failed_migration"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"duplicate_history_rows": migrationCommandInt(migrationCommandDuplicateHistory(after.history)),
		"history":                protocol.String("exact_latest"),
		"schema":                 protocol.String("exact_latest"),
	})
	return migrationCommandObservation(contract, &result, nil, &dbState, protocol.Object(map[string]protocol.Value{
		"automatic_retries": migrationCommandInt(automaticRetries),
		"fresh_invocations": migrationCommandInt(freshInvocations),
		"prefix_writes":     migrationCommandInt(prefixWrites),
	})), nil
}

func migrationCommandCommitUnknown(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	backend := &migrationCommandSyntheticBackend{
		commit:    migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown},
		commitErr: errors.New("migration-command synthetic ambiguous commit"),
	}
	execution := project.run(ctx, migrationCommandSyntheticConfig(migrationCommandSources()[:1], backend), nil, nil)
	if err := migrationCommandFailure(execution, migrateprotocol.CategoryTransaction, string(migrations.CodeCommitOutcomeUnknown)); err != nil {
		return protocol.Observation{}, err
	}
	observed := backend.snapshot()
	successPublications := execution.global.UserStdoutWrites
	automaticRetries := (execution.linked.RevisionLifecycleCalls - 1) +
		(observed.sessionOpenCalls - 1) + (observed.snapshotCalls - 1) +
		(observed.beginCalls - 1) + (observed.commitCalls - 1)
	exactAttempts := execution.linked.RevisionLifecycleCalls == 1 && observed.sessionOpenCalls == 1 &&
		observed.snapshotCalls == 1 && observed.beginCalls == 1 && observed.schemaMutations == 1 &&
		observed.recorderWrites == 1 && observed.commitCalls == 1 && observed.rollbackCalls == 0 &&
		observed.sessionCloseCalls == 1 && observed.backendCloseCalls == 1
	if !exactAttempts || automaticRetries != 0 || successPublications != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command commit unknown = attempts:%t retries:%d commit:%d rollback:%d success:%d snapshot=%+v",
			exactAttempts, automaticRetries, observed.commitCalls, observed.rollbackCalls, successPublications, observed,
		)
	}
	dbState := protocol.Object(map[string]protocol.Value{
		"history":                 protocol.String("unknown"),
		"reconciliation_required": protocol.Boolean(true),
		"reported_success":        protocol.Boolean(successPublications != 0),
		"schema":                  protocol.String("unknown"),
		"verified_commit":         protocol.Boolean(false),
	})
	return migrationCommandObservation(contract, nil, migrationCommandObservedError(execution.global.MigrateFailure.Category, execution.global.MigrateFailure.Code), &dbState, protocol.Object(map[string]protocol.Value{
		"automatic_retries":      migrationCommandInt(automaticRetries),
		"rollback_after_unknown": migrationCommandInt(observed.rollbackCalls),
		"success_publications":   migrationCommandInt(successPublications),
	})), nil
}

func migrationCommandConcurrentLatest(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandActualProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	sources := migrationCommandSources()
	history, _, err := migrationCommandExpectedHistory(sources)
	if err != nil {
		return protocol.Observation{}, err
	}
	databasePath := filepath.Join(project.root, "concurrent.sqlite3")
	databaseDSN := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc&_busy_timeout=250"
	completed := make(chan migrationCommandActualExecution, 2)
	start := make(chan struct{})
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			completed <- project.runActual(ctx, migrationcommandworker.ModeConcurrency, databaseDSN, "", nil)
		}()
	}
	close(start)
	executions := []migrationCommandActualExecution{<-completed, <-completed}
	successes := 0
	contended := 0
	for _, execution := range executions {
		if execution.report.HasMigrateResult && execution.report.ExitCode == 0 {
			if err := migrationCommandActualSuccess(execution); err != nil {
				return protocol.Observation{}, err
			}
			successes++
			continue
		}
		if execution.report.HasMigrateFailure && execution.report.MigrateFailure.Category == migrateprotocol.CategoryTransaction && execution.report.MigrateFailure.Code == string(migrations.CodeHistoryRevisionContended) {
			if err := migrationCommandActualFailure(execution, migrateprotocol.CategoryTransaction, string(migrations.CodeHistoryRevisionContended), 0, 0); err != nil {
				return protocol.Observation{}, err
			}
			contended++
			continue
		}
		return protocol.Observation{}, fmt.Errorf("migration-command concurrency unexpected outcome = %+v", execution.report)
	}
	participants, err := migrationCommandActualParticipants(project)
	if err != nil {
		return protocol.Observation{}, err
	}
	concurrentParticipants := migrationCommandActualParticipantsByMode(participants, migrationcommandworker.ModeConcurrency)
	if successes != 1 || contended != 1 || len(participants) != 2 || len(concurrentParticipants) != 2 || concurrentParticipants[0].pid == concurrentParticipants[1].pid {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command concurrency = successes:%d contended:%d participants:%d concurrent:%d",
			successes, contended, len(participants), len(concurrentParticipants),
		)
	}
	for _, participant := range participants {
		if err := migrationCommandAssertActualParticipant(participant); err != nil {
			return protocol.Observation{}, err
		}
	}
	wantWinnerBegins := []string{
		"command/" + migrationCommandPrefix,
		"command/" + migrationCommandMiddle,
		"command/" + migrationCommandTail,
	}
	wantContenderBegins := []string{"command/" + migrationCommandPrefix}
	automaticRetries := 0
	for index, participant := range concurrentParticipants {
		sessionAttempts := migrationCommandTraceCount(participant.trace, "session_attempt")
		snapshotAttempts := migrationCommandTraceCount(participant.trace, "snapshot_attempt")
		automaticRetries += (sessionAttempts - 1) + (snapshotAttempts - 1)
		if sessionAttempts != 1 || snapshotAttempts != 1 ||
			migrationCommandTraceCount(participant.trace, "snapshot_complete:0") != 1 {
			return protocol.Observation{}, fmt.Errorf("migration-command concurrency lifecycle pid=%d trace=%v", participant.pid, participant.trace)
		}
		begins := migrationCommandTracePrefix(participant.trace, "begin_attempt:")
		response, failure, failed := migrateprotocol.ParseResponse(participant.privateReply, true)
		if failed || failure != (migrateprotocol.Failure{}) {
			return protocol.Observation{}, errors.New("migration-command concurrency private response did not parse")
		}
		if index == 0 {
			if !slicesEqual(begins, wantWinnerBegins) || !response.OK ||
				!slicesEqual(migrationCommandTracePrefix(participant.trace, "record_complete:"), wantWinnerBegins) {
				return protocol.Observation{}, fmt.Errorf("migration-command concurrency winner pid=%d trace=%v response=%+v", participant.pid, participant.trace, response)
			}
			continue
		}
		if !slicesEqual(begins, wantContenderBegins) || response.OK ||
			response.Failure.Category != migrateprotocol.CategoryTransaction ||
			response.Failure.Code != string(migrations.CodeHistoryRevisionContended) ||
			migrationCommandTraceCount(participant.trace, "begin_contended:command/"+migrationCommandPrefix) != 1 ||
			len(migrationCommandTracePrefix(participant.trace, "record_complete:")) != 0 {
			return protocol.Observation{}, fmt.Errorf("migration-command concurrency contender pid=%d trace=%v response=%+v", participant.pid, participant.trace, response)
		}
	}
	winnerMarker, err := os.ReadFile(filepath.Join(project.trace, "winner-lock"))
	if err != nil || string(winnerMarker) != fmt.Sprintf("pid=%d\n", concurrentParticipants[0].pid) {
		return protocol.Observation{}, fmt.Errorf("migration-command concurrency winner marker=%q error=%v", winnerMarker, err)
	}
	contenderMarker, err := os.ReadFile(filepath.Join(project.trace, "contender-observed"))
	if err != nil || string(contenderMarker) != fmt.Sprintf("pid=%d\nstatus=contended\n", concurrentParticipants[1].pid) {
		return protocol.Observation{}, fmt.Errorf("migration-command concurrency contender marker=%q error=%v", contenderMarker, err)
	}

	reconciliation := project.runActual(ctx, migrationcommandworker.ModeNormal, databaseDSN, "", nil)
	if err := migrationCommandActualSuccess(reconciliation); err != nil {
		return protocol.Observation{}, fmt.Errorf("migration-command reconciliation: %w", err)
	}
	participants, err = migrationCommandActualParticipants(project)
	if err != nil {
		return protocol.Observation{}, err
	}
	finalConcurrentParticipants := migrationCommandActualParticipantsByMode(participants, migrationcommandworker.ModeConcurrency)
	reconciliations := migrationCommandActualParticipantsByMode(participants, migrationcommandworker.ModeNormal)
	if len(participants) != 3 || len(finalConcurrentParticipants) != 2 || len(reconciliations) != 1 ||
		reconciliations[0].pid == concurrentParticipants[0].pid || reconciliations[0].pid == concurrentParticipants[1].pid {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command reconciliation participant inventory = total:%d concurrent:%d normal:%d",
			len(participants), len(finalConcurrentParticipants), len(reconciliations),
		)
	}
	for _, participant := range participants {
		if err := migrationCommandAssertActualParticipant(participant); err != nil {
			return protocol.Observation{}, err
		}
	}
	if migrationCommandTraceCount(reconciliations[0].trace, "session_attempt") != 1 ||
		migrationCommandTraceCount(reconciliations[0].trace, "snapshot_attempt") != 1 ||
		migrationCommandTraceCount(reconciliations[0].trace, "snapshot_complete:3") != 1 ||
		len(migrationCommandTracePrefix(reconciliations[0].trace, "begin_attempt:")) != 0 {
		return protocol.Observation{}, fmt.Errorf("migration-command reconciliation trace=%v", reconciliations[0].trace)
	}
	snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	duplicate := migrationCommandDuplicateHistory(snapshot.history)
	exactErr := migrationCommandAssertExactSQLiteLatest(snapshot, history)
	corrupt := exactErr != nil
	if duplicate != 0 || corrupt || automaticRetries != 0 {
		return protocol.Observation{}, fmt.Errorf("migration-command concurrent database = duplicate:%d corrupt:%t retries:%d: %v", duplicate, corrupt, automaticRetries, exactErr)
	}
	result := protocol.Object(map[string]protocol.Value{
		"corrupt_history":      protocol.Boolean(corrupt),
		"duplicate_history":    protocol.Boolean(duplicate != 0),
		"fresh_reconciliation": protocol.String("latest"),
		"outcome":              protocol.String("fenced"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"history": protocol.String("exact_latest"),
		"schema":  protocol.String("exact_latest"),
	})
	return migrationCommandObservation(contract, &result, nil, &dbState, protocol.Object(map[string]protocol.Value{
		"automatic_retries":                migrationCommandInt(automaticRetries),
		"child_processes":                  migrationCommandInt(len(concurrentParticipants)),
		"corrupt_history_rows":             migrationCommandInt(boolToInt(corrupt)),
		"duplicate_history_rows":           migrationCommandInt(duplicate),
		"fresh_reconciliation_invocations": migrationCommandInt(migrationCommandTraceCount(reconciliations[0].trace, "session_attempt")),
	})), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func migrationCommandBackendSecrets(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandActualProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	repository, err := systemStateRepositoryRoot()
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := migrationCommandAssertGlobalSourceOwnership(repository); err != nil {
		return protocol.Observation{}, err
	}
	secret, err := migrationCommandActualSecretCanary()
	if err != nil {
		return protocol.Observation{}, err
	}
	type secretCase struct {
		name     string
		mode     string
		wantCode string
	}
	tests := []secretCase{
		{name: "missing_configuration", mode: migrationcommandworker.ModeSecretMissing, wantCode: migrateprotocol.CodeBackendOpenFailed},
		{name: "invalid_configuration", mode: migrationcommandworker.ModeSecretInvalid, wantCode: migrateprotocol.CodeBackendOpenFailed},
		{name: "typed_nil_backend", mode: migrationcommandworker.ModeSecretNil, wantCode: migrateprotocol.CodeInvalidBackend},
	}
	cases := make([]protocol.Value, 0, len(tests))
	var publicStdout bytes.Buffer
	var publicStderr bytes.Buffer
	protocolOccurrences := 0
	privateStderrOccurrences := 0
	environmentPassThroughOccurrences := 0
	childAccesses := 0
	for index, test := range tests {
		execution := project.runActual(ctx, test.mode, "", secret, nil)
		if err := migrationCommandActualFailure(execution, migrateprotocol.CategoryBackend, test.wantCode, 0, 0); err != nil {
			return protocol.Observation{}, fmt.Errorf("migration-command backend case %s: %w", test.name, err)
		}
		publicStdout.Write(execution.stdout)
		publicStderr.Write(execution.stderr)
		environmentOccurrences := migrationCommandActualEnvironmentOccurrences(execution.environment, secret)
		if environmentOccurrences != 1 {
			return protocol.Observation{}, fmt.Errorf("migration-command backend case %s environment pass-through occurrences=%d", test.name, environmentOccurrences)
		}
		environmentPassThroughOccurrences += environmentOccurrences
		participants, err := migrationCommandActualParticipants(project)
		if err != nil {
			return protocol.Observation{}, err
		}
		if len(participants) != index+1 {
			return protocol.Observation{}, fmt.Errorf(
				"migration-command backend case %s participant inventory=%d, want %d",
				test.name, len(participants), index+1,
			)
		}
		for _, participant := range participants {
			if err := migrationCommandAssertActualParticipant(participant); err != nil {
				return protocol.Observation{}, err
			}
		}
		matched := migrationCommandActualParticipantsByMode(participants, test.mode)
		if len(matched) != 1 {
			return protocol.Observation{}, fmt.Errorf("migration-command backend case %s participants=%d", test.name, len(matched))
		}
		participant := matched[0]
		if !slicesEqual(participant.trace, []string{"backend_open_attempt"}) {
			return protocol.Observation{}, fmt.Errorf("migration-command backend case %s trace=%v", test.name, participant.trace)
		}
		access, err := os.ReadFile(filepath.Join(project.trace, "secret-access-"+strconv.Itoa(participant.pid)))
		secretDigest := sha256.Sum256([]byte(migrationcommandworker.SecretDigestDomain + secret))
		wantAccess := fmt.Sprintf(
			"pid=%d\nppid=%d\nmode=%s\ndigest=%s\n",
			participant.pid, participant.parentPID, participant.mode,
			migrationCommandActualDigestText(secretDigest),
		)
		if err != nil || string(access) != wantAccess {
			return protocol.Observation{}, fmt.Errorf("migration-command backend case %s secret access marker is invalid", test.name)
		}
		childAccesses++
		protocolOccurrences += bytes.Count(participant.privateRequest, []byte(secret))
		protocolOccurrences += bytes.Count(participant.privateReply, []byte(secret))
		privateStderrOccurrences += bytes.Count(participant.privateStderr, []byte(secret))
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"case":     protocol.String(test.name),
			"category": protocol.String(execution.report.MigrateFailure.Category),
			"code":     protocol.String(execution.report.MigrateFailure.Code),
			"exit":     protocol.String("nonzero"),
		}))
	}
	finalParticipants, err := migrationCommandActualParticipants(project)
	if err != nil {
		return protocol.Observation{}, err
	}
	if len(finalParticipants) != len(tests) {
		return protocol.Observation{}, fmt.Errorf("migration-command backend final participant inventory=%d", len(finalParticipants))
	}
	for _, test := range tests {
		if len(migrationCommandActualParticipantsByMode(finalParticipants, test.mode)) != 1 {
			return protocol.Observation{}, fmt.Errorf("migration-command backend final mode inventory for %s is not exact", test.name)
		}
	}
	stdoutOccurrences := bytes.Count(publicStdout.Bytes(), []byte(secret))
	stderrOccurrences := bytes.Count(publicStderr.Bytes(), []byte(secret))
	argvAndStdinOccurrences := migrationCommandActualArgvAndStdinOccurrences(secret)
	parentAmbientOccurrences := strings.Count(os.Getenv(migrationcommandworker.EnvironmentSecret), secret)
	if err := migrationCommandAssertActualDirectoryEmpty(project.workspace); err != nil {
		return protocol.Observation{}, err
	}
	universeOccurrences, err := migrationCommandActualArtifactOccurrences(project.universe, secret)
	if err != nil {
		return protocol.Observation{}, err
	}
	rawCausesPublished := stdoutOccurrences+stderrOccurrences+protocolOccurrences+privateStderrOccurrences+universeOccurrences != 0
	globalParsedSecret := argvAndStdinOccurrences+parentAmbientOccurrences != 0
	if environmentPassThroughOccurrences != len(tests) || childAccesses != len(tests) {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command secret boundary pass-through=%d child_accesses=%d",
			environmentPassThroughOccurrences, childAccesses,
		)
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases":                    protocol.List(cases...),
		"global_cli_parses_secret": protocol.Boolean(globalParsedSecret),
		"raw_causes_published":     protocol.Boolean(rawCausesPublished),
	})
	metricFields := map[string]protocol.Value{
		"cases":                       migrationCommandInt(len(cases)),
		"protocol_secret_occurrences": migrationCommandInt(protocolOccurrences),
		"stderr_secret_occurrences":   migrationCommandInt(stderrOccurrences),
		"stdout_secret_occurrences":   migrationCommandInt(stdoutOccurrences),
	}
	candidate := migrationCommandObservation(contract, &result, nil, nil, protocol.Object(metricFields))
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return protocol.Observation{}, err
	}
	artifactOccurrences := bytes.Count(encoded, []byte(secret)) + universeOccurrences
	metricFields["artifact_secret_occurrences"] = migrationCommandInt(artifactOccurrences)
	candidate = migrationCommandObservation(contract, &result, nil, nil, protocol.Object(metricFields))
	if artifactOccurrences != 0 || rawCausesPublished || globalParsedSecret ||
		argvAndStdinOccurrences != 0 || parentAmbientOccurrences != 0 || privateStderrOccurrences != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command secret boundary = artifact:%d raw:%t global:%t argv_stdin:%d parent:%d private_stderr:%d",
			artifactOccurrences, rawCausesPublished, globalParsedSecret,
			argvAndStdinOccurrences, parentAmbientOccurrences, privateStderrOccurrences,
		)
	}
	return candidate, nil
}

func migrationCommandInterruptCleanup(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	project, err := newMigrationCommandActualProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, project.close()) }()
	sources := migrationCommandSources()
	databasePath := filepath.Join(project.root, "interrupt.sqlite3")
	if err := migrationCommandApplySources(ctx, databasePath, sources[:1]); err != nil {
		return protocol.Observation{}, fmt.Errorf("seed migration-command interrupt prefix: %w", err)
	}
	interrupt := make(chan struct{})
	completed := make(chan migrationCommandActualExecution, 1)
	go func() {
		completed <- project.runActual(ctx, migrationcommandworker.ModeInterrupt, databasePath, "", interrupt)
	}()
	childPID, err := migrationCommandWaitForActualMarker(ctx, project.trace, "interrupt-ready")
	if err != nil {
		return protocol.Observation{}, err
	}
	interruptedAt := time.Now()
	close(interrupt)
	var execution migrationCommandActualExecution
	select {
	case execution = <-completed:
	case <-ctx.Done():
		return protocol.Observation{}, ctx.Err()
	}
	graceElapsed := time.Since(interruptedAt)
	if err := migrationCommandActualFailure(execution, migrateprotocol.CategoryProcess, migrateprotocol.CodeProjectInterrupted, 1, 0); err != nil {
		return protocol.Observation{}, err
	}
	participants, err := migrationCommandActualParticipants(project)
	if err != nil {
		return protocol.Observation{}, err
	}
	interrupted := migrationCommandActualParticipantsByMode(participants, migrationcommandworker.ModeInterrupt)
	if len(participants) != 1 || len(interrupted) != 1 || interrupted[0].pid != childPID {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command interrupt participant inventory = total:%d interrupted:%d ready_pid:%d",
			len(participants), len(interrupted), childPID,
		)
	}
	participant := interrupted[0]
	if err := migrationCommandAssertActualParticipant(participant); err != nil {
		return protocol.Observation{}, err
	}
	processGroupErr := migrationCommandWaitForProcessGroupAbsent(ctx, participant.pid)
	processGroupAbsent := processGroupErr == nil
	if !processGroupAbsent {
		return protocol.Observation{}, processGroupErr
	}
	snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	exactErr := migrationCommandAssertExactSQLitePrefix(snapshot)
	prefixOnly := exactErr == nil
	rollbackCalls := migrationCommandTraceCount(participant.trace, "rollback_complete:command/"+migrationCommandMiddle)
	sessionCloseCalls := migrationCommandTraceCount(participant.trace, "session_close_complete")
	backendCloseCalls := migrationCommandTraceCount(participant.trace, "backend_close_complete")
	signalCancellations := migrationCommandTraceCount(participant.trace, "signal_context_canceled")
	childReaps := execution.report.DirectChildReaps - execution.report.BuildCalls
	processResidue := boolToInt(!processGroupAbsent)
	workspaceResidue := execution.report.ResidualTemp
	wantBegins := []string{"command/" + migrationCommandMiddle}
	wantCreates := []string{migrationCommandMiddleTable}
	exactAttempts := slicesEqual(migrationCommandTracePrefix(participant.trace, "begin_attempt:"), wantBegins) &&
		slicesEqual(migrationCommandTracePrefix(participant.trace, "create_complete:"), wantCreates) &&
		len(migrationCommandTracePrefix(participant.trace, "record_attempt:")) == 0 &&
		migrationCommandTraceCount(participant.trace, "session_attempt") == 1 &&
		migrationCommandTraceCount(participant.trace, "snapshot_attempt") == 1 &&
		migrationCommandTraceCount(participant.trace, "snapshot_complete:1") == 1
	if !prefixOnly || !exactAttempts || rollbackCalls != 1 || sessionCloseCalls != 1 || backendCloseCalls != 1 ||
		childReaps != 1 || execution.report.GroupSIGINTAttempts != 1 || execution.report.GroupSIGKILLAttempts != 0 ||
		processResidue != 0 || workspaceResidue != 0 || signalCancellations != 1 || graceElapsed >= 15*time.Second {
		return protocol.Observation{}, fmt.Errorf(
			"migration-command interrupt cleanup = prefix:%t attempts:%t rollback:%d session:%d backend:%d reap:%d sigint:%d kill:%d process_residue:%d workspace_residue:%d cancellation:%d grace:%s",
			prefixOnly, exactAttempts, rollbackCalls, sessionCloseCalls, backendCloseCalls,
			childReaps, execution.report.GroupSIGINTAttempts, execution.report.GroupSIGKILLAttempts,
			processResidue, workspaceResidue, signalCancellations, graceElapsed,
		)
	}
	dbState := protocol.Object(map[string]protocol.Value{
		"backend_close":        protocol.String("completed"),
		"child_reap":           protocol.String("direct"),
		"current_step_effects": protocol.String("absent"),
		"force_kill":           protocol.Boolean(execution.report.GroupSIGKILLAttempts != 0),
		"history":              protocol.String("exact_committed_prefix"),
		"process_residue":      migrationCommandInt(processResidue),
		"rollback":             protocol.String("completed"),
		"session_close":        protocol.String("completed"),
	})
	return migrationCommandObservation(contract, nil, migrationCommandObservedError(execution.report.MigrateFailure.Category, execution.report.MigrateFailure.Code), &dbState, protocol.Object(map[string]protocol.Value{
		"backend_close_calls":          migrationCommandInt(backendCloseCalls),
		"child_reaps":                  migrationCommandInt(childReaps),
		"force_kills":                  migrationCommandInt(execution.report.GroupSIGKILLAttempts),
		"rollback_calls":               migrationCommandInt(rollbackCalls),
		"session_close_calls":          migrationCommandInt(sessionCloseCalls),
		"signal_context_cancellations": migrationCommandInt(signalCancellations),
	})), nil
}
