//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const migrationTargetPlanSecret = "godj-target-plan-private-secret-canary"

type migrationTargetPlanRegistration struct {
	id      string
	phase   protocol.Phase
	handler scenarioHandler
}

var migrationTargetPlanScenarioRegistry = map[string]migrationTargetPlanRegistration{
	"godj.migration.target_plan.target_argv_and_pre_io_rejection": {
		id: "MIG-119", phase: protocol.PhaseEnvironment, handler: migrationTargetPlanArgv,
	},
	"django.migration.target_plan.named_forward_closure": {
		id: "MIG-120", phase: protocol.PhaseEvaluation, handler: migrationTargetPlanNamedForward,
	},
	"django.migration.target_plan.named_reverse_descendants": {
		id: "MIG-121", phase: protocol.PhaseEvaluation, handler: migrationTargetPlanNamedReverse,
	},
	"django.migration.target_plan.app_zero_cross_app_dependents": {
		id: "MIG-122", phase: protocol.PhaseEvaluation, handler: migrationTargetPlanAppZero,
	},
	"godj.migration.target_plan.target_noop_and_legacy_zero": {
		id: "MIG-123", phase: protocol.PhaseEvaluation, handler: migrationTargetPlanNoop,
	},
	"godj.migration.target_plan.plan_exact_and_no_mutation": {
		id: "MIG-124", phase: protocol.PhaseEvaluation, handler: migrationTargetPlanReadOnly,
	},
	"godj.migration.target_plan.preview_drift_fresh_execute": {
		id: "MIG-125", phase: protocol.PhaseCommit, handler: migrationTargetPlanFreshExecute,
	},
	"godj.migration.target_plan.reverse_middle_failure_resume": {
		id: "MIG-126", phase: protocol.PhaseRollback, handler: migrationTargetPlanReverseResume,
	},
	"godj.migration.target_plan.reverse_commit_outcomes": {
		id: "MIG-127", phase: protocol.PhaseCommit, handler: migrationTargetPlanCommitOutcomes,
	},
	"godj.migration.target_plan.project_protocol_and_ownership": {
		id: "MIG-128", phase: protocol.PhaseEnvironment, handler: migrationTargetPlanOwnership,
	},
}

func migrationTargetPlanScenarioHandler(scenario string) (scenarioHandler, bool) {
	registration, ok := migrationTargetPlanScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		if ctx == nil {
			return protocol.Observation{}, errors.New("migration-target-plan scenario context is nil")
		}
		if contract.ID != registration.id {
			return protocol.Observation{}, fmt.Errorf("migration-target-plan scenario %q contract id %q; want %q", scenario, contract.ID, registration.id)
		}
		if contract.Scenario != scenario {
			return protocol.Observation{}, fmt.Errorf("migration-target-plan scenario %q contract scenario %q", scenario, contract.Scenario)
		}
		if contract.Phase != registration.phase {
			return protocol.Observation{}, fmt.Errorf("migration-target-plan scenario %q phase %q; want %q", scenario, contract.Phase, registration.phase)
		}
		return registration.handler(ctx, contract)
	}, true
}

var (
	targetPlanA1 = migrations.MigrationKey{App: "alpha", Name: "0001_initial"}
	targetPlanA2 = migrations.MigrationKey{App: "alpha", Name: "0002_second"}
	targetPlanA3 = migrations.MigrationKey{App: "alpha", Name: "0003_third"}
	targetPlanB1 = migrations.MigrationKey{App: "beta", Name: "0001_direct_dependent"}
	targetPlanC1 = migrations.MigrationKey{App: "charlie", Name: "0001_descendant_dependent"}
	targetPlanG1 = migrations.MigrationKey{App: "gamma", Name: "0001_unrelated"}
)

func migrationTargetPlanDefinitions(includeDescendant bool) []migrations.Migration {
	definitions := []migrations.Migration{
		migrationTargetPlanMigration(targetPlanA1, nil, "target_alpha_initial"),
		migrationTargetPlanMigration(targetPlanA2, []migrations.MigrationKey{targetPlanA1}, "target_alpha_second"),
		migrationTargetPlanMigration(targetPlanA3, []migrations.MigrationKey{targetPlanA2}, "target_alpha_third"),
		migrationTargetPlanMigration(targetPlanB1, []migrations.MigrationKey{targetPlanA1}, "target_beta_direct"),
	}
	if includeDescendant {
		definitions = append(definitions, migrationTargetPlanMigration(targetPlanC1, []migrations.MigrationKey{targetPlanA3}, "target_charlie_descendant"))
	}
	return append(definitions, migrationTargetPlanMigration(targetPlanG1, nil, "target_gamma_unrelated"))
}

func migrationTargetPlanMigration(key migrations.MigrationKey, dependencies []migrations.MigrationKey, table string) migrations.Migration {
	return migrations.Migration{
		App: key.App, Name: key.Name, Dependencies: append([]migrations.MigrationKey(nil), dependencies...),
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: key.App,
			Model: ir.Model{
				Name: table, GoName: migrationTargetPlanGoName(key), DBTable: table,
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			},
		}},
	}
}

func migrationTargetPlanGoName(key migrations.MigrationKey) string {
	value := strings.NewReplacer("_", " ", "-", " ").Replace(key.App + " " + key.Name)
	parts := strings.Fields(value)
	for index := range parts {
		if len(parts[index]) != 0 {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, "")
}

func migrationTargetPlanSources(definitions []migrations.Migration) ([]definition.Source, error) {
	sources := make([]definition.Source, len(definitions))
	for index, value := range definitions {
		document, err := definition.Encode(definition.Producer{Name: "godj-target-plan-actual", Version: "1"}, value)
		if err != nil {
			return nil, err
		}
		sources[index] = definition.Source{
			SourceID: fmt.Sprintf("actual/%02d_%s_%s.godj.json", index, value.App, value.Name),
			Document: document,
		}
	}
	return sources, nil
}

func migrationTargetPlanLoaded(definitions []migrations.Migration) (migrations.LoadedDefinitionSet, error) {
	sources, err := migrationTargetPlanSources(definitions)
	if err != nil {
		return migrations.LoadedDefinitionSet{}, err
	}
	loaded, _, err := definition.Load(sources...)
	return loaded, err
}

func migrationTargetPlanApplied(keys ...migrations.MigrationKey) (migrations.AppliedState, error) {
	return migrations.NewAppliedState(append([]migrations.MigrationKey(nil), keys...)...)
}

func migrationTargetPlanRows(steps []migrations.PlanStep) protocol.Value {
	rows := make([]protocol.Value, len(steps))
	for index, step := range steps {
		rows[index] = protocol.Object(map[string]protocol.Value{
			"app": protocol.String(step.Key.App), "direction": protocol.String(string(step.Direction)), "name": protocol.String(step.Key.Name),
		})
	}
	return protocol.List(rows...)
}

func migrationTargetPlanKeys(keys []migrations.MigrationKey) protocol.Value {
	rows := make([]protocol.Value, len(keys))
	for index, key := range keys {
		rows[index] = protocol.Object(map[string]protocol.Value{"app": protocol.String(key.App), "name": protocol.String(key.Name)})
	}
	return protocol.List(rows...)
}

func migrationTargetPlanStrings(values ...string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = protocol.String(value)
	}
	return protocol.List(items...)
}

func migrationTargetPlanArgvValue(values []string) protocol.Value {
	return migrationTargetPlanStrings(values...)
}

func migrationTargetPlanInt(value int) protocol.Value { return protocol.Integer(strconv.Itoa(value)) }

func migrationTargetPlanObservation(contract protocol.Contract, result, dbState, metrics *protocol.Value) protocol.Observation {
	return protocol.Observation{
		ID: contract.ID, Status: protocol.StatusObserved, Phase: contract.Phase,
		Result: result, DBState: dbState, Metrics: metrics,
	}
}

func migrationTargetPlanPlannerResult(
	contract protocol.Contract,
	name string,
	definitions []migrations.Migration,
	appliedKeys []migrations.MigrationKey,
	targetKeys []migrations.MigrationKey,
	targets ...migrations.Target,
) (protocol.Observation, error) {
	planner, err := migrations.NewPlanner(definitions...)
	if err != nil {
		return protocol.Observation{}, err
	}
	applied, err := migrationTargetPlanApplied(appliedKeys...)
	if err != nil {
		return protocol.Observation{}, err
	}
	steps, err := planner.Plan(applied, targets...)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"applied": migrationTargetPlanKeys(appliedKeys),
		"name":    protocol.String(name),
		"plan":    migrationTargetPlanRows(steps),
		"targets": migrationTargetPlanKeys(targetKeys),
	})
	return migrationTargetPlanObservation(contract, &result, nil, nil), nil
}

func migrationTargetPlanNamedForward(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return migrationTargetPlanPlannerResult(
		contract, "named_forward_closure", migrationTargetPlanDefinitions(true), nil,
		[]migrations.MigrationKey{targetPlanA3}, migrations.NamedTarget(targetPlanA3),
	)
}

func migrationTargetPlanNamedReverse(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	applied := []migrations.MigrationKey{targetPlanA1, targetPlanA2, targetPlanA3, targetPlanB1, targetPlanC1, targetPlanG1}
	return migrationTargetPlanPlannerResult(
		contract, "named_reverse_descendants", migrationTargetPlanDefinitions(true), applied,
		[]migrations.MigrationKey{targetPlanA1}, migrations.NamedTarget(targetPlanA1),
	)
}

func migrationTargetPlanAppZero(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	definitions := migrationTargetPlanDefinitions(false)
	appliedKeys := []migrations.MigrationKey{targetPlanA1, targetPlanA2, targetPlanA3, targetPlanB1, targetPlanG1}
	planner, err := migrations.NewPlanner(definitions...)
	if err != nil {
		return protocol.Observation{}, err
	}
	applied, err := migrationTargetPlanApplied(appliedKeys...)
	if err != nil {
		return protocol.Observation{}, err
	}
	steps, err := planner.Plan(applied, migrations.KnownAppZeroTarget("alpha"))
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"applied": migrationTargetPlanKeys(appliedKeys),
		"name":    protocol.String("app_zero_cross_app_dependents"),
		"plan":    migrationTargetPlanRows(steps),
		"targets": protocol.List(protocol.Object(map[string]protocol.Value{
			"app": protocol.String("alpha"), "name": protocol.Null(),
		})),
	})
	return migrationTargetPlanObservation(contract, &result, nil, nil), nil
}

type migrationTargetPlanProject struct {
	universe    string
	root        string
	unselected  string
	descriptor  string
	environment []string
}

func newMigrationTargetPlanProject() (migrationTargetPlanProject, error) {
	universe, err := os.MkdirTemp("", "godj-target-plan-actual-")
	if err != nil {
		return migrationTargetPlanProject{}, err
	}
	fail := func(cause error) (migrationTargetPlanProject, error) {
		return migrationTargetPlanProject{}, errors.Join(cause, os.RemoveAll(universe))
	}
	project := migrationTargetPlanProject{
		universe: universe,
		root:     filepath.Join(universe, "project"), unselected: filepath.Join(universe, "unselected"),
	}
	for _, relative := range []string{"project", "unselected", "home", "tmp"} {
		if err := os.Mkdir(filepath.Join(universe, relative), 0o700); err != nil {
			return fail(err)
		}
	}
	project.descriptor = filepath.Join(project.root, "godj.toml")
	if err := os.WriteFile(project.descriptor, []byte("format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n"), 0o600); err != nil {
		return fail(err)
	}
	project.environment = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(universe, "home"),
		"TMPDIR=" + filepath.Join(universe, "tmp"), "GOTELEMETRY=off",
	}
	return project, nil
}

func (project migrationTargetPlanProject) close() error { return os.RemoveAll(project.universe) }

type migrationTargetPlanTransactionMode uint8

const (
	migrationTargetPlanCommitNormal migrationTargetPlanTransactionMode = iota
	migrationTargetPlanCommitUnknown
	migrationTargetPlanCommitCleanupFailure
)

type migrationTargetPlanBackend struct {
	mu sync.Mutex

	records []migrationbackend.AppliedMigration

	transactionModes []migrationTargetPlanTransactionMode
	failDeleteTable  string
	failDeleteOnce   bool
	closeErr         error

	openSessionCalls   int
	historyReads       int
	beginCalls         int
	schemaMutations    int
	recorderMutations  int
	applicationChanges int
	revisionMutations  int
	commitCalls        int
	rollbackCalls      int
	sessionCloseCalls  int
	backendCloseCalls  int

	beginSteps []migrations.PlanStep
}

func newMigrationTargetPlanBackend(keys ...migrations.MigrationKey) *migrationTargetPlanBackend {
	backend := &migrationTargetPlanBackend{}
	for _, key := range keys {
		backend.records = append(backend.records, migrationbackend.AppliedMigration{App: key.App, Name: key.Name})
	}
	return backend
}

func (*migrationTargetPlanBackend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	return migrationbackend.MigrationCapabilities{}
}

func (backend *migrationTargetPlanBackend) OpenRevisionFencedSession(context.Context) (migrationbackend.RevisionFencedSession, error) {
	backend.mu.Lock()
	backend.openSessionCalls++
	backend.mu.Unlock()
	return &migrationTargetPlanSession{backend: backend}, nil
}

func (backend *migrationTargetPlanBackend) Close() error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.backendCloseCalls++
	return backend.closeErr
}

type migrationTargetPlanSession struct{ backend *migrationTargetPlanBackend }

func (session *migrationTargetPlanSession) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	session.backend.mu.Lock()
	defer session.backend.mu.Unlock()
	session.backend.historyReads++
	return append([]migrationbackend.AppliedMigration(nil), session.backend.records...), nil
}

func (session *migrationTargetPlanSession) BeginMigration(
	_ context.Context,
	transition migrationbackend.HistoryTransition,
	_ migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.backend.mu.Lock()
	defer session.backend.mu.Unlock()
	index := session.backend.beginCalls
	session.backend.beginCalls++
	direction := migrations.DirectionForward
	if transition.Kind == migrationbackend.HistoryTransitionUnapply {
		direction = migrations.DirectionBackward
	}
	session.backend.beginSteps = append(session.backend.beginSteps, migrations.PlanStep{
		Key: migrations.MigrationKey{App: transition.Migration.App, Name: transition.Migration.Name}, Direction: direction,
	})
	mode := migrationTargetPlanCommitNormal
	if index < len(session.backend.transactionModes) {
		mode = session.backend.transactionModes[index]
	}
	return &migrationTargetPlanTransaction{backend: session.backend, transition: transition, mode: mode}, nil
}

func (session *migrationTargetPlanSession) Close(context.Context) error {
	session.backend.mu.Lock()
	session.backend.sessionCloseCalls++
	session.backend.mu.Unlock()
	return nil
}

type migrationTargetPlanTransaction struct {
	backend    *migrationTargetPlanBackend
	transition migrationbackend.HistoryTransition
	mode       migrationTargetPlanTransactionMode
	recorded   bool
}

func (transaction *migrationTargetPlanTransaction) CreateModel(context.Context, ir.Model) error {
	transaction.backend.mu.Lock()
	transaction.backend.schemaMutations++
	transaction.backend.applicationChanges++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationTargetPlanTransaction) DeleteModel(_ context.Context, model ir.Model) error {
	transaction.backend.mu.Lock()
	defer transaction.backend.mu.Unlock()
	transaction.backend.schemaMutations++
	transaction.backend.applicationChanges++
	if transaction.backend.failDeleteTable == model.DBTable && transaction.backend.failDeleteOnce {
		transaction.backend.failDeleteOnce = false
		return errors.New(migrationTargetPlanSecret)
	}
	return nil
}

func (transaction *migrationTargetPlanTransaction) AddField(context.Context, ir.Model, ir.Field) error {
	transaction.backend.mu.Lock()
	transaction.backend.schemaMutations++
	transaction.backend.applicationChanges++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationTargetPlanTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	transaction.backend.mu.Lock()
	transaction.backend.schemaMutations++
	transaction.backend.applicationChanges++
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationTargetPlanTransaction) RecordApplied(context.Context, string, string) error {
	transaction.backend.mu.Lock()
	transaction.backend.recorderMutations++
	transaction.recorded = true
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationTargetPlanTransaction) RecordUnapplied(context.Context, string, string) error {
	transaction.backend.mu.Lock()
	transaction.backend.recorderMutations++
	transaction.recorded = true
	transaction.backend.mu.Unlock()
	return nil
}

func (transaction *migrationTargetPlanTransaction) CommitFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.backend.mu.Lock()
	defer transaction.backend.mu.Unlock()
	transaction.backend.commitCalls++
	switch transaction.mode {
	case migrationTargetPlanCommitUnknown:
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.New(migrationTargetPlanSecret)
	case migrationTargetPlanCommitCleanupFailure:
		transaction.applyCommittedLocked()
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, errors.New(migrationTargetPlanSecret)
	default:
		transaction.applyCommittedLocked()
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, nil
	}
}

func (transaction *migrationTargetPlanTransaction) applyCommittedLocked() {
	if !transaction.recorded {
		return
	}
	key := migrationbackend.AppliedMigration{App: transaction.transition.Migration.App, Name: transaction.transition.Migration.Name}
	switch transaction.transition.Kind {
	case migrationbackend.HistoryTransitionApply:
		for _, existing := range transaction.backend.records {
			if existing == key {
				return
			}
		}
		transaction.backend.records = append(transaction.backend.records, key)
	case migrationbackend.HistoryTransitionUnapply:
		for index, existing := range transaction.backend.records {
			if existing == key {
				transaction.backend.records = append(transaction.backend.records[:index], transaction.backend.records[index+1:]...)
				break
			}
		}
	}
	transaction.backend.revisionMutations++
}

func (transaction *migrationTargetPlanTransaction) Rollback(context.Context) error {
	transaction.backend.mu.Lock()
	transaction.backend.rollbackCalls++
	transaction.backend.mu.Unlock()
	return nil
}

type migrationTargetPlanBackendSnapshot struct {
	records []migrationbackend.AppliedMigration

	openSessionCalls   int
	historyReads       int
	beginCalls         int
	schemaMutations    int
	recorderMutations  int
	applicationChanges int
	revisionMutations  int
	commitCalls        int
	rollbackCalls      int
	sessionCloseCalls  int
	backendCloseCalls  int
	beginSteps         []migrations.PlanStep
}

func (backend *migrationTargetPlanBackend) snapshot() migrationTargetPlanBackendSnapshot {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return migrationTargetPlanBackendSnapshot{
		records:          append([]migrationbackend.AppliedMigration(nil), backend.records...),
		openSessionCalls: backend.openSessionCalls, historyReads: backend.historyReads,
		beginCalls: backend.beginCalls, schemaMutations: backend.schemaMutations,
		recorderMutations: backend.recorderMutations, applicationChanges: backend.applicationChanges,
		revisionMutations: backend.revisionMutations, commitCalls: backend.commitCalls,
		rollbackCalls: backend.rollbackCalls, sessionCloseCalls: backend.sessionCloseCalls,
		backendCloseCalls: backend.backendCloseCalls, beginSteps: append([]migrations.PlanStep(nil), backend.beginSteps...),
	}
}

type migrationTargetPlanProcessOwner struct {
	sources []definition.Source
	backend *migrationTargetPlanBackend
	openErr error

	mu          sync.Mutex
	linked      linked.Report
	wire        []byte
	openCalls   int
	stages      []productcheck.ProcessStage
	commands    []productcheck.Command
	runner      func(context.Context, <-chan struct{}, productcheck.Command) productcheck.ProcessResult
	internalErr error
}

func (owner *migrationTargetPlanProcessOwner) Execute(
	ctx context.Context,
	interrupt <-chan struct{},
	stage productcheck.ProcessStage,
	command productcheck.Command,
) productcheck.ProcessResult {
	owner.mu.Lock()
	owner.stages = append(owner.stages, stage)
	owner.commands = append(owner.commands, productcheck.Command{
		Dir: command.Dir, Argv: append([]string(nil), command.Argv...), Env: append([]string(nil), command.Env...), Stdin: append([]byte(nil), command.Stdin...),
	})
	owner.mu.Unlock()
	if stage == productcheck.BuildStage {
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	}
	if owner.runner != nil {
		return owner.runner(ctx, interrupt, command)
	}
	if stage != productcheck.MigrateRunnerStage {
		return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	}
	var output bytes.Buffer
	report, err := linked.RunMigrate(ctx, linked.MigrateConfig{
		MigrationDefinitionSources: cloneMigrationStatusSources(owner.sources),
		OpenMigrationBackend: func(context.Context) (linked.MigrationBackend, error) {
			owner.mu.Lock()
			owner.openCalls++
			owner.mu.Unlock()
			return owner.backend, owner.openErr
		},
	}, command.Argv[1:], bytes.NewReader(command.Stdin), &output)
	owner.mu.Lock()
	owner.linked = report
	owner.wire = append([]byte(nil), output.Bytes()...)
	owner.internalErr = err
	owner.mu.Unlock()
	if err != nil {
		return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	}
	wire := append([]byte(nil), output.Bytes()...)
	return productcheck.ProcessResult{
		Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
		StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(wire)},
	}
}

type migrationTargetPlanWriterMode uint8

const (
	migrationTargetPlanWriterNormal migrationTargetPlanWriterMode = iota
	migrationTargetPlanWriterShort
	migrationTargetPlanWriterError
)

type migrationTargetPlanWriter struct {
	mode   migrationTargetPlanWriterMode
	writes int
	buffer bytes.Buffer
}

func (writer *migrationTargetPlanWriter) Write(payload []byte) (int, error) {
	writer.writes++
	switch writer.mode {
	case migrationTargetPlanWriterNormal:
		return writer.buffer.Write(payload)
	case migrationTargetPlanWriterShort:
		if len(payload) == 0 {
			return 0, nil
		}
		return writer.buffer.Write(payload[:len(payload)-1])
	case migrationTargetPlanWriterError:
		return 0, errors.New(migrationTargetPlanSecret)
	default:
		return 0, errors.New("invalid migration target plan writer")
	}
}

type migrationTargetPlanExecution struct {
	report  productcheck.MigrateReport
	linked  linked.Report
	owner   *migrationTargetPlanProcessOwner
	backend *migrationTargetPlanBackend
	stdout  *migrationTargetPlanWriter
	stderr  *migrationTargetPlanWriter
	wire    []byte
}

func (project migrationTargetPlanProject) run(
	ctx context.Context,
	cwd string,
	args []string,
	sources []definition.Source,
	backend *migrationTargetPlanBackend,
	configure func(*migrationTargetPlanProcessOwner),
	stdoutMode migrationTargetPlanWriterMode,
) (migrationTargetPlanExecution, error) {
	owner := &migrationTargetPlanProcessOwner{sources: cloneMigrationStatusSources(sources), backend: backend}
	if configure != nil {
		configure(owner)
	}
	stdout := &migrationTargetPlanWriter{mode: stdoutMode}
	stderr := &migrationTargetPlanWriter{}
	report := productcheck.RunMigrate(productcheck.MigrateInvocation{
		Context: ctx, CWD: cwd, Args: append([]string(nil), args...),
		Environment: append([]string(nil), project.environment...), Stdout: stdout, Stderr: stderr, Backend: owner,
	})
	owner.mu.Lock()
	execution := migrationTargetPlanExecution{
		report: report, linked: owner.linked, owner: owner, backend: backend, stdout: stdout, stderr: stderr,
		wire: append([]byte(nil), owner.wire...),
	}
	ownerErr := owner.internalErr
	owner.mu.Unlock()
	return execution, ownerErr
}

func migrationTargetPlanRequestTargetValue(target migrateprotocol.Target) protocol.Value {
	fields := map[string]protocol.Value{"kind": protocol.String(string(target.Kind))}
	if target.App != "" {
		fields["app"] = protocol.String(target.App)
	}
	if target.Name != "" {
		fields["name"] = protocol.String(target.Name)
	}
	return protocol.Object(fields)
}

func migrationTargetPlanCapturedRequest(owner *migrationTargetPlanProcessOwner) (migrateprotocol.Request, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	for index := len(owner.commands) - 1; index >= 0; index-- {
		if len(owner.commands[index].Argv) < 2 || owner.commands[index].Argv[1] != migrateprotocol.PrivateArgument {
			continue
		}
		request, failure, failed, err := migrateprotocol.ReadRequest(bytes.NewReader(owner.commands[index].Stdin))
		if err != nil {
			return migrateprotocol.Request{}, err
		}
		if failed {
			return migrateprotocol.Request{}, fmt.Errorf("captured target-plan request failed: %+v", failure)
		}
		return request, nil
	}
	return migrateprotocol.Request{}, errors.New("target-plan process owner has no private migrate request")
}

func migrationTargetPlanArgv(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	project, err := newMigrationTargetPlanProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()

	acceptedDefinitions := []migrations.Migration{
		migrationTargetPlanMigration(migrations.MigrationKey{App: "blog", Name: "0001_article"}, nil, "target_blog_article"),
	}
	acceptedSources, err := migrationTargetPlanSources(acceptedDefinitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	acceptedArguments := [][]string{
		{"migrate"},
		{"migrate", "--project", "./godj.toml"},
		{"migrate", "--plan"},
		{"migrate", "--plan", "--project", "./godj.toml"},
		{"migrate", "blog", "0001_article"},
		{"migrate", "blog", "0001_article", "--project", "./godj.toml"},
		{"migrate", "blog", "0001_article", "--plan"},
		{"migrate", "blog", "0001_article", "--plan", "--project", "./godj.toml"},
	}
	accepted := make([]protocol.Value, 0, len(acceptedArguments))
	for _, argv := range acceptedArguments {
		backend := newMigrationTargetPlanBackend()
		execution, err := project.run(ctx, project.root, argv, acceptedSources, backend, nil, migrationTargetPlanWriterNormal)
		if err != nil {
			return protocol.Observation{}, err
		}
		request, err := migrationTargetPlanCapturedRequest(execution.owner)
		if err != nil {
			return protocol.Observation{}, err
		}
		succeeded := execution.report.ExitCode == 0 && !execution.report.HasMigrateFailure &&
			((request.Mode == migrateprotocol.ModeExecute && execution.report.HasMigrateResult) ||
				(request.Mode == migrateprotocol.ModePlan && execution.report.HasMigratePlan))
		if !succeeded || execution.report.BuildCalls != 1 || execution.owner.openCalls != 1 {
			return protocol.Observation{}, fmt.Errorf("target-plan accepted argv %v = report:%+v open:%d", argv, execution.report, execution.owner.openCalls)
		}
		accepted = append(accepted, protocol.Object(map[string]protocol.Value{
			"argv": migrationTargetPlanArgvValue(argv), "mode": protocol.String(string(request.Mode)),
			"target": migrationTargetPlanRequestTargetValue(request.Target),
		}))
	}

	rejectedArguments := []struct {
		name string
		argv []string
	}{
		{name: "app_only", argv: []string{"migrate", "blog"}},
		{name: "permuted_project_plan", argv: []string{"migrate", "--project", "./godj.toml", "--plan"}},
		{name: "repeated_plan", argv: []string{"migrate", "--plan", "--plan"}},
		{name: "double_dash", argv: []string{"migrate", "--", "blog", "0001_article"}},
		{name: "unknown_option", argv: []string{"migrate", "--database", "other"}},
		{name: "leading_dash_app", argv: []string{"migrate", "--blog", "0001_article"}},
		{name: "leading_dash_name", argv: []string{"migrate", "blog", "--0001"}},
	}
	rejected := make([]protocol.Value, 0, len(rejectedArguments))
	backendOpensForRejected, buildsForRejected, discoveriesForRejected := 0, 0, 0
	for _, test := range rejectedArguments {
		backend := newMigrationTargetPlanBackend()
		execution, err := project.run(ctx, project.unselected, test.argv, acceptedSources, backend, nil, migrationTargetPlanWriterNormal)
		if err != nil {
			return protocol.Observation{}, err
		}
		backendOpens := execution.owner.openCalls
		builds := execution.report.BuildCalls
		discoveries := execution.report.DescriptorReads
		backendOpensForRejected += backendOpens
		buildsForRejected += builds
		discoveriesForRejected += discoveries
		if execution.report.ExitCode != 2 || !execution.report.HasMigrateFailure ||
			execution.report.MigrateFailure.Category != migrateprotocol.CategoryCommand ||
			execution.report.MigrateFailure.Code != migrateprotocol.CodeInvalidArguments ||
			backendOpens != 0 || builds != 0 || discoveries != 0 {
			return protocol.Observation{}, fmt.Errorf("target-plan rejected argv %s = report:%+v open:%d", test.name, execution.report, backendOpens)
		}
		rejected = append(rejected, protocol.Object(map[string]protocol.Value{
			"argv": migrationTargetPlanArgvValue(test.argv), "backend_opens": migrationTargetPlanInt(backendOpens),
			"builds": migrationTargetPlanInt(builds), "case": protocol.String(test.name),
			"category": protocol.String(execution.report.MigrateFailure.Category), "code": protocol.String(execution.report.MigrateFailure.Code),
			"project_discoveries": migrationTargetPlanInt(discoveries),
		}))
	}

	prefixKey := migrations.MigrationKey{App: "blog", Name: "0001_initial"}
	prefixSources, err := migrationTargetPlanSources([]migrations.Migration{
		migrationTargetPlanMigration(prefixKey, nil, "target_blog_initial"),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	prefixArgv := []string{"migrate", "blog", "0001"}
	prefixBackend := newMigrationTargetPlanBackend()
	prefixExecution, err := project.run(ctx, project.root, prefixArgv, prefixSources, prefixBackend, nil, migrationTargetPlanWriterNormal)
	if err != nil {
		return protocol.Observation{}, err
	}
	prefixSnapshot := prefixBackend.snapshot()
	if prefixExecution.report.ExitCode != 1 || !prefixExecution.report.HasMigrateFailure ||
		prefixExecution.report.MigrateFailure.Category != migrateprotocol.CategoryPlan ||
		prefixExecution.report.MigrateFailure.Code != string(migrations.CodeTargetNotFound) ||
		prefixExecution.report.DescriptorReads != 1 || prefixExecution.report.BuildCalls != 1 ||
		prefixExecution.owner.openCalls != 1 || prefixSnapshot.historyReads != 1 || prefixSnapshot.beginCalls != 0 {
		return protocol.Observation{}, fmt.Errorf("target-plan exact prefix miss = report:%+v backend:%+v", prefixExecution.report, prefixSnapshot)
	}
	postDiscovery := protocol.List(protocol.Object(map[string]protocol.Value{
		"argv": migrationTargetPlanArgvValue(prefixArgv), "backend_opens": migrationTargetPlanInt(prefixExecution.owner.openCalls),
		"builds": migrationTargetPlanInt(prefixExecution.report.BuildCalls), "case": protocol.String("prefix_looking_exact_miss"),
		"catalog_exact_name": protocol.String(prefixKey.Name), "category": protocol.String(prefixExecution.report.MigrateFailure.Category),
		"code": protocol.String(prefixExecution.report.MigrateFailure.Code), "history_reads": migrationTargetPlanInt(prefixSnapshot.historyReads),
		"migration_begins": migrationTargetPlanInt(prefixSnapshot.beginCalls), "project_discoveries": migrationTargetPlanInt(prefixExecution.report.DescriptorReads),
		"requested_name": protocol.String("0001"),
	}))

	result := protocol.Object(map[string]protocol.Value{
		"accepted": protocol.List(accepted...), "exact_public_families": migrationTargetPlanInt(len(accepted)),
		"migration_name_resolution": protocol.String("exact_only"), "option_permutations": protocol.String("rejected"),
		"post_discovery_rejections": postDiscovery, "rejected": protocol.List(rejected...),
		"zero_reserved_spelling": protocol.String("zero"),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"accepted_forms": migrationTargetPlanInt(len(accepted)), "backend_opens_for_rejected": migrationTargetPlanInt(backendOpensForRejected),
		"builds_for_rejected": migrationTargetPlanInt(buildsForRejected), "post_discovery_target_not_found_cases": migrationTargetPlanInt(1),
		"project_discoveries_for_rejected": migrationTargetPlanInt(discoveriesForRejected), "rejected_forms": migrationTargetPlanInt(len(rejected)),
	})
	return migrationTargetPlanObservation(contract, &result, nil, &metrics), nil
}

func migrationTargetPlanBlogDefinitions(count int) []migrations.Migration {
	keys := []migrations.MigrationKey{
		{App: "blog", Name: "0001_article"},
		{App: "blog", Name: "0002_editor"},
		{App: "blog", Name: "0003_publish"},
		{App: "blog", Name: "0004_archive"},
	}
	tables := []string{"target_blog_article", "target_blog_editor", "target_blog_publish", "target_blog_archive"}
	if count < 0 || count > len(keys) {
		return nil
	}
	result := make([]migrations.Migration, count)
	for index := 0; index < count; index++ {
		var dependencies []migrations.MigrationKey
		if index != 0 {
			dependencies = []migrations.MigrationKey{keys[index-1]}
		}
		result[index] = migrationTargetPlanMigration(keys[index], dependencies, tables[index])
	}
	return result
}

func migrationTargetPlanDefinitionKeys(definitions []migrations.Migration) []migrations.MigrationKey {
	keys := make([]migrations.MigrationKey, len(definitions))
	for index := range definitions {
		keys[index] = definitions[index].Key()
	}
	return keys
}

func migrationTargetPlanErrorValue(err error) (protocol.Value, protocol.Value, error) {
	if err == nil {
		return protocol.Null(), protocol.Null(), nil
	}
	var planning *migrations.PlanningError
	var execution *migrations.Error
	switch {
	case errors.As(err, &planning) && planning != nil:
		return protocol.String(string(planning.Category)), protocol.String(string(planning.Code)), nil
	case errors.As(err, &execution) && execution != nil:
		return protocol.String(string(execution.Category)), protocol.String(string(execution.Code)), nil
	default:
		return protocol.Value{}, protocol.Value{}, fmt.Errorf("unclassified migration-target-plan error: %w", err)
	}
}

func migrationTargetPlanNoop(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	definitions := migrationTargetPlanBlogDefinitions(2)
	loaded, err := migrationTargetPlanLoaded(definitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	allApplied := migrationTargetPlanDefinitionKeys(definitions)
	tests := []struct {
		name    string
		backend *migrationTargetPlanBackend
		request migrations.LifecycleRequest
	}{
		{
			name: "applied_named_leaf", backend: newMigrationTargetPlanBackend(allApplied...),
			request: migrations.TargetedLifecycleRequest(migrations.NamedTarget(allApplied[len(allApplied)-1])),
		},
		{
			name: "legacy_zero_unknown_app", backend: newMigrationTargetPlanBackend(allApplied...),
			request: migrations.TargetedLifecycleRequest(migrations.ZeroTarget("unknown")),
		},
		{
			name: "public_known_zero_unknown_app", backend: newMigrationTargetPlanBackend(allApplied...),
			request: migrations.TargetedLifecycleRequest(migrations.KnownAppZeroTarget("unknown")),
		},
	}
	caseValues := make([]protocol.Value, 0, len(tests))
	historyReads, beginCalls, targetNotFound := 0, 0, 0
	for _, test := range tests {
		plan, planErr := (migrations.Executor{Backend: test.backend}).Plan(ctx, loaded, test.request)
		snapshot := test.backend.snapshot()
		historyReads += snapshot.historyReads
		beginCalls += snapshot.beginCalls
		category, code, err := migrationTargetPlanErrorValue(planErr)
		if err != nil {
			return protocol.Observation{}, err
		}
		if test.name == "public_known_zero_unknown_app" {
			var planning *migrations.PlanningError
			if !errors.As(planErr, &planning) || planning == nil || planning.Category != migrations.CategoryPlan || planning.Code != migrations.CodeTargetNotFound || plan != nil {
				return protocol.Observation{}, fmt.Errorf("known zero unknown = plan:%v err:%v", plan, planErr)
			}
			targetNotFound++
		} else if planErr != nil || len(plan) != 0 || plan == nil {
			return protocol.Observation{}, fmt.Errorf("target-plan noop %s = plan:%v err:%v", test.name, plan, planErr)
		}
		planValue := migrationTargetPlanRows(plan)
		if planErr != nil {
			planValue = protocol.Null()
		}
		caseValues = append(caseValues, protocol.Object(map[string]protocol.Value{
			"begin_calls": migrationTargetPlanInt(snapshot.beginCalls), "case": protocol.String(test.name),
			"category": category, "code": code, "plan": planValue,
		}))
	}
	if historyReads != len(tests) || beginCalls != 0 || targetNotFound != 1 {
		return protocol.Observation{}, fmt.Errorf("target-plan noop counters history=%d begin=%d misses=%d", historyReads, beginCalls, targetNotFound)
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(caseValues...), "legacy_zero_unknown_contract": protocol.String("empty_plan"),
		"public_zero_requires_known_app": protocol.Boolean(true),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"begin_calls": migrationTargetPlanInt(beginCalls), "history_reads": migrationTargetPlanInt(historyReads),
		"target_not_found_cases": migrationTargetPlanInt(targetNotFound),
	})
	return migrationTargetPlanObservation(contract, &result, nil, &metrics), nil
}

func migrationTargetPlanProtocolRows(steps []migrations.PlanStep) []migrateprotocol.PlanRow {
	rows := make([]migrateprotocol.PlanRow, len(steps))
	for index, step := range steps {
		direction := migrateprotocol.DirectionForward
		if step.Direction == migrations.DirectionBackward {
			direction = migrateprotocol.DirectionBackward
		}
		rows[index] = migrateprotocol.PlanRow{App: step.Key.App, Name: step.Key.Name, Direction: direction}
	}
	return rows
}

func migrationTargetPlanHistoryValue(records []migrationbackend.AppliedMigration) protocol.Value {
	values := make([]string, len(records))
	for index, record := range records {
		values[index] = record.App + "." + record.Name
	}
	return migrationTargetPlanStrings(values...)
}

func migrationTargetPlanKeyStrings(keys ...migrations.MigrationKey) protocol.Value {
	values := make([]string, len(keys))
	for index, key := range keys {
		values[index] = key.App + "." + key.Name
	}
	return migrationTargetPlanStrings(values...)
}

func migrationTargetPlanStepKeyStrings(steps []migrations.PlanStep) protocol.Value {
	values := make([]string, len(steps))
	for index, step := range steps {
		values[index] = step.Key.App + "." + step.Key.Name
	}
	return migrationTargetPlanStrings(values...)
}

func migrationTargetPlanSnapshotValue(records []migrationbackend.AppliedMigration, revision int) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"application": protocol.String("unchanged"), "history": migrationTargetPlanHistoryValue(records),
		"revision": migrationTargetPlanInt(revision), "schema": protocol.String("unchanged"),
	})
}

func migrationTargetPlanReadOnly(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	definitions := migrationTargetPlanBlogDefinitions(2)
	keys := migrationTargetPlanDefinitionKeys(definitions)
	loaded, err := migrationTargetPlanLoaded(definitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	backend := newMigrationTargetPlanBackend(keys...)
	tests := []struct {
		name   string
		target migrations.MigrationKey
	}{
		{name: "nonempty", target: keys[0]},
		{name: "empty", target: keys[1]},
	}
	resultCases := make([]protocol.Value, 0, len(tests))
	databaseCases := make([]protocol.Value, 0, len(tests))
	for _, test := range tests {
		before := backend.snapshot()
		plan, err := (migrations.Executor{Backend: backend}).Plan(
			ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(test.target)),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		encoded, err := migrateprotocol.EncodePublicPlan(migrationTargetPlanProtocolRows(plan))
		if err != nil {
			return protocol.Observation{}, err
		}
		after := backend.snapshot()
		if !reflect.DeepEqual(before.records, after.records) || after.beginCalls != before.beginCalls ||
			after.schemaMutations != before.schemaMutations || after.recorderMutations != before.recorderMutations ||
			after.revisionMutations != before.revisionMutations || after.applicationChanges != before.applicationChanges ||
			after.historyReads != before.historyReads+1 || after.sessionCloseCalls != before.sessionCloseCalls+1 {
			return protocol.Observation{}, fmt.Errorf("target-plan read-only case %s mutated: before=%+v after=%+v", test.name, before, after)
		}
		resultCases = append(resultCases, protocol.Object(map[string]protocol.Value{
			"case": protocol.String(test.name), "plan": migrationTargetPlanRows(plan), "stdout": protocol.String(string(encoded)),
		}))
		beforeValue := migrationTargetPlanSnapshotValue(before.records, len(before.records))
		afterValue := migrationTargetPlanSnapshotValue(after.records, len(after.records))
		databaseCases = append(databaseCases, protocol.Object(map[string]protocol.Value{
			"after": afterValue, "before": beforeValue, "case": protocol.String(test.name),
		}))
	}
	snapshot := backend.snapshot()
	if snapshot.historyReads != 2 || snapshot.beginCalls != 0 || snapshot.sessionCloseCalls != 2 {
		return protocol.Observation{}, fmt.Errorf("target-plan read-only ownership = %+v", snapshot)
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(resultCases...), "plan_is_execution_authority": protocol.Boolean(false),
	})
	dbState := protocol.Object(map[string]protocol.Value{"cases": protocol.List(databaseCases...)})
	metrics := protocol.Object(map[string]protocol.Value{
		"application_mutations": migrationTargetPlanInt(snapshot.applicationChanges), "history_reads": migrationTargetPlanInt(snapshot.historyReads),
		"migration_begins": migrationTargetPlanInt(snapshot.beginCalls), "recorder_mutations": migrationTargetPlanInt(snapshot.recorderMutations),
		"revision_mutations": migrationTargetPlanInt(snapshot.revisionMutations), "schema_mutations": migrationTargetPlanInt(snapshot.schemaMutations),
		"session_closes": migrationTargetPlanInt(snapshot.sessionCloseCalls),
	})
	return migrationTargetPlanObservation(contract, &result, &dbState, &metrics), nil
}

func migrationTargetPlanFreshExecute(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	definitions := migrationTargetPlanBlogDefinitions(2)
	keys := migrationTargetPlanDefinitionKeys(definitions)
	loaded, err := migrationTargetPlanLoaded(definitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	backend := newMigrationTargetPlanBackend(keys[0])
	request := migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[1]))
	preview, err := (migrations.Executor{Backend: backend}).Plan(ctx, loaded, request)
	if err != nil {
		return protocol.Observation{}, err
	}
	afterPreview := backend.snapshot()
	previewMutations := afterPreview.beginCalls + afterPreview.schemaMutations + afterPreview.recorderMutations +
		afterPreview.revisionMutations + afterPreview.applicationChanges
	if previewMutations != 0 || len(preview) != 1 || preview[0].Key != keys[1] || preview[0].Direction != migrations.DirectionForward {
		return protocol.Observation{}, fmt.Errorf("target-plan preview = %v snapshot=%+v", preview, afterPreview)
	}

	backend.mu.Lock()
	backend.records = append(backend.records, migrationbackend.AppliedMigration{App: keys[1].App, Name: keys[1].Name})
	backend.mu.Unlock()
	afterDrift := backend.snapshot()
	_, err = (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, request)
	if err != nil {
		return protocol.Observation{}, err
	}
	afterExecute := backend.snapshot()
	executePlan := afterExecute.beginSteps[len(afterPreview.beginSteps):]
	if len(executePlan) != 0 || afterExecute.historyReads != 2 || afterExecute.beginCalls != 0 ||
		!reflect.DeepEqual(afterExecute.records, afterDrift.records) {
		return protocol.Observation{}, fmt.Errorf("target-plan fresh execute = plan:%v snapshot:%+v", executePlan, afterExecute)
	}
	result := protocol.Object(map[string]protocol.Value{
		"execute_plan": migrationTargetPlanRows(executePlan), "preview_plan": migrationTargetPlanRows(preview),
		"preview_token_accepted": protocol.Boolean(false), "replanned_from_fresh_history": protocol.Boolean(true),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"after_execute_history":      migrationTargetPlanHistoryValue(afterExecute.records),
		"after_preview_history":      migrationTargetPlanHistoryValue(afterPreview.records),
		"after_writer_drift_history": migrationTargetPlanHistoryValue(afterDrift.records),
		"preview_mutations":          migrationTargetPlanInt(previewMutations),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"automatic_retries": migrationTargetPlanInt(0), "execute_history_reads": migrationTargetPlanInt(afterExecute.historyReads - afterPreview.historyReads),
		"execute_migration_begins": migrationTargetPlanInt(afterExecute.beginCalls - afterPreview.beginCalls),
		"preview_history_reads":    migrationTargetPlanInt(afterPreview.historyReads), "preview_migration_begins": migrationTargetPlanInt(afterPreview.beginCalls),
	})
	return migrationTargetPlanObservation(contract, &result, &dbState, &metrics), nil
}

func migrationTargetPlanReverseResume(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	definitions := migrationTargetPlanBlogDefinitions(4)
	keys := migrationTargetPlanDefinitionKeys(definitions)
	loaded, err := migrationTargetPlanLoaded(definitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	request := migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[0]))
	first := newMigrationTargetPlanBackend(keys...)
	firstInitial := first.snapshot()
	fullPlan, err := (migrations.Executor{Backend: first}).Plan(ctx, loaded, request)
	if err != nil {
		return protocol.Observation{}, err
	}
	first.failDeleteTable = definitions[2].Operations[0].(migrations.CreateModel).Model.DBTable
	first.failDeleteOnce = true
	_, firstErr := (migrations.Executor{Backend: first}).Migrate(ctx, loaded, request)
	firstSnapshot := first.snapshot()
	var migrationErr *migrations.Error
	if !errors.As(firstErr, &migrationErr) || migrationErr == nil || migrationErr.Category != migrations.CategoryExecution || migrationErr.Code != migrations.CodeOperationFailed {
		return protocol.Observation{}, fmt.Errorf("target-plan first reverse error = %v", firstErr)
	}
	if firstSnapshot.commitCalls != 1 || firstSnapshot.rollbackCalls != 1 || len(firstSnapshot.beginSteps) != 2 || len(firstSnapshot.records) != 3 {
		return protocol.Observation{}, fmt.Errorf("target-plan first reverse snapshot = %+v", firstSnapshot)
	}
	firstObserved, err := migrationTargetPlanObserveExecution(fullPlan, firstInitial.records, firstSnapshot)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("target-plan first reverse observations: %w", err)
	}

	remainingKeys := migrationTargetPlanRecordKeys(firstSnapshot.records)
	resume := newMigrationTargetPlanBackend(remainingKeys...)
	resumeInitial := resume.snapshot()
	resumePlan, err := (migrations.Executor{Backend: resume}).Plan(ctx, loaded, request)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, err = (migrations.Executor{Backend: resume}).Migrate(ctx, loaded, request)
	if err != nil {
		return protocol.Observation{}, err
	}
	resumeSnapshot := resume.snapshot()
	if resumeSnapshot.commitCalls != 2 || resumeSnapshot.rollbackCalls != 0 || len(resumeSnapshot.records) != 1 ||
		resumeSnapshot.records[0] != (migrationbackend.AppliedMigration{App: keys[0].App, Name: keys[0].Name}) {
		return protocol.Observation{}, fmt.Errorf("target-plan resume snapshot = %+v", resumeSnapshot)
	}
	resumeObserved, err := migrationTargetPlanObserveExecution(resumePlan, resumeInitial.records, resumeSnapshot)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("target-plan fresh reverse observations: %w", err)
	}
	unstartedTailStarted := migrationTargetPlanKeysIntersect(firstObserved.unstarted, migrationTargetPlanStepKeys(firstSnapshot.beginSteps))
	firstCase := protocol.Object(map[string]protocol.Value{
		"case": protocol.String("first_process"), "category": protocol.String(string(migrationErr.Category)),
		"code": protocol.String(string(migrationErr.Code)), "committed": migrationTargetPlanKeyStrings(firstObserved.committed...),
		"plan": migrationTargetPlanStepKeyStrings(fullPlan), "rolled_back": migrationTargetPlanKeyStrings(firstObserved.rolledBack...),
		"unstarted": migrationTargetPlanKeyStrings(firstObserved.unstarted...),
	})
	resumeCase := protocol.Object(map[string]protocol.Value{
		"case": protocol.String("fresh_resume"), "category": protocol.Null(), "code": protocol.Null(),
		"committed": migrationTargetPlanKeyStrings(resumeObserved.committed...), "plan": migrationTargetPlanStepKeyStrings(resumePlan),
		"rolled_back": migrationTargetPlanKeyStrings(resumeObserved.rolledBack...), "unstarted": migrationTargetPlanKeyStrings(resumeObserved.unstarted...),
	})
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(firstCase, resumeCase), "unstarted_tail_started": protocol.Boolean(unstartedTailStarted),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"after_failure_history":      migrationTargetPlanHistoryValue(firstSnapshot.records),
		"after_resume_history":       migrationTargetPlanHistoryValue(resumeSnapshot.records),
		"durable_prefix_preserved":   protocol.Boolean(migrationTargetPlanObservedHistoryExact(firstInitial.records, firstSnapshot.records, firstObserved)),
		"initial_history":            migrationTargetPlanHistoryValue(firstInitial.records),
		"rolled_back_step_preserved": protocol.Boolean(migrationTargetPlanRecordsContainAll(firstSnapshot.records, firstObserved.rolledBack)),
		"unstarted_tail_preserved":   protocol.Boolean(migrationTargetPlanRecordsContainAll(firstSnapshot.records, firstObserved.unstarted)),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"automatic_retries": migrationTargetPlanInt(0), "first_process_reverse_commits": migrationTargetPlanInt(firstSnapshot.commitCalls),
		"first_process_reverse_rollbacks": migrationTargetPlanInt(firstSnapshot.rollbackCalls), "first_process_unstarted_steps": migrationTargetPlanInt(len(firstObserved.unstarted)),
		"fresh_processes": migrationTargetPlanInt(1), "fresh_resume_reverse_commits": migrationTargetPlanInt(resumeSnapshot.commitCalls),
		"fresh_resume_reverse_rollbacks": migrationTargetPlanInt(resumeSnapshot.rollbackCalls),
		"reverse_commits":                migrationTargetPlanInt(firstSnapshot.commitCalls + resumeSnapshot.commitCalls),
		"reverse_rollbacks":              migrationTargetPlanInt(firstSnapshot.rollbackCalls + resumeSnapshot.rollbackCalls),
		"started_steps":                  migrationTargetPlanInt(len(firstSnapshot.beginSteps) + len(resumeSnapshot.beginSteps)), "unstarted_steps": migrationTargetPlanInt(len(firstObserved.unstarted) + len(resumeObserved.unstarted)),
	})
	return migrationTargetPlanObservation(contract, &result, &dbState, &metrics), nil
}

type migrationTargetPlanExecutionObservation struct {
	committed  []migrations.MigrationKey
	rolledBack []migrations.MigrationKey
	unstarted  []migrations.MigrationKey
}

func migrationTargetPlanObserveExecution(
	plan []migrations.PlanStep,
	before []migrationbackend.AppliedMigration,
	after migrationTargetPlanBackendSnapshot,
) (migrationTargetPlanExecutionObservation, error) {
	if len(after.beginSteps) != after.beginCalls {
		return migrationTargetPlanExecutionObservation{}, fmt.Errorf("begun step count %d differs from begin calls %d", len(after.beginSteps), after.beginCalls)
	}
	if len(after.beginSteps) > len(plan) {
		return migrationTargetPlanExecutionObservation{}, errors.New("begun step count exceeds plan")
	}
	for index, begun := range after.beginSteps {
		if begun != plan[index] {
			return migrationTargetPlanExecutionObservation{}, fmt.Errorf("begun step %d = %+v, want plan prefix %+v", index, begun, plan[index])
		}
	}
	observed := migrationTargetPlanExecutionObservation{}
	for index, step := range plan {
		if index >= len(after.beginSteps) {
			observed.unstarted = append(observed.unstarted, step.Key)
			continue
		}
		beforeContains := migrationTargetPlanRecordsContain(before, step.Key)
		afterContains := migrationTargetPlanRecordsContain(after.records, step.Key)
		switch step.Direction {
		case migrations.DirectionBackward:
			if !beforeContains {
				return migrationTargetPlanExecutionObservation{}, fmt.Errorf("reverse step %+v was absent before execution", step.Key)
			}
			if afterContains {
				observed.rolledBack = append(observed.rolledBack, step.Key)
			} else {
				observed.committed = append(observed.committed, step.Key)
			}
		case migrations.DirectionForward:
			if beforeContains {
				return migrationTargetPlanExecutionObservation{}, fmt.Errorf("forward step %+v was present before execution", step.Key)
			}
			if afterContains {
				observed.committed = append(observed.committed, step.Key)
			} else {
				observed.rolledBack = append(observed.rolledBack, step.Key)
			}
		default:
			return migrationTargetPlanExecutionObservation{}, fmt.Errorf("step %+v has invalid direction %q", step.Key, step.Direction)
		}
	}
	if len(observed.committed) != after.commitCalls || len(observed.rolledBack) != after.rollbackCalls {
		return migrationTargetPlanExecutionObservation{}, fmt.Errorf(
			"observed outcomes commit:%d rollback:%d, counters commit:%d rollback:%d",
			len(observed.committed), len(observed.rolledBack), after.commitCalls, after.rollbackCalls,
		)
	}
	return observed, nil
}

func migrationTargetPlanObservedHistoryExact(
	before, after []migrationbackend.AppliedMigration,
	observed migrationTargetPlanExecutionObservation,
) bool {
	expected := append([]migrationbackend.AppliedMigration(nil), before...)
	for _, key := range observed.committed {
		for index, record := range expected {
			if record.App == key.App && record.Name == key.Name {
				expected = append(expected[:index], expected[index+1:]...)
				break
			}
		}
	}
	return reflect.DeepEqual(expected, after)
}

func migrationTargetPlanRecordKeys(records []migrationbackend.AppliedMigration) []migrations.MigrationKey {
	keys := make([]migrations.MigrationKey, len(records))
	for index, record := range records {
		keys[index] = migrations.MigrationKey{App: record.App, Name: record.Name}
	}
	return keys
}

func migrationTargetPlanStepKeys(steps []migrations.PlanStep) []migrations.MigrationKey {
	keys := make([]migrations.MigrationKey, len(steps))
	for index, step := range steps {
		keys[index] = step.Key
	}
	return keys
}

func migrationTargetPlanKeysIntersect(left, right []migrations.MigrationKey) bool {
	for _, candidate := range left {
		for _, other := range right {
			if candidate == other {
				return true
			}
		}
	}
	return false
}

func migrationTargetPlanRecordsContainAll(records []migrationbackend.AppliedMigration, keys []migrations.MigrationKey) bool {
	for _, key := range keys {
		if !migrationTargetPlanRecordsContain(records, key) {
			return false
		}
	}
	return true
}

func migrationTargetPlanRecordsContain(records []migrationbackend.AppliedMigration, key migrations.MigrationKey) bool {
	for _, record := range records {
		if record.App == key.App && record.Name == key.Name {
			return true
		}
	}
	return false
}

type migrationTargetPlanOutcomeCase struct {
	name             string
	category         string
	code             string
	history          string
	automaticRetries int
	rollbackCalls    int
	successPublished bool
	records          []migrationbackend.AppliedMigration
	commitUnknown    bool
}

func migrationTargetPlanCommitOutcomes(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	definitions := migrationTargetPlanBlogDefinitions(2)
	keys := migrationTargetPlanDefinitionKeys(definitions)
	loaded, err := migrationTargetPlanLoaded(definitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	request := migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[0]))
	tests := []struct {
		name            string
		mode            migrationTargetPlanTransactionMode
		failDeleteTable string
		history         string
	}{
		{name: "commit_outcome_unknown", mode: migrationTargetPlanCommitUnknown, history: "unknown"},
		{name: "confirmed_rollback", failDeleteTable: definitions[1].Operations[0].(migrations.CreateModel).Model.DBTable, history: "preserved_before_step"},
		{name: "committed_cleanup_failure", mode: migrationTargetPlanCommitCleanupFailure, history: "committed_successor"},
	}
	observed := make([]migrationTargetPlanOutcomeCase, 0, len(tests))
	for _, test := range tests {
		backend := newMigrationTargetPlanBackend(keys...)
		backend.transactionModes = []migrationTargetPlanTransactionMode{test.mode}
		if test.failDeleteTable != "" {
			backend.failDeleteTable = test.failDeleteTable
			backend.failDeleteOnce = true
		}
		_, migrateErr := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, request)
		category, code, err := migrationTargetPlanErrorValue(migrateErr)
		if err != nil {
			return protocol.Observation{}, err
		}
		categoryText, codeText := migrationTargetPlanStringScalar(category), migrationTargetPlanStringScalar(code)
		snapshot := backend.snapshot()
		retries := migrationTargetPlanAutomaticRetries(snapshot, 1, 1)
		current := migrationTargetPlanOutcomeCase{
			name: test.name, category: categoryText, code: codeText, history: test.history,
			automaticRetries: retries, rollbackCalls: snapshot.rollbackCalls, successPublished: migrateErr == nil,
			records: snapshot.records, commitUnknown: test.name == "commit_outcome_unknown",
		}
		if snapshot.beginCalls != 1 || snapshot.historyReads != 1 || snapshot.sessionCloseCalls != 1 || retries != 0 || migrateErr == nil {
			return protocol.Observation{}, fmt.Errorf("target-plan commit outcome %s = err:%v snapshot:%+v", test.name, migrateErr, snapshot)
		}
		switch test.name {
		case "commit_outcome_unknown":
			if categoryText != migrateprotocol.CategoryTransaction || codeText != string(migrations.CodeCommitOutcomeUnknown) ||
				snapshot.commitCalls != 1 || snapshot.rollbackCalls != 0 || len(snapshot.records) != 2 {
				return protocol.Observation{}, fmt.Errorf("target-plan unknown outcome = %+v", current)
			}
		case "confirmed_rollback":
			if categoryText != migrateprotocol.CategoryExecution || codeText != string(migrations.CodeOperationFailed) ||
				snapshot.commitCalls != 0 || snapshot.rollbackCalls != 1 || len(snapshot.records) != 2 {
				return protocol.Observation{}, fmt.Errorf("target-plan rolled-back outcome = %+v", current)
			}
		case "committed_cleanup_failure":
			if categoryText != migrateprotocol.CategoryTransaction || codeText != string(migrations.CodeCommitCleanupFailed) ||
				snapshot.commitCalls != 1 || snapshot.rollbackCalls != 0 || len(snapshot.records) != 1 ||
				!migrationTargetPlanRecordsContain(snapshot.records, keys[0]) {
				return protocol.Observation{}, fmt.Errorf("target-plan committed-cleanup outcome = %+v", current)
			}
		}
		observed = append(observed, current)
	}
	caseValues := make([]protocol.Value, len(observed))
	unknownRollbacks, automaticRetries := 0, 0
	for index, current := range observed {
		caseValues[index] = protocol.Object(map[string]protocol.Value{
			"automatic_retries": migrationTargetPlanInt(current.automaticRetries), "case": protocol.String(current.name),
			"category": protocol.String(current.category), "code": protocol.String(current.code),
			"history": protocol.String(current.history), "reported_success": protocol.Boolean(current.successPublished),
			"rollback_after_outcome": migrationTargetPlanInt(current.rollbackCalls),
		})
		automaticRetries += current.automaticRetries
		if current.commitUnknown {
			unknownRollbacks += current.rollbackCalls
		}
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(caseValues...), "reconciliation_required_after_unknown": protocol.Boolean(true),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"committed_cleanup_history_preserved":  protocol.Boolean(len(observed[2].records) == 1),
		"confirmed_rollback_history_preserved": protocol.Boolean(len(observed[1].records) == 2),
		"unknown_history_guessed":              protocol.Boolean(false),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"automatic_retries": migrationTargetPlanInt(automaticRetries), "cases": migrationTargetPlanInt(len(observed)),
		"unknown_rollbacks": migrationTargetPlanInt(unknownRollbacks),
	})
	return migrationTargetPlanObservation(contract, &result, &dbState, &metrics), nil
}

func migrationTargetPlanAutomaticRetries(snapshot migrationTargetPlanBackendSnapshot, expectedHistoryReads, expectedBegins int) int {
	retries := snapshot.openSessionCalls - 1
	retries += snapshot.historyReads - expectedHistoryReads
	retries += snapshot.beginCalls - expectedBegins
	if retries < 0 {
		return 0
	}
	return retries
}

func migrationTargetPlanStringScalar(value protocol.Value) string {
	if value.Type != protocol.ValueString || value.Text == nil {
		return ""
	}
	return *value.Text
}

const (
	migrationTargetPlanActualModeEnvironment     = "GODJ_TARGET_PLAN_ACTUAL_MODE"
	migrationTargetPlanActualTraceEnvironment    = "GODJ_TARGET_PLAN_ACTUAL_TRACE"
	migrationTargetPlanActualResponseEnvironment = "GODJ_TARGET_PLAN_ACTUAL_RESPONSE"
	migrationTargetPlanActualSecretEnvironment   = "GODJ_TARGET_PLAN_ACTUAL_SECRET"
	migrationTargetPlanActualBytesEnvironment    = "GODJ_TARGET_PLAN_ACTUAL_BYTES"

	migrationTargetPlanActualCancelMode   = "cancellation"
	migrationTargetPlanActualForcedMode   = "forced_cancellation"
	migrationTargetPlanActualOverflowMode = "response_overflow"
	migrationTargetPlanActualStderrMode   = "stderr_redaction"
)

type migrationTargetPlanActualExecution struct {
	report productcheck.MigrateReport
	stdout []byte
	stderr []byte
}

type migrationTargetPlanActualOwnershipEvidence struct {
	cancellation           migrationTargetPlanActualExecution
	cancellationRunnerPID  int
	cancellationChildPID   int
	processGroupsRemaining int
	forcedCancellation     migrationTargetPlanActualExecution
	forcedRunnerPID        int
	forcedChildPID         int
	forcedGroupsRemaining  int
	responseOverflow       migrationTargetPlanActualExecution
	responseOverflowBytes  int
	stderrRedaction        migrationTargetPlanActualExecution
	stderrSecretBytes      int
	artifactSecrets        int
}

func migrationTargetPlanActualOwnership(
	ctx context.Context,
) (evidence migrationTargetPlanActualOwnershipEvidence, resultErr error) {
	project, err := newMigrationCommandActualProject()
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	if err := writeMigrationCommandActualFile(
		filepath.Join(project.root, "cmd", "site", "main.go"),
		[]byte(migrationTargetPlanActualRunnerSource),
	); err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}

	cancellation, runnerPID, childPID, remaining, err := migrationTargetPlanRunActualCancellation(
		ctx, project, migrationTargetPlanActualCancelMode, "target-plan-cancel", 0,
	)
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	evidence.cancellation = cancellation
	evidence.cancellationRunnerPID = runnerPID
	evidence.cancellationChildPID = childPID
	evidence.processGroupsRemaining = remaining
	forced, forcedRunnerPID, forcedChildPID, forcedRemaining, err := migrationTargetPlanRunActualCancellation(
		ctx, project, migrationTargetPlanActualForcedMode, "target-plan-force", 1,
	)
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	evidence.forcedCancellation = forced
	evidence.forcedRunnerPID = forcedRunnerPID
	evidence.forcedChildPID = forcedChildPID
	evidence.forcedGroupsRemaining = forcedRemaining

	overflow := migrationTargetPlanRunActual(ctx, project, migrationTargetPlanActualOverflowMode, "", "")
	overflowPID, err := migrationCommandWaitForActualMarker(ctx, project.trace, "target-plan-overflow")
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	overflowMarker, err := migrationCommandReadActualFile(project.trace, "target-plan-overflow", overflowPID)
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	overflowBytes, err := migrationTargetPlanParseActualCount(migrationTargetPlanActualMarkerField(overflowMarker), "bytes")
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	if err := migrationCommandWaitForProcessGroupAbsent(ctx, overflowPID); err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	if overflowBytes != migrateprotocol.MaxResponseBytes+1 ||
		overflow.report.MigrateFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: migrateprotocol.CodeInvalidResponse}) ||
		!overflow.report.HasMigrateFailure || overflow.report.HasMigratePlan || overflow.report.HasMigrateResult ||
		overflow.report.RunnerCalls != 1 || overflow.report.RunnerResponseWrites != 1 ||
		overflow.report.RunnerStdoutRetainedBytes != migrateprotocol.MaxResponseBytes || !overflow.report.RunnerStdoutTruncated ||
		overflow.report.UserStdoutWrites != 0 || overflow.report.UserStderrWrites != 1 ||
		overflow.report.DirectChildReaps != 2 || len(overflow.stdout) != 0 ||
		!bytes.Equal(overflow.stderr, []byte(migrateprotocol.CategoryProtocol+"/"+migrateprotocol.CodeInvalidResponse+"\n")) {
		return migrationTargetPlanActualOwnershipEvidence{}, fmt.Errorf(
			"target-plan actual response overflow = bytes:%d report:%+v stdout:%d stderr:%q",
			overflowBytes, overflow.report, len(overflow.stdout), overflow.stderr,
		)
	}
	evidence.responseOverflow = overflow
	evidence.responseOverflowBytes = overflowBytes

	privatePlan, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan,
		Plan: make([]migrateprotocol.PlanRow, 0),
	}})
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	stderr := migrationTargetPlanRunActual(
		ctx,
		project,
		migrationTargetPlanActualStderrMode,
		base64.StdEncoding.EncodeToString(privatePlan),
		migrationTargetPlanSecret,
	)
	stderrPID, err := migrationCommandWaitForActualMarker(ctx, project.trace, "target-plan-stderr")
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	stderrMarker, err := migrationCommandReadActualFile(project.trace, "target-plan-stderr", stderrPID)
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	stderrFields := strings.Fields(string(stderrMarker))
	if len(stderrFields) != 2 {
		return migrationTargetPlanActualOwnershipEvidence{}, errors.New("target-plan stderr marker shape is invalid")
	}
	stderrBytes, err := migrationTargetPlanParseActualCount(stderrFields[0], "bytes")
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	stderrDigest, err := migrationTargetPlanParseActualDigest(stderrFields[1], "sha256")
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	wantStderrDigest := sha256.Sum256([]byte(migrationTargetPlanSecret))
	if err := migrationCommandWaitForProcessGroupAbsent(ctx, stderrPID); err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	publicPlan, err := migrateprotocol.EncodePublicPlan(make([]migrateprotocol.PlanRow, 0))
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	if stderrBytes != len(migrationTargetPlanSecret) || stderrDigest != hex.EncodeToString(wantStderrDigest[:]) || stderr.report.ExitCode != 0 ||
		!stderr.report.HasMigratePlan || stderr.report.HasMigrateResult || stderr.report.HasMigrateFailure ||
		stderr.report.BuildCalls != 1 || stderr.report.RunnerCalls != 1 || stderr.report.DirectChildReaps != 2 ||
		stderr.report.RunnerStdoutRetainedBytes != len(privatePlan) || stderr.report.RunnerStdoutTruncated ||
		stderr.report.RunnerStderrRetainedBytes != stderrBytes || stderr.report.RunnerStderrTruncated ||
		!stderr.report.RawDiagnosticsDiscarded || stderr.report.UserStdoutWrites != 1 || stderr.report.UserStderrWrites != 0 ||
		!bytes.Equal(stderr.stdout, publicPlan) || len(stderr.stderr) != 0 {
		return migrationTargetPlanActualOwnershipEvidence{}, fmt.Errorf(
			"target-plan actual stderr redaction = bytes:%d report:%+v stdout:%q stderr:%q",
			stderrBytes, stderr.report, stderr.stdout, stderr.stderr,
		)
	}
	artifactSecrets, err := migrationCommandActualArtifactOccurrences(project.universe, migrationTargetPlanSecret)
	if err != nil {
		return migrationTargetPlanActualOwnershipEvidence{}, err
	}
	evidence.stderrRedaction = stderr
	evidence.stderrSecretBytes = stderrBytes
	evidence.artifactSecrets = artifactSecrets
	return evidence, nil
}

func migrationTargetPlanRunActualCancellation(
	ctx context.Context,
	project migrationCommandProject,
	mode string,
	markerPrefix string,
	wantSIGKILL int,
) (migrationTargetPlanActualExecution, int, int, int, error) {
	phaseContext, cancelPhase := context.WithTimeout(ctx, migrationCommandActualTimeout)
	defer cancelPhase()
	runnerContext, cancelRunner := context.WithCancel(phaseContext)
	done := make(chan migrationTargetPlanActualExecution, 1)
	go func() {
		done <- migrationTargetPlanRunActual(runnerContext, project, mode, "", "")
	}()
	abort := func(primary error) (migrationTargetPlanActualExecution, int, int, int, error) {
		cancelRunner()
		cleanupTimer := time.NewTimer(30 * time.Second)
		defer cleanupTimer.Stop()
		select {
		case <-done:
		case <-cleanupTimer.C:
			primary = errors.Join(primary, errors.New("target-plan cancellation cleanup timed out"))
		}
		return migrationTargetPlanActualExecution{}, 0, 0, 0, primary
	}

	runnerPID, err := migrationCommandWaitForActualMarker(phaseContext, project.trace, markerPrefix+"-ready")
	if err != nil {
		return abort(err)
	}
	marker, err := migrationCommandReadActualFile(project.trace, markerPrefix+"-ready", runnerPID)
	if err != nil {
		return abort(err)
	}
	markerFields := strings.Fields(string(marker))
	if len(markerFields) != 2 {
		return abort(errors.New("target-plan cancellation marker shape is invalid"))
	}
	observedRunnerPID, err := migrationTargetPlanParseActualCount(markerFields[0], "runner_pid")
	if err != nil || observedRunnerPID != runnerPID {
		return abort(errors.New("target-plan cancellation runner PID is invalid"))
	}
	childPID, err := migrationTargetPlanParseActualCount(markerFields[1], "child_pid")
	if err != nil || childPID == runnerPID {
		return abort(errors.New("target-plan cancellation child PID is invalid"))
	}
	childMarker, err := migrationCommandReadActualFile(project.trace, markerPrefix+"-child", childPID)
	if err != nil || string(childMarker) != "ready" {
		return abort(errors.New("target-plan cancellation child readiness is invalid"))
	}

	cancelRunner()
	var execution migrationTargetPlanActualExecution
	select {
	case execution = <-done:
	case <-phaseContext.Done():
		return migrationTargetPlanActualExecution{}, 0, 0, 0, phaseContext.Err()
	}
	remaining := 1
	if err := migrationCommandWaitForProcessGroupAbsent(phaseContext, runnerPID); err != nil {
		return migrationTargetPlanActualExecution{}, 0, 0, remaining, err
	}
	remaining = 0
	if execution.report.MigrateFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProcess, Code: migrateprotocol.CodeProjectCanceled}) ||
		!execution.report.HasMigrateFailure || execution.report.HasMigratePlan || execution.report.HasMigrateResult ||
		execution.report.BuildCalls != 1 || execution.report.RunnerCalls != 1 || execution.report.DirectChildReaps != 2 ||
		execution.report.GroupSIGINTAttempts != 1 || execution.report.GroupSIGKILLAttempts != wantSIGKILL ||
		execution.report.CleanupFailed != 0 || execution.report.ResidualTemp != 0 ||
		execution.report.UserStdoutWrites != 0 || execution.report.UserStderrWrites != 1 || len(execution.stdout) != 0 ||
		!bytes.Equal(execution.stderr, []byte(migrateprotocol.CategoryProcess+"/"+migrateprotocol.CodeProjectCanceled+"\n")) {
		return migrationTargetPlanActualExecution{}, 0, 0, remaining, fmt.Errorf(
			"target-plan actual cancellation = report:%+v runner:%d child:%d stdout:%d stderr:%q",
			execution.report, runnerPID, childPID, len(execution.stdout), execution.stderr,
		)
	}
	return execution, runnerPID, childPID, remaining, nil
}

func migrationTargetPlanRunActual(
	ctx context.Context,
	project migrationCommandProject,
	mode, response, secret string,
) migrationTargetPlanActualExecution {
	phaseContext, cancelPhase := context.WithTimeout(ctx, migrationCommandActualTimeout)
	defer cancelPhase()
	environment := migrationCommandActualEnvironment(os.Environ())
	environment[migrationTargetPlanActualModeEnvironment] = mode
	environment[migrationTargetPlanActualTraceEnvironment] = project.trace
	if response != "" {
		environment[migrationTargetPlanActualResponseEnvironment] = response
	}
	if secret != "" {
		environment[migrationTargetPlanActualSecretEnvironment] = secret
	}
	if mode == migrationTargetPlanActualOverflowMode {
		environment[migrationTargetPlanActualBytesEnvironment] = strconv.Itoa(migrateprotocol.MaxResponseBytes + 1)
	}
	environment["TMPDIR"] = project.workspace
	environment["GOWORK"] = "off"
	environment["GOTOOLCHAIN"] = "local"
	environment["GOENV"] = "off"
	environment["GOFLAGS"] = ""
	environment["GOCACHEPROG"] = ""
	environment["GOPROXY"] = "off"
	entries := migrationCommandSortedEnvironment(environment)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	report := productcheck.RunMigrate(productcheck.MigrateInvocation{
		Context: phaseContext, CWD: project.root, Args: []string{"migrate", "--plan"}, Environment: entries,
		Stdout: &stdout, Stderr: &stderr,
	})
	return migrationTargetPlanActualExecution{
		report: report, stdout: append([]byte(nil), stdout.Bytes()...), stderr: append([]byte(nil), stderr.Bytes()...),
	}
}

func migrationTargetPlanParseActualCount(field, name string) (int, error) {
	actualName, raw, ok := strings.Cut(field, "=")
	if !ok || actualName != name {
		return 0, errors.New("target-plan actual marker field is invalid")
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || raw != strconv.Itoa(value) {
		return 0, errors.New("target-plan actual marker count is invalid")
	}
	return value, nil
}

func migrationTargetPlanParseActualDigest(field, name string) (string, error) {
	actualName, raw, ok := strings.Cut(field, "=")
	if !ok || actualName != name || len(raw) != sha256.Size*2 {
		return "", errors.New("target-plan actual marker digest is invalid")
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != sha256.Size || raw != hex.EncodeToString(decoded) {
		return "", errors.New("target-plan actual marker digest is invalid")
	}
	return raw, nil
}

func migrationTargetPlanActualMarkerField(document []byte) string {
	return strings.TrimSpace(string(document))
}

const migrationTargetPlanActualRunnerSource = `package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"
)

const (
	modeEnvironment = "GODJ_TARGET_PLAN_ACTUAL_MODE"
	traceEnvironment = "GODJ_TARGET_PLAN_ACTUAL_TRACE"
	responseEnvironment = "GODJ_TARGET_PLAN_ACTUAL_RESPONSE"
	secretEnvironment = "GODJ_TARGET_PLAN_ACTUAL_SECRET"
	bytesEnvironment = "GODJ_TARGET_PLAN_ACTUAL_BYTES"
)

func main() {
	switch os.Getenv(modeEnvironment) {
	case "cancellation":
		blockForCancellation("target-plan-cancel", false)
	case "forced_cancellation":
		blockForCancellation("target-plan-force", true)
	case "response_overflow":
		writeResponseOverflow()
	case "stderr_redaction":
		writeSecretStderrAndResponse()
	default:
		os.Exit(2)
	}
}

func blockForCancellation(markerPrefix string, ignoreInterrupt bool) {
	signals := make(chan os.Signal, 1)
	if ignoreInterrupt {
		signal.Ignore(os.Interrupt)
	} else {
		signal.Notify(signals, os.Interrupt)
	}
	if len(os.Args) == 2 && os.Args[1] == "descendant" {
		writeMarker(markerPrefix+"-child", "ready")
		waitForCancellation(signals, ignoreInterrupt)
		return
	}
	child := exec.Command(os.Args[0], "descendant")
	child.Env = os.Environ()
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(3)
	}
	childMarker := filepath.Join(os.Getenv(traceEnvironment), markerPrefix+"-child-"+strconv.Itoa(child.Process.Pid))
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(childMarker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(4)
		}
		time.Sleep(5 * time.Millisecond)
	}
	writeMarker(markerPrefix+"-ready", "runner_pid="+strconv.Itoa(os.Getpid())+" child_pid="+strconv.Itoa(child.Process.Pid))
	waitForCancellation(signals, ignoreInterrupt)
}

func waitForCancellation(signals <-chan os.Signal, ignoreInterrupt bool) {
	if !ignoreInterrupt {
		<-signals
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

func writeResponseOverflow() {
	total, err := strconv.Atoi(os.Getenv(bytesEnvironment))
	if err != nil || total <= 0 {
		os.Exit(5)
	}
	remaining := total
	chunk := make([]byte, 64<<10)
	for index := range chunk {
		chunk[index] = ' '
	}
	for remaining > 0 {
		count := len(chunk)
		if count > remaining {
			count = remaining
		}
		written, err := os.Stdout.Write(chunk[:count])
		if err != nil || written != count {
			os.Exit(6)
		}
		remaining -= written
	}
	writeMarker("target-plan-overflow", "bytes="+strconv.Itoa(total))
}

func writeSecretStderrAndResponse() {
	secret := os.Getenv(secretEnvironment)
	written, err := fmt.Fprint(os.Stderr, secret)
	if err != nil || written != len(secret) {
		os.Exit(7)
	}
	response, err := base64.StdEncoding.DecodeString(os.Getenv(responseEnvironment))
	if err != nil {
		os.Exit(8)
	}
	digest := sha256.Sum256([]byte(secret))
	writeMarker("target-plan-stderr", "bytes="+strconv.Itoa(written)+" sha256="+hex.EncodeToString(digest[:]))
	written, err = os.Stdout.Write(response)
	if err != nil || written != len(response) {
		os.Exit(9)
	}
}

func writeMarker(prefix, value string) {
	path := filepath.Join(os.Getenv(traceEnvironment), prefix+"-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		os.Exit(10)
	}
}
`

func migrationTargetPlanOwnership(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, resultErr error) {
	project, err := newMigrationTargetPlanProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, project.close()) }()
	definitions := migrationTargetPlanBlogDefinitions(1)
	sources, err := migrationTargetPlanSources(definitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	actual, err := migrationTargetPlanActualOwnership(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}

	executeBackend := newMigrationTargetPlanBackend()
	execute, err := project.run(ctx, project.root, []string{"migrate"}, sources, executeBackend, nil, migrationTargetPlanWriterNormal)
	if err != nil {
		return protocol.Observation{}, err
	}
	if execute.report.ExitCode != 0 || !execute.report.HasMigrateResult || execute.report.HasMigratePlan || execute.report.HasMigrateFailure {
		return protocol.Observation{}, fmt.Errorf("target-plan ownership execute = %+v", execute.report)
	}

	planBackend := newMigrationTargetPlanBackend()
	plan, err := project.run(ctx, project.root, []string{"migrate", "--plan"}, sources, planBackend, nil, migrationTargetPlanWriterNormal)
	if err != nil {
		return protocol.Observation{}, err
	}
	if plan.report.ExitCode != 0 || !plan.report.HasMigratePlan || plan.report.HasMigrateResult || plan.report.HasMigrateFailure {
		return protocol.Observation{}, fmt.Errorf("target-plan ownership plan = %+v", plan.report)
	}

	closeBackend := newMigrationTargetPlanBackend()
	closeBackend.closeErr = errors.New(migrationTargetPlanSecret)
	closeFailure, err := project.run(ctx, project.root, []string{"migrate", "--plan"}, sources, closeBackend, nil, migrationTargetPlanWriterNormal)
	if err != nil {
		return protocol.Observation{}, err
	}
	if closeFailure.report.ExitCode != 3 || !closeFailure.report.HasMigrateFailure || closeFailure.report.HasMigratePlan ||
		closeFailure.report.MigrateFailure != (migrateprotocol.Failure{
			Category: migrateprotocol.CategoryBackend, Code: migrateprotocol.CodeBackendCloseFailed, CleanupFailed: true,
		}) {
		return protocol.Observation{}, fmt.Errorf("target-plan ownership close = %+v", closeFailure.report)
	}

	partialBackend := newMigrationTargetPlanBackend()
	partial, err := project.run(ctx, project.root, []string{"migrate", "--plan"}, sources, partialBackend, func(owner *migrationTargetPlanProcessOwner) {
		owner.runner = func(context.Context, <-chan struct{}, productcheck.Command) productcheck.ProcessResult {
			wire := []byte(`{"protocol_version":2,"status":"ok","result":`)
			return productcheck.ProcessResult{
				Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
				StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(wire)},
			}
		}
	}, migrationTargetPlanWriterNormal)
	if err != nil {
		return protocol.Observation{}, err
	}
	if partial.report.MigrateFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: migrateprotocol.CodeInvalidResponse}) ||
		partial.report.HasMigratePlan || partial.report.UserStdoutWrites != 0 || partial.report.UserStderrWrites != 1 {
		return protocol.Observation{}, fmt.Errorf("target-plan partial ownership = %+v", partial.report)
	}

	shortBackend := newMigrationTargetPlanBackend()
	shortWrite, err := project.run(ctx, project.root, []string{"migrate", "--plan"}, sources, shortBackend, nil, migrationTargetPlanWriterShort)
	if err != nil {
		return protocol.Observation{}, err
	}
	if shortWrite.report.MigrateFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryInternal, Code: migrateprotocol.CodeProjectInternalError}) ||
		shortWrite.report.HasMigratePlan || shortWrite.report.UserStdoutWrites != 1 || shortWrite.report.PartialStdoutWrites != 1 ||
		shortWrite.linked.RunnerResponseWrites != 1 {
		return protocol.Observation{}, fmt.Errorf("target-plan terminal short write = %+v linked=%+v", shortWrite.report, shortWrite.linked)
	}
	canceledGroupsRemaining := actual.processGroupsRemaining + actual.forcedGroupsRemaining
	sigintAttemptsMaximum := max(actual.cancellation.report.GroupSIGINTAttempts, actual.forcedCancellation.report.GroupSIGINTAttempts)
	sigkillAttemptsMaximum := max(actual.cancellation.report.GroupSIGKILLAttempts, actual.forcedCancellation.report.GroupSIGKILLAttempts)

	ownershipCases := []protocol.Value{
		migrationTargetPlanOwnershipSuccessValue("execute_success", migrateprotocol.ModeExecute, execute, "public_result_published"),
		migrationTargetPlanOwnershipSuccessValue("plan_success", migrateprotocol.ModePlan, plan, "public_plan_published"),
		protocol.Object(map[string]protocol.Value{
			"backend_closes": migrationTargetPlanInt(closeFailure.linked.BackendCloseCalls), "backend_opens": migrationTargetPlanInt(closeFailure.linked.BackendOpenCalls),
			"case": protocol.String("outer_close_failure"), "category": protocol.String(closeFailure.report.MigrateFailure.Category),
			"cleanup_failed": protocol.Boolean(closeFailure.report.MigrateFailure.CleanupFailed), "code": protocol.String(closeFailure.report.MigrateFailure.Code),
			"lifecycle_calls": migrationTargetPlanInt(closeFailure.linked.RevisionLifecycleCalls), "mode": protocol.String(string(migrateprotocol.ModePlan)),
			"private_response_writes": migrationTargetPlanInt(closeFailure.linked.RunnerResponseWrites), "public_plan_published": protocol.Boolean(closeFailure.report.HasMigratePlan),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("cancellation_cleanup"), "category": protocol.String(actual.cancellation.report.MigrateFailure.Category),
			"child_started": protocol.Boolean(actual.cancellationRunnerPID > 0 && actual.cancellationChildPID > 0), "cleanup_failed": protocol.Boolean(actual.cancellation.report.CleanupFailed != 0),
			"code": protocol.String(actual.cancellation.report.MigrateFailure.Code), "direct_reaps": migrationTargetPlanInt(actual.cancellation.report.DirectChildReaps - actual.cancellation.report.BuildCalls),
			"mode": protocol.String(string(migrateprotocol.ModePlan)), "partial_response_republished": protocol.Boolean(actual.cancellation.report.UserStdoutWrites != 0),
			"process_group_terminations": migrationTargetPlanInt(actual.cancellation.report.GroupSIGINTAttempts + actual.cancellation.report.GroupSIGKILLAttempts),
			"process_groups_remaining":   migrationTargetPlanInt(canceledGroupsRemaining), "public_plan_published": protocol.Boolean(actual.cancellation.report.HasMigratePlan),
			"sigint_attempts_maximum":  migrationTargetPlanInt(sigintAttemptsMaximum),
			"sigkill_attempts_maximum": migrationTargetPlanInt(sigkillAttemptsMaximum),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("partial_output_non_publication"), "category": protocol.String(partial.report.MigrateFailure.Category),
			"child_started": protocol.Boolean(partial.report.RunnerCalls == 1), "cleanup_failed": protocol.Boolean(partial.report.CleanupFailed != 0),
			"code": protocol.String(partial.report.MigrateFailure.Code), "complete_private_documents": migrationTargetPlanInt(0),
			"direct_reaps": migrationTargetPlanInt(partial.report.DirectChildReaps - partial.report.BuildCalls), "mode": protocol.String(string(migrateprotocol.ModePlan)),
			"partial_private_chunks": migrationTargetPlanInt(1), "partial_response_republished": protocol.Boolean(partial.report.UserStdoutWrites != 0),
			"public_plan_published": protocol.Boolean(partial.report.HasMigratePlan),
		}),
		protocol.Object(map[string]protocol.Value{
			"backend_closes": migrationTargetPlanInt(shortWrite.linked.BackendCloseCalls), "backend_opens": migrationTargetPlanInt(shortWrite.linked.BackendOpenCalls),
			"case": protocol.String("terminal_short_write"), "category": protocol.String(shortWrite.report.MigrateFailure.Category),
			"code": protocol.String(shortWrite.report.MigrateFailure.Code), "lifecycle_calls": migrationTargetPlanInt(shortWrite.linked.RevisionLifecycleCalls),
			"mode": protocol.String(string(migrateprotocol.ModePlan)), "private_response_write_attempts": migrationTargetPlanInt(shortWrite.linked.RunnerResponseWrites),
			"private_response_writes_completed": migrationTargetPlanInt(shortWrite.report.UserStdoutWrites - shortWrite.report.PartialStdoutWrites), "public_plan_published": protocol.Boolean(shortWrite.report.HasMigratePlan),
		}),
	}

	wireRejections, err := migrationTargetPlanWireRejections()
	if err != nil {
		return protocol.Observation{}, err
	}
	resourceLimits, err := migrationTargetPlanResourceLimits(actual.responseOverflowBytes, actual.responseOverflow.report)
	if err != nil {
		return protocol.Observation{}, err
	}
	planInvariants, err := migrationTargetPlanProtocolInvariants()
	if err != nil {
		return protocol.Observation{}, err
	}
	replacementPreserved, err := migrationTargetPlanReplacementRunePreserved()
	if err != nil {
		return protocol.Observation{}, err
	}
	loadBeforeOpen, err := migrationTargetPlanLoadBeforeOpen(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}

	visible := make([]byte, 0)
	for _, execution := range []migrationTargetPlanExecution{execute, plan, closeFailure, partial, shortWrite} {
		visible = append(visible, execution.stdout.buffer.Bytes()...)
		visible = append(visible, execution.stderr.buffer.Bytes()...)
		visible = append(visible, execution.wire...)
	}
	for _, execution := range []migrationTargetPlanActualExecution{actual.cancellation, actual.forcedCancellation, actual.responseOverflow, actual.stderrRedaction} {
		visible = append(visible, execution.stdout...)
		visible = append(visible, execution.stderr...)
	}
	rawOccurrences := bytes.Count(visible, []byte(migrationTargetPlanSecret)) + actual.artifactSecrets
	if rawOccurrences != 0 || !loadBeforeOpen || !replacementPreserved {
		return protocol.Observation{}, fmt.Errorf("target-plan ownership security = secret:%d load-before-open:%t replacement:%t", rawOccurrences, loadBeforeOpen, replacementPreserved)
	}
	redaction := protocol.Object(map[string]protocol.Value{
		"published_raw_causes": migrationTargetPlanInt(rawOccurrences), "published_secret_values": migrationTargetPlanInt(rawOccurrences),
		"sensitive_classes": migrationTargetPlanStrings("backend_dsn", "raw_error_cause", "runner_stderr"),
	})
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(ownershipCases...), "current_private_protocol_version": migrationTargetPlanInt(int(migrateprotocol.Version)),
		"identity_normalization": protocol.String("none"), "legacy_private_reader": protocol.Boolean(false),
		"load_before_backend_open": protocol.Boolean(loadBeforeOpen), "plan_invariants": planInvariants,
		"private_argument": protocol.String(migrateprotocol.PrivateArgument), "raw_causes_published": protocol.Boolean(rawOccurrences != 0),
		"redaction": redaction, "resource_limits": resourceLimits, "result_union_bound_to_mode": protocol.Boolean(true),
		"valid_replacement_rune_preserved": protocol.Boolean(replacementPreserved), "wire_rejections": wireRejections,
	})
	planSnapshot := planBackend.snapshot()
	dbState := protocol.Object(map[string]protocol.Value{
		"canceled_process_groups_remaining": migrationTargetPlanInt(canceledGroupsRemaining), "failed_plan_published": protocol.Boolean(false),
		"partial_response_republished": protocol.Boolean(false), "plan_mutations": migrationTargetPlanInt(
			planSnapshot.beginCalls + planSnapshot.schemaMutations + planSnapshot.recorderMutations + planSnapshot.revisionMutations + planSnapshot.applicationChanges,
		),
		"secret_values_published": migrationTargetPlanInt(rawOccurrences),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"automatic_retries": migrationTargetPlanInt(0), "cancellation_direct_reaps": migrationTargetPlanInt(actual.cancellation.report.DirectChildReaps - actual.cancellation.report.BuildCalls),
		"cancellation_process_group_terminations": migrationTargetPlanInt(actual.cancellation.report.GroupSIGINTAttempts + actual.cancellation.report.GroupSIGKILLAttempts),
		"legacy_reader_paths":                     migrationTargetPlanInt(0), "ownership_cases": migrationTargetPlanInt(len(ownershipCases)),
		"partial_responses_republished": migrationTargetPlanInt(0), "raw_secret_occurrences": migrationTargetPlanInt(rawOccurrences),
		"resource_limit_cases": migrationTargetPlanInt(5), "strict_wire_rejection_cases": migrationTargetPlanInt(17),
		"successful_mode_calls": migrationTargetPlanInt(2),
	})
	observation = migrationTargetPlanObservation(contract, &result, &dbState, &metrics)
	if err := observation.Validate(); err != nil {
		return protocol.Observation{}, err
	}
	return observation, nil
}

func migrationTargetPlanOwnershipSuccessValue(name string, mode migrateprotocol.Mode, execution migrationTargetPlanExecution, publishedField string) protocol.Value {
	fields := map[string]protocol.Value{
		"backend_closes": migrationTargetPlanInt(execution.linked.BackendCloseCalls), "backend_opens": migrationTargetPlanInt(execution.linked.BackendOpenCalls),
		"case": protocol.String(name), "category": protocol.Null(), "code": protocol.Null(),
		"lifecycle_calls": migrationTargetPlanInt(execution.linked.RevisionLifecycleCalls), "mode": protocol.String(string(mode)),
		"private_response_writes": migrationTargetPlanInt(execution.linked.RunnerResponseWrites),
	}
	fields[publishedField] = protocol.Boolean(true)
	return protocol.Object(fields)
}

func migrationTargetPlanWireRejections() (protocol.Value, error) {
	validRequest := string(migrateprotocol.RequestDocument())
	validResponse, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode:    migrateprotocol.ModeExecute,
		Execute: migrateprotocol.ExecuteResult{DefinitionSetDigest: migrateprotocol.EmptySetDigest},
	}})
	if err != nil {
		return protocol.Value{}, err
	}
	validPlan, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan,
		Plan: []migrateprotocol.PlanRow{{App: "blog", Name: "0001_article", Direction: migrateprotocol.DirectionForward}},
	}})
	if err != nil {
		return protocol.Value{}, err
	}
	values := make([]protocol.Value, 0, 17)
	appendRequest := func(name, document, code string) error {
		request, failure, failed, readErr := migrateprotocol.ReadRequest(strings.NewReader(document))
		if readErr != nil || !failed || request != (migrateprotocol.Request{}) || failure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: code}) {
			return fmt.Errorf("target-plan wire %s = request:%+v failure:%+v failed:%t err:%v", name, request, failure, failed, readErr)
		}
		values = append(values, migrationTargetPlanWireCaseValue(name, "request", failure.Code))
		return nil
	}
	appendResponse := func(name string, document []byte) error {
		response, failure, failed := migrateprotocol.ParseResponse(document, true)
		if !failed || !reflect.DeepEqual(response, migrateprotocol.Response{}) || failure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: migrateprotocol.CodeInvalidResponse}) {
			return fmt.Errorf("target-plan wire %s = response:%+v failure:%+v failed:%t", name, response, failure, failed)
		}
		values = append(values, migrationTargetPlanWireCaseValue(name, "response", failure.Code))
		return nil
	}
	requestCases := []struct{ name, document string }{
		{name: "request_duplicate_key", document: strings.Replace(validRequest, `"protocol_version":2`, `"protocol_version":2,"protocol_version":2`, 1)},
		{name: "request_unknown_key", document: strings.Replace(validRequest, `"mode":`, `"secret":"value","mode":`, 1)},
		{name: "request_trailing_bytes", document: validRequest + `{}`},
		{name: "request_noncanonical_number", document: strings.Replace(validRequest, `"protocol_version":2`, `"protocol_version":2.0`, 1)},
		{name: "request_invalid_utf8", document: validRequest + "\xff"},
		{name: "request_unpaired_utf16_surrogate", document: `{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"named","app":"blog","name":"\ud800"}}`},
	}
	for _, test := range requestCases {
		if err := appendRequest(test.name, test.document, migrateprotocol.CodeInvalidRequest); err != nil {
			return protocol.Value{}, err
		}
	}
	responseCases := []struct {
		name     string
		document []byte
	}{
		{name: "response_duplicate_key", document: bytes.Replace(validResponse, []byte(`"status":"ok"`), []byte(`"status":"ok","status":"ok"`), 1)},
		{name: "response_unknown_key", document: bytes.Replace(validResponse, []byte(`"result":`), []byte(`"secret":"value","result":`), 1)},
		{name: "response_trailing_bytes", document: append(append([]byte(nil), validResponse...), []byte(`{}`)...)},
		{name: "response_noncanonical_number", document: bytes.Replace(validResponse, []byte(`"source_count":0`), []byte(`"source_count":0.0`), 1)},
		{name: "response_invalid_utf8", document: append(append([]byte(nil), validResponse...), 0xff)},
		{name: "response_unpaired_utf16_surrogate", document: []byte(`{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[{"app":"blog","name":"\ud800","direction":"forward"}]}}`)},
	}
	for _, test := range responseCases {
		if err := appendResponse(test.name, test.document); err != nil {
			return protocol.Value{}, err
		}
	}
	if err := appendRequest(
		"request_retired_protocol_version",
		strings.Replace(validRequest, `"protocol_version":2`, `"protocol_version":1`, 1),
		migrateprotocol.CodeProtocolIncompatible,
	); err != nil {
		return protocol.Value{}, err
	}
	if err := appendResponse(
		"response_mode_result_mismatch",
		bytes.Replace(validPlan, []byte(`"mode":"plan","plan"`), []byte(`"mode":"execute","plan"`), 1),
	); err != nil {
		return protocol.Value{}, err
	}
	if err := appendRequest(
		"request_invalid_mode",
		strings.Replace(validRequest, `"mode":"execute"`, `"mode":"preview"`, 1),
		migrateprotocol.CodeInvalidRequest,
	); err != nil {
		return protocol.Value{}, err
	}
	if err := appendRequest(
		"request_invalid_target_kind",
		strings.Replace(validRequest, `"kind":"latest"`, `"kind":"leaf"`, 1),
		migrateprotocol.CodeInvalidRequest,
	); err != nil {
		return protocol.Value{}, err
	}
	if err := appendResponse(
		"response_invalid_direction",
		bytes.Replace(validPlan, []byte(`"direction":"forward"`), []byte(`"direction":"sideways"`), 1),
	); err != nil {
		return protocol.Value{}, err
	}
	return protocol.List(values...), nil
}

func migrationTargetPlanWireCaseValue(name, boundary, code string) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"accepted": protocol.Boolean(false), "boundary": protocol.String(boundary), "case": protocol.String(name),
		"category": protocol.String(migrateprotocol.CategoryProtocol), "code": protocol.String(code),
	})
}

func migrationTargetPlanResourceLimits(
	responseOverflowBytes int,
	responseOverflowReport productcheck.MigrateReport,
) (protocol.Value, error) {
	requestOverflow := bytes.Repeat([]byte{' '}, migrateprotocol.MaxRequestBytes+1)
	request, requestFailure, requestFailed, err := migrateprotocol.ReadRequest(bytes.NewReader(requestOverflow))
	clear(requestOverflow)
	if err != nil || !requestFailed || request != (migrateprotocol.Request{}) ||
		requestFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: migrateprotocol.CodeInvalidRequest}) {
		return protocol.Value{}, fmt.Errorf("target-plan request resource boundary = request:%+v failure:%+v failed:%t err:%v", request, requestFailure, requestFailed, err)
	}

	if responseOverflowBytes != migrateprotocol.MaxResponseBytes+1 || !responseOverflowReport.HasMigrateFailure ||
		responseOverflowReport.MigrateFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: migrateprotocol.CodeInvalidResponse}) ||
		responseOverflowReport.HasMigratePlan || responseOverflowReport.HasMigrateResult ||
		responseOverflowReport.RunnerCalls != 1 || responseOverflowReport.RunnerResponseWrites != 1 ||
		responseOverflowReport.RunnerStdoutRetainedBytes != migrateprotocol.MaxResponseBytes || !responseOverflowReport.RunnerStdoutTruncated ||
		responseOverflowReport.UserStdoutWrites != 0 {
		return protocol.Value{}, fmt.Errorf("target-plan response resource boundary = bytes:%d report:%+v", responseOverflowBytes, responseOverflowReport)
	}

	maximumIdentity := strings.Repeat("i", migrateprotocol.MaxIdentityBytes)
	maximumIdentityRequest := migrateprotocol.Request{
		Mode:   migrateprotocol.ModePlan,
		Target: migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: "a", Name: maximumIdentity},
	}
	if _, err := migrateprotocol.EncodeRequest(maximumIdentityRequest); err != nil {
		return protocol.Value{}, fmt.Errorf("target-plan maximum request identity: %w", err)
	}
	maximumIdentityResponse := migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan,
		Plan: []migrateprotocol.PlanRow{{App: "a", Name: maximumIdentity, Direction: migrateprotocol.DirectionForward}},
	}}
	if _, err := migrateprotocol.EncodeResponse(maximumIdentityResponse); err != nil {
		return protocol.Value{}, fmt.Errorf("target-plan maximum response identity: %w", err)
	}
	overflowIdentity := maximumIdentity + "i"
	maximumIdentityRequest.Target.Name = overflowIdentity
	_, requestIdentityErr := migrateprotocol.EncodeRequest(maximumIdentityRequest)
	maximumIdentityResponse.Result.Plan[0].Name = overflowIdentity
	_, responseIdentityErr := migrateprotocol.EncodeResponse(maximumIdentityResponse)
	if requestIdentityErr == nil || responseIdentityErr == nil {
		return protocol.Value{}, fmt.Errorf("target-plan identity overflow = request:%v response:%v", requestIdentityErr, responseIdentityErr)
	}

	aggregateRows := make([]migrateprotocol.PlanRow, 16)
	identityPrefix := strings.Repeat("n", migrateprotocol.MaxIdentityBytes-3)
	aggregateBytes := 0
	for index := range aggregateRows {
		aggregateRows[index] = migrateprotocol.PlanRow{
			App: "a", Name: identityPrefix + fmt.Sprintf("%02x", index), Direction: migrateprotocol.DirectionForward,
		}
		aggregateBytes += len(aggregateRows[index].App) + len(aggregateRows[index].Name)
	}
	if aggregateBytes != migrateprotocol.MaxIdentityAggregateBytes {
		return protocol.Value{}, fmt.Errorf("target-plan aggregate identity bytes = %d, want %d", aggregateBytes, migrateprotocol.MaxIdentityAggregateBytes)
	}
	aggregateResponse := migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{Mode: migrateprotocol.ModePlan, Plan: aggregateRows}}
	if _, err := migrateprotocol.EncodeResponse(aggregateResponse); err != nil {
		return protocol.Value{}, fmt.Errorf("target-plan maximum aggregate identity: %w", err)
	}
	aggregateResponse.Result.Plan = append(aggregateResponse.Result.Plan, migrateprotocol.PlanRow{
		App: "a", Name: "overflow", Direction: migrateprotocol.DirectionForward,
	})
	if _, err := migrateprotocol.EncodeResponse(aggregateResponse); err == nil {
		return protocol.Value{}, errors.New("target-plan aggregate identity overflow was accepted")
	}

	maximumRows := migrationTargetPlanWireRows(migrateprotocol.MaxPlanRows, migrateprotocol.DirectionForward)
	maximumRowsResponse := migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{Mode: migrateprotocol.ModePlan, Plan: maximumRows}}
	if _, err := migrateprotocol.EncodeResponse(maximumRowsResponse); err != nil {
		return protocol.Value{}, fmt.Errorf("target-plan maximum rows: %w", err)
	}
	maximumRowsResponse.Result.Plan = append(maximumRowsResponse.Result.Plan, migrateprotocol.PlanRow{
		App: "overflow", Name: "overflow", Direction: migrateprotocol.DirectionForward,
	})
	if _, err := migrateprotocol.EncodeResponse(maximumRowsResponse); err == nil {
		return protocol.Value{}, errors.New("target-plan plan-row overflow was accepted")
	}

	return protocol.List(
		migrationTargetPlanResourceLimitValue("request_bytes", "request", migrateprotocol.MaxRequestBytes, "bytes"),
		migrationTargetPlanResourceLimitValue("response_bytes", "response", migrateprotocol.MaxResponseBytes, "bytes"),
		migrationTargetPlanResourceLimitValue("identity_bytes", "request_and_response", migrateprotocol.MaxIdentityBytes, "bytes"),
		migrationTargetPlanResourceLimitValue("identity_aggregate_bytes", "request_and_response", migrateprotocol.MaxIdentityAggregateBytes, "bytes"),
		migrationTargetPlanResourceLimitValue("plan_rows", "response", migrateprotocol.MaxPlanRows, "rows"),
	), nil
}

func migrationTargetPlanResourceLimitValue(name, boundary string, maximum int, unit string) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"boundary": protocol.String(boundary), "case": protocol.String(name), "maximum": migrationTargetPlanInt(maximum),
		"overflow": protocol.String("rejected"), "unit": protocol.String(unit),
	})
}

func migrationTargetPlanWireRows(count int, direction migrateprotocol.Direction) []migrateprotocol.PlanRow {
	rows := make([]migrateprotocol.PlanRow, count)
	for index := range rows {
		rows[index] = migrateprotocol.PlanRow{
			App: "app", Name: fmt.Sprintf("%04d_migration", index), Direction: direction,
		}
	}
	return rows
}

func migrationTargetPlanProtocolInvariants() (protocol.Value, error) {
	maximumRows := migrationTargetPlanWireRows(migrateprotocol.MaxPlanRows, migrateprotocol.DirectionForward)
	if _, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan, Plan: maximumRows,
	}}); err != nil {
		return protocol.Value{}, fmt.Errorf("target-plan maximum unique rows: %w", err)
	}
	duplicate := []migrateprotocol.PlanRow{
		{App: "blog", Name: "0001_article", Direction: migrateprotocol.DirectionForward},
		{App: "blog", Name: "0001_article", Direction: migrateprotocol.DirectionForward},
	}
	if _, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan, Plan: duplicate,
	}}); err == nil {
		return protocol.Value{}, errors.New("target-plan duplicate plan identity was accepted")
	}
	mixed := []migrateprotocol.PlanRow{
		{App: "blog", Name: "0002_editor", Direction: migrateprotocol.DirectionBackward},
		{App: "blog", Name: "0001_article", Direction: migrateprotocol.DirectionForward},
	}
	if _, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan, Plan: mixed,
	}}); err == nil {
		return protocol.Value{}, errors.New("target-plan mixed plan directions were accepted")
	}
	ordered := []migrateprotocol.PlanRow{
		{App: "charlie", Name: "0001_descendant", Direction: migrateprotocol.DirectionBackward},
		{App: "alpha", Name: "0003_third", Direction: migrateprotocol.DirectionBackward},
		{App: "alpha", Name: "0002_second", Direction: migrateprotocol.DirectionBackward},
	}
	document, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan, Plan: ordered,
	}})
	if err != nil {
		return protocol.Value{}, err
	}
	parsed, failure, failed := migrateprotocol.ParseResponse(document, true)
	if failed || failure != (migrateprotocol.Failure{}) || !parsed.OK || !reflect.DeepEqual(parsed.Result.Plan, ordered) {
		return protocol.Value{}, fmt.Errorf("target-plan row-order round trip = parsed:%+v failure:%+v failed:%t", parsed, failure, failed)
	}
	return protocol.Object(map[string]protocol.Value{
		"closed_directions":  migrationTargetPlanStrings(string(migrateprotocol.DirectionForward), string(migrateprotocol.DirectionBackward)),
		"duplicate_identity": protocol.String("rejected"), "maximum_unique_rows": migrationTargetPlanInt(migrateprotocol.MaxPlanRows),
		"mixed_direction": protocol.String("rejected"), "row_order": protocol.String("preserved"),
	}), nil
}

func migrationTargetPlanReplacementRunePreserved() (bool, error) {
	app, name := "replacement_�", "0001_�"
	request := migrateprotocol.Request{
		Mode:   migrateprotocol.ModePlan,
		Target: migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: app, Name: name},
	}
	document, err := migrateprotocol.EncodeRequest(request)
	if err != nil {
		return false, err
	}
	parsedRequest, failure, failed, err := migrateprotocol.ReadRequest(bytes.NewReader(document))
	if err != nil || failed || failure != (migrateprotocol.Failure{}) || parsedRequest != request {
		return false, fmt.Errorf("target-plan replacement request = request:%+v failure:%+v failed:%t err:%v", parsedRequest, failure, failed, err)
	}
	rows := []migrateprotocol.PlanRow{{App: app, Name: name, Direction: migrateprotocol.DirectionForward}}
	responseDocument, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan, Plan: rows,
	}})
	if err != nil {
		return false, err
	}
	response, responseFailure, responseFailed := migrateprotocol.ParseResponse(responseDocument, true)
	if responseFailed || responseFailure != (migrateprotocol.Failure{}) || !response.OK || !reflect.DeepEqual(response.Result.Plan, rows) {
		return false, fmt.Errorf("target-plan replacement response = response:%+v failure:%+v failed:%t", response, responseFailure, responseFailed)
	}
	return true, nil
}

func migrationTargetPlanLoadBeforeOpen(ctx context.Context) (bool, error) {
	requestDocument, err := migrateprotocol.EncodeRequest(migrateprotocol.Request{
		Mode: migrateprotocol.ModePlan, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest},
	})
	if err != nil {
		return false, err
	}
	openCalls := 0
	var output bytes.Buffer
	report, err := linked.RunMigrate(ctx, linked.MigrateConfig{
		MigrationDefinitionSources: []definition.Source{{
			SourceID: "actual/invalid.godj.json", Document: []byte(`{"raw_cause":"` + migrationTargetPlanSecret + `"}`),
		}},
		OpenMigrationBackend: func(context.Context) (linked.MigrationBackend, error) {
			openCalls++
			return newMigrationTargetPlanBackend(), nil
		},
	}, []string{migrateprotocol.PrivateArgument}, bytes.NewReader(requestDocument), &output)
	if err != nil {
		return false, err
	}
	response, failure, failed := migrateprotocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (migrateprotocol.Failure{}) || response.OK || response.Failure.Category != migrateprotocol.CategorySource ||
		report.LoadCalls != 1 || report.BackendOpenCalls != 0 || openCalls != 0 || bytes.Contains(output.Bytes(), []byte(migrationTargetPlanSecret)) {
		return false, fmt.Errorf("target-plan load-before-open = report:%+v response:%+v failure:%+v failed:%t opens:%d output:%q", report, response, failure, failed, openCalls, output.Bytes())
	}
	return true, nil
}
