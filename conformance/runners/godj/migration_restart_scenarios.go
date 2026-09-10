package godj

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

const migrationRestartAlphaTable = "godj_restart_alpha"

var (
	migrationRestartA1     = migrations.MigrationKey{App: "alpha", Name: "0001_initial"}
	migrationRestartA2     = migrations.MigrationKey{App: "alpha", Name: "0002_second"}
	migrationRestartA3     = migrations.MigrationKey{App: "alpha", Name: "0003_third"}
	migrationRestartB1     = migrations.MigrationKey{App: "beta", Name: "0001_initial"}
	migrationRestartLegacy = migrations.MigrationKey{App: "legacy", Name: "0099_retired"}
)

type migrationRestartMode uint8

const (
	migrationRestartAbsentRecorder migrationRestartMode = iota + 1
	migrationRestartEmptyRecorder
	migrationRestartRecorded
	migrationRestartUnrecorded
	migrationRestartAliases
	migrationRestartAppliedPrefix
	migrationRestartFullyApplied
	migrationRestartUnknownLegacy
	migrationRestartInconsistentHistory
	migrationRestartFailureTail
)

type migrationRestartFixture struct {
	phase            protocol.Phase
	mode             migrationRestartMode
	restartBoundary  string
	definitions      []migrations.Migration
	recorderSetup    []migrationRestartRecorderTransition
	executionSetup   []migrations.MigrationKey
	target           migrations.MigrationKey
	failedMigration  migrations.MigrationKey
	includeSetupFact bool
}

type migrationRestartRecorderTransition struct {
	key     migrations.MigrationKey
	applied bool
}

var migrationRestartFixtures = map[string]func() migrationRestartFixture{
	"django.migration.restart.absent_recorder_read": func() migrationRestartFixture {
		return migrationRestartFixture{phase: protocol.PhaseEvaluation, mode: migrationRestartAbsentRecorder, restartBoundary: "fresh_recorder"}
	},
	"django.migration.restart.empty_recorder_read": func() migrationRestartFixture {
		return migrationRestartFixture{
			phase: protocol.PhaseEvaluation, mode: migrationRestartEmptyRecorder, restartBoundary: "fresh_recorder",
			recorderSetup: migrationRestartRecordThenUnrecord(migrationRestartA1),
		}
	},
	"django.migration.restart.record_visible_to_fresh_reader": func() migrationRestartFixture {
		return migrationRestartFixture{
			phase: protocol.PhaseEvaluation, mode: migrationRestartRecorded, restartBoundary: "fresh_recorder",
			recorderSetup: []migrationRestartRecorderTransition{{key: migrationRestartA1, applied: true}}, includeSetupFact: true,
		}
	},
	"django.migration.restart.unrecord_hidden_from_fresh_reader": func() migrationRestartFixture {
		return migrationRestartFixture{
			phase: protocol.PhaseEvaluation, mode: migrationRestartUnrecorded, restartBoundary: "fresh_recorder",
			recorderSetup: migrationRestartRecordThenUnrecord(migrationRestartA1), includeSetupFact: true,
		}
	},
	"django.migration.restart.database_alias_isolation": func() migrationRestartFixture {
		return migrationRestartFixture{phase: protocol.PhaseEvaluation, mode: migrationRestartAliases, restartBoundary: "fresh_recorder"}
	},
	"django.migration.restart.applied_prefix_tail": func() migrationRestartFixture {
		return migrationRestartPlanningFixture(migrationRestartAppliedPrefix, []migrations.MigrationKey{migrationRestartA1})
	},
	"django.migration.restart.fully_applied_empty_plan": func() migrationRestartFixture {
		return migrationRestartPlanningFixture(migrationRestartFullyApplied, []migrations.MigrationKey{migrationRestartA1, migrationRestartA2, migrationRestartA3})
	},
	"django.migration.restart.unknown_legacy_record": func() migrationRestartFixture {
		fixture := migrationRestartPlanningFixture(migrationRestartUnknownLegacy, nil)
		fixture.recorderSetup = []migrationRestartRecorderTransition{{key: migrationRestartLegacy, applied: true}}
		return fixture
	},
	"django.migration.restart.inconsistent_known_history": func() migrationRestartFixture {
		fixture := migrationRestartPlanningFixture(migrationRestartInconsistentHistory, nil)
		fixture.recorderSetup = []migrationRestartRecorderTransition{{key: migrationRestartA2, applied: true}}
		return fixture
	},
	"django.migration.restart.failure_tail": func() migrationRestartFixture {
		fixture := migrationRestartPlanningFixture(
			migrationRestartFailureTail,
			[]migrations.MigrationKey{migrationRestartA1, migrationRestartA2, migrationRestartA3},
		)
		fixture.failedMigration = migrationRestartA2
		return fixture
	},
}

func migrationRestartPlanningFixture(mode migrationRestartMode, setup []migrations.MigrationKey) migrationRestartFixture {
	return migrationRestartFixture{
		phase: protocol.PhaseEvaluation, mode: mode, restartBoundary: "fresh_executor",
		definitions: migrationRestartDefinitions(), executionSetup: append([]migrations.MigrationKey(nil), setup...),
		target: migrationRestartA3,
	}
}

func migrationRestartRecordThenUnrecord(key migrations.MigrationKey) []migrationRestartRecorderTransition {
	return []migrationRestartRecorderTransition{{key: key, applied: true}, {key: key, applied: false}}
}

func migrationRestartScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	factory, ok := migrationRestartFixtures[contract.Scenario]
	if !ok {
		return protocol.Observation{}, fmt.Errorf("unsupported scenario %q", contract.Scenario)
	}
	fixture := factory()
	if fixture.phase != contract.Phase {
		return protocol.Observation{}, fmt.Errorf("scenario %q phase = %q, manifest requires %q", contract.Scenario, fixture.phase, contract.Phase)
	}
	return runMigrationRestartFixture(ctx, contract.ID, fixture)
}

func runMigrationRestartFixture(ctx context.Context, contractID string, fixture migrationRestartFixture) (protocol.Observation, error) {
	if ctx == nil {
		return protocol.Observation{}, errors.New("migration restart scenario: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return protocol.Observation{}, err
	}
	directory, err := os.MkdirTemp("", "godj-migration-restart-")
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("create migration restart directory: %w", err)
	}

	var observation protocol.Observation
	if fixture.mode == migrationRestartAliases {
		observation, err = runMigrationRestartAliasFixture(ctx, contractID, directory, fixture)
	} else {
		path := filepath.Join(directory, "default.sqlite3")
		if setupErr := setupMigrationRestartDatabase(ctx, path, fixture); setupErr != nil {
			err = setupErr
		} else {
			observation, err = captureMigrationRestartDatabase(ctx, contractID, path, fixture)
		}
	}
	cleanupErr := os.RemoveAll(directory)
	if err != nil || cleanupErr != nil {
		return protocol.Observation{}, errors.Join(err, cleanupErr)
	}
	return observation, nil
}

func setupMigrationRestartDatabase(ctx context.Context, path string, fixture migrationRestartFixture) (resultErr error) {
	writer, err := sqlite.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("open migration restart writer: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, writer.Close()) }()

	switch fixture.mode {
	case migrationRestartAbsentRecorder:
		return nil
	case migrationRestartEmptyRecorder, migrationRestartRecorded, migrationRestartUnrecorded,
		migrationRestartUnknownLegacy, migrationRestartInconsistentHistory:
		for _, transition := range fixture.recorderSetup {
			if err := writeMigrationRestartRecord(ctx, writer, transition.key, transition.applied); err != nil {
				return err
			}
		}
		return nil
	case migrationRestartAppliedPrefix, migrationRestartFullyApplied:
		return executeMigrationRestartSeed(ctx, writer, fixture.definitions, fixture.executionSetup...)
	case migrationRestartFailureTail:
		plan := migrationExecutionForwardPlan(fixture.executionSetup...)
		trace := newMigrationExecutionTrace(writer, fixture.definitions, &migrationExecutionFault{
			key: fixture.failedMigration, direction: migrations.DirectionForward,
		}, nil)
		_, executionErr := (migrations.DirectExecutor{Backend: trace}).ExecutePlan(
			ctx, migrations.EmptyProjectState(), fixture.definitions, plan,
		)
		if executionErr == nil {
			return errors.New("seed migration restart failure tail unexpectedly succeeded")
		}
		if err := trace.validate(plan, executionErr); err != nil {
			return fmt.Errorf("validate migration restart failure seed: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported migration restart setup mode %d", fixture.mode)
	}
}

func writeMigrationRestartRecord(ctx context.Context, writer *sqlite.Backend, key migrations.MigrationKey, applied bool) (resultErr error) {
	transaction, err := writer.BeginMigration(ctx)
	if err != nil {
		return fmt.Errorf("begin migration restart recorder transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, transaction.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	if applied {
		err = transaction.RecordApplied(ctx, key.App, key.Name)
	} else {
		err = transaction.RecordUnapplied(ctx, key.App, key.Name)
	}
	if err != nil {
		return fmt.Errorf("write migration restart recorder transition for %s.%s: %w", key.App, key.Name, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration restart recorder transition for %s.%s: %w", key.App, key.Name, err)
	}
	return nil
}

func executeMigrationRestartSeed(ctx context.Context, writer *sqlite.Backend, definitions []migrations.Migration, keys ...migrations.MigrationKey) error {
	_, err := (migrations.DirectExecutor{Backend: writer}).ExecutePlan(
		ctx,
		migrations.EmptyProjectState(),
		definitions,
		migrationExecutionForwardPlan(keys...),
	)
	if err != nil {
		return fmt.Errorf("execute migration restart seed: %w", err)
	}
	return nil
}

func migrationRestartDefinitions() []migrations.Migration {
	return []migrations.Migration{
		{
			App: migrationRestartA1.App, Name: migrationRestartA1.Name,
			Operations: []migrations.Operation{migrations.CreateModel{
				AppLabel: migrationRestartA1.App,
				Model: ir.Model{
					Name: "entry", GoName: "Entry", DBTable: migrationRestartAlphaTable,
					Fields: []ir.Field{
						migrationExecutionAutoField(),
						{Name: "a1_marker", GoName: "A1Marker", Column: "a1_marker", Kind: ir.FieldChar, MaxLength: 16},
					},
				},
			}},
		},
		{
			App: migrationRestartA2.App, Name: migrationRestartA2.Name,
			Dependencies: []migrations.MigrationKey{migrationRestartA1},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: migrationRestartA2.App, ModelName: "entry",
				Field: ir.Field{Name: "a2_marker", GoName: "A2Marker", Column: "a2_marker", Kind: ir.FieldBoolean},
			}},
		},
		{
			App: migrationRestartA3.App, Name: migrationRestartA3.Name,
			Dependencies: []migrations.MigrationKey{migrationRestartA2},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: migrationRestartA3.App, ModelName: "entry",
				Field: ir.Field{Name: "a3_marker", GoName: "A3Marker", Column: "a3_marker", Kind: ir.FieldChar, Nullable: true, MaxLength: 16},
			}},
		},
	}
}

type migrationRestartObservedReader struct {
	delegate migrationbackend.AppliedMigrationReader
	calls    int
	records  []migrationbackend.AppliedMigration
}

func (reader *migrationRestartObservedReader) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	reader.calls++
	records, err := reader.delegate.ReadAppliedMigrations(ctx)
	reader.records = append([]migrationbackend.AppliedMigration(nil), records...)
	return append([]migrationbackend.AppliedMigration(nil), records...), err
}

func captureMigrationRestartDatabase(ctx context.Context, contractID, path string, fixture migrationRestartFixture) (result protocol.Observation, resultErr error) {
	dataSourceName := migrationRestartReadOnlyDataSource(path)
	observer, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("open migration restart observer: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, observer.Close()) }()
	if err := observer.PingContext(ctx); err != nil {
		return protocol.Observation{}, fmt.Errorf("ping migration restart observer: %w", err)
	}
	readerBackend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("open fresh migration restart reader: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, readerBackend.Close()) }()

	before, err := migrationRestartDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("snapshot migration restart database before capture: %w", err)
	}
	reader := &migrationRestartObservedReader{delegate: readerBackend}
	// The durable setup and both database snapshots sit outside this capture.
	// Its only database operation is LoadAppliedState through a mode=ro fresh
	// backend; CheckHistory and Plan are pure. That makes all non-SELECT, DDL,
	// and write counts structurally zero instead of inferred from unchanged data.
	appliedState, err := migrations.LoadAppliedState(ctx, reader)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("load migration restart applied state: %w", err)
	}
	if reader.calls != 1 {
		return protocol.Observation{}, fmt.Errorf("load migration restart applied state called reader %d times, want 1", reader.calls)
	}
	appliedKeys := migrationRestartRecordKeys(reader.records)

	var plan []migrations.PlanStep
	var historyErr error
	planCalls := 0
	if migrationRestartUsesPlanner(fixture.mode) {
		planner, err := migrations.NewPlanner(fixture.definitions...)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("build migration restart planner: %w", err)
		}
		historyErr = planner.CheckHistory(appliedState)
		if historyErr == nil {
			planCalls++
			plan, err = planner.Plan(appliedState, migrations.NamedTarget(fixture.target))
			if err != nil {
				return protocol.Observation{}, fmt.Errorf("plan migration restart tail: %w", err)
			}
		}
	}
	after, err := migrationRestartDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("snapshot migration restart database after capture: %w", err)
	}
	if !reflect.DeepEqual(before, after) {
		return protocol.Observation{}, errors.New("migration restart read/check/plan capture mutated database state")
	}

	databaseState := protocol.Object(map[string]protocol.Value{"after": after, "before": before})
	metrics := migrationRestartMetrics(fixture, before, after)
	if migrationRestartUsesPlanner(fixture.mode) {
		metrics = migrationRestartMetricsWithGraph(fixture, before, after)
	}

	if fixture.mode == migrationRestartInconsistentHistory {
		if historyErr == nil {
			return protocol.Observation{}, errors.New("inconsistent migration restart history unexpectedly passed validation")
		}
		if planCalls != 0 {
			return protocol.Observation{}, fmt.Errorf("inconsistent migration restart history invoked Plan %d times, want 0", planCalls)
		}
		var planningError *migrations.PlanningError
		if !errors.As(historyErr, &planningError) {
			return protocol.Observation{}, fmt.Errorf("migration restart history error = %T, want *migrations.PlanningError: %w", historyErr, historyErr)
		}
		metrics = migrationRestartMetricsWithGraph(fixture, before, after)
		metrics = migrationRestartAddObjectFields(metrics, map[string]protocol.Value{
			"request": protocol.Object(map[string]protocol.Value{
				"applied_migrations": protocol.List(planningKeyValuesSorted(appliedKeys)...),
				"operation":          protocol.String("validate_history_before_planning"),
				"plan_invoked":       protocol.Boolean(planCalls != 0),
				"target":             planningKeyValue(fixture.target),
			}),
		})
		return protocol.Observation{
			ID: contractID, Status: protocol.StatusObserved, Phase: fixture.phase,
			Error: &protocol.ObservedError{
				Category: string(planningError.Category), Code: string(planningError.Code),
				Message: historyErr.Error(), MessageIsContract: boolPointer(false),
			},
			DBState: &databaseState, Metrics: &metrics,
		}, nil
	}
	if historyErr != nil {
		return protocol.Observation{}, fmt.Errorf("migration restart history unexpectedly failed: %w", historyErr)
	}

	resultValue := migrationRestartResult(fixture, appliedKeys, plan)
	return protocol.Observation{
		ID: contractID, Status: protocol.StatusObserved, Phase: fixture.phase,
		Result: &resultValue, DBState: &databaseState, Metrics: &metrics,
	}, nil
}

func runMigrationRestartAliasFixture(ctx context.Context, contractID, directory string, fixture migrationRestartFixture) (protocol.Observation, error) {
	type aliasCase struct {
		alias string
		path  string
		key   migrations.MigrationKey
	}
	aliases := []aliasCase{
		{alias: "default", path: filepath.Join(directory, "default.sqlite3"), key: migrationRestartA1},
		{alias: "other", path: filepath.Join(directory, "other.sqlite3"), key: migrationRestartB1},
	}
	for _, alias := range aliases {
		writer, err := sqlite.Open(ctx, alias.path)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("open migration restart alias %s writer: %w", alias.alias, err)
		}
		writeErr := writeMigrationRestartRecord(ctx, writer, alias.key, true)
		closeErr := writer.Close()
		if writeErr != nil || closeErr != nil {
			return protocol.Observation{}, errors.Join(writeErr, closeErr)
		}
	}

	beforeDatabases := make([]protocol.Value, 0, len(aliases))
	afterDatabases := make([]protocol.Value, 0, len(aliases))
	resultDatabases := make([]protocol.Value, 0, len(aliases))
	for _, alias := range aliases {
		dataSourceName := migrationRestartReadOnlyDataSource(alias.path)
		observer, err := sql.Open("sqlite", dataSourceName)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := observer.PingContext(ctx); err != nil {
			return protocol.Observation{}, errors.Join(err, observer.Close())
		}
		backend, err := sqlite.Open(ctx, dataSourceName)
		if err != nil {
			return protocol.Observation{}, errors.Join(err, observer.Close())
		}
		before, err := migrationRestartAliasSnapshot(ctx, observer, alias.alias)
		if err != nil {
			return protocol.Observation{}, errors.Join(err, backend.Close(), observer.Close())
		}
		reader := &migrationRestartObservedReader{delegate: backend}
		if _, err := migrations.LoadAppliedState(ctx, reader); err != nil {
			return protocol.Observation{}, errors.Join(err, backend.Close(), observer.Close())
		}
		if reader.calls != 1 {
			return protocol.Observation{}, errors.Join(fmt.Errorf("alias %s reader calls = %d, want 1", alias.alias, reader.calls), backend.Close(), observer.Close())
		}
		after, err := migrationRestartAliasSnapshot(ctx, observer, alias.alias)
		cleanupErr := errors.Join(backend.Close(), observer.Close())
		if err != nil || cleanupErr != nil {
			return protocol.Observation{}, errors.Join(err, cleanupErr)
		}
		if !reflect.DeepEqual(before, after) {
			return protocol.Observation{}, fmt.Errorf("migration restart alias %s capture mutated database state", alias.alias)
		}
		keys := migrationRestartRecordKeys(reader.records)
		beforeDatabases = append(beforeDatabases, before)
		afterDatabases = append(afterDatabases, after)
		resultDatabases = append(resultDatabases, protocol.Object(map[string]protocol.Value{
			"alias": protocol.String(alias.alias), "applied_migrations": protocol.List(planningKeyValuesSorted(keys)...),
		}))
	}

	before := protocol.Object(map[string]protocol.Value{"databases": protocol.List(beforeDatabases...)})
	after := protocol.Object(map[string]protocol.Value{"databases": protocol.List(afterDatabases...)})
	databaseState := protocol.Object(map[string]protocol.Value{"after": after, "before": before})
	metrics := migrationRestartMetrics(fixture, before, after)
	result := protocol.Object(map[string]protocol.Value{"databases": protocol.List(resultDatabases...)})
	return protocol.Observation{
		ID: contractID, Status: protocol.StatusObserved, Phase: fixture.phase,
		Result: &result, DBState: &databaseState, Metrics: &metrics,
	}, nil
}

func migrationRestartReadOnlyDataSource(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro"
}

func migrationRestartDatabaseSnapshot(ctx context.Context, observer *sql.DB) (protocol.Value, error) {
	fields, err := migrationRestartDatabaseFields(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	return protocol.Object(fields), nil
}

func migrationRestartAliasSnapshot(ctx context.Context, observer *sql.DB, alias string) (protocol.Value, error) {
	fields, err := migrationRestartDatabaseFields(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	fields["alias"] = protocol.String(alias)
	return protocol.Object(fields), nil
}

func migrationRestartDatabaseFields(ctx context.Context, observer *sql.DB) (map[string]protocol.Value, error) {
	recorderPresent, err := sqliteTableExistsContext(ctx, observer, goDjMigrationRecordTable)
	if err != nil {
		return nil, err
	}
	applied, err := readMigrationRecords(ctx, observer)
	if err != nil {
		return nil, err
	}
	managedSchema := make([]protocol.Value, 0, 1)
	tablePresent, err := sqliteTableExistsContext(ctx, observer, migrationRestartAlphaTable)
	if err != nil {
		return nil, err
	}
	if tablePresent {
		columns, err := migrationExecutionColumns(ctx, observer, migrationRestartAlphaTable)
		if err != nil {
			return nil, err
		}
		managedSchema = append(managedSchema, protocol.Object(map[string]protocol.Value{
			"columns": protocol.List(columns...), "name": protocol.String(migrationRestartAlphaTable),
		}))
	}
	return map[string]protocol.Value{
		"applied_migrations": applied,
		"managed_schema":     protocol.List(managedSchema...),
		"recorder_present":   protocol.Boolean(recorderPresent),
	}, nil
}

func migrationRestartRecordKeys(records []migrationbackend.AppliedMigration) []migrations.MigrationKey {
	keys := make([]migrations.MigrationKey, 0, len(records))
	for _, record := range records {
		keys = append(keys, migrations.MigrationKey{App: record.App, Name: record.Name})
	}
	sort.Slice(keys, func(left, right int) bool { return planningKeyLess(keys[left], keys[right]) })
	return keys
}

func migrationRestartUsesPlanner(mode migrationRestartMode) bool {
	switch mode {
	case migrationRestartAppliedPrefix, migrationRestartFullyApplied, migrationRestartUnknownLegacy, migrationRestartInconsistentHistory, migrationRestartFailureTail:
		return true
	default:
		return false
	}
}

func migrationRestartMetrics(fixture migrationRestartFixture, before, after protocol.Value) protocol.Value {
	fields := map[string]protocol.Value{
		"ddl_statement_count":        protocol.Integer("0"),
		"non_select_statement_count": protocol.Integer("0"),
		"restart_boundary":           protocol.String(fixture.restartBoundary),
		"state_unchanged":            protocol.Boolean(reflect.DeepEqual(before, after)),
		"write_statement_count":      protocol.Integer("0"),
	}
	if fixture.includeSetupFact {
		key, transition, ok := migrationRestartSetupFact(fixture.recorderSetup)
		if !ok {
			return protocol.Object(fields)
		}
		fields["setup"] = protocol.Object(map[string]protocol.Value{
			"migration": planningKeyValue(key), "transition": protocol.String(transition),
		})
	}
	return protocol.Object(fields)
}

func migrationRestartSetupFact(transitions []migrationRestartRecorderTransition) (migrations.MigrationKey, string, bool) {
	if len(transitions) == 1 && transitions[0].applied {
		return transitions[0].key, "recorded", true
	}
	if len(transitions) == 2 && transitions[0].applied && !transitions[1].applied && transitions[0].key == transitions[1].key {
		return transitions[0].key, "recorded_then_unrecorded", true
	}
	return migrations.MigrationKey{}, "", false
}

func migrationRestartMetricsWithGraph(fixture migrationRestartFixture, before, after protocol.Value) protocol.Value {
	nodes, dependencies := migrationRestartGraphFacts(fixture.definitions)
	return migrationRestartAddObjectFields(migrationRestartMetrics(fixture, before, after), map[string]protocol.Value{
		"graph": planningGraphValue(nodes, dependencies),
	})
}

func migrationRestartGraphFacts(definitions []migrations.Migration) ([]migrations.MigrationKey, []migrationPlanningDependency) {
	nodes := make([]migrations.MigrationKey, 0, len(definitions))
	dependencies := make([]migrationPlanningDependency, 0)
	for _, definition := range definitions {
		nodes = append(nodes, definition.Key())
		for _, parent := range definition.Dependencies {
			dependencies = append(dependencies, migrationPlanningDependency{child: definition.Key(), parent: parent})
		}
	}
	return nodes, dependencies
}

func migrationRestartAddObjectFields(value protocol.Value, added map[string]protocol.Value) protocol.Value {
	fields := make(map[string]protocol.Value, len(value.Fields)+len(added))
	for _, field := range value.Fields {
		fields[field.Name] = field.Value
	}
	for name, field := range added {
		fields[name] = field
	}
	return protocol.Object(fields)
}

func migrationRestartResult(fixture migrationRestartFixture, applied []migrations.MigrationKey, plan []migrations.PlanStep) protocol.Value {
	fields := map[string]protocol.Value{
		"applied_migrations": protocol.List(planningKeyValuesSorted(applied)...),
	}
	switch fixture.mode {
	case migrationRestartRecorded:
		if key, _, ok := migrationRestartSetupFact(fixture.recorderSetup); ok {
			fields["recorded_migration"] = planningKeyValue(key)
		}
	case migrationRestartUnrecorded:
		if key, _, ok := migrationRestartSetupFact(fixture.recorderSetup); ok {
			fields["unrecorded_migration"] = planningKeyValue(key)
		}
	case migrationRestartAppliedPrefix, migrationRestartFullyApplied, migrationRestartFailureTail:
		fields["plan"] = protocol.List(planningStepValues(plan)...)
		fields["target"] = planningKeyValue(fixture.target)
		if fixture.mode == migrationRestartFailureTail {
			fields["failed_migration"] = planningKeyValue(fixture.failedMigration)
		}
	case migrationRestartUnknownLegacy:
		known, unknown := migrationRestartPartitionApplied(fixture.definitions, applied)
		fields["known_applied"] = protocol.List(planningKeyValuesSorted(known)...)
		fields["unknown_applied"] = protocol.List(planningKeyValuesSorted(unknown)...)
		fields["plan"] = protocol.List(planningStepValues(plan)...)
		fields["target"] = planningKeyValue(fixture.target)
	}
	return protocol.Object(fields)
}

func migrationRestartPartitionApplied(definitions []migrations.Migration, applied []migrations.MigrationKey) (known, unknown []migrations.MigrationKey) {
	knownSet := make(map[migrations.MigrationKey]struct{}, len(definitions))
	for _, definition := range definitions {
		knownSet[definition.Key()] = struct{}{}
	}
	for _, key := range applied {
		if _, ok := knownSet[key]; ok {
			known = append(known, key)
		} else {
			unknown = append(unknown, key)
		}
	}
	return known, unknown
}
