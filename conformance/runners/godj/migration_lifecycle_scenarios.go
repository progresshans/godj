package godj

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const (
	migrationLifecycleAlphaTable = "godj_lifecycle_alpha"
	migrationLifecycleBetaTable  = "godj_lifecycle_beta"
)

var (
	migrationLifecycleA1     = migrations.MigrationKey{App: "alpha", Name: "0001_initial"}
	migrationLifecycleA2     = migrations.MigrationKey{App: "alpha", Name: "0002_second"}
	migrationLifecycleA3     = migrations.MigrationKey{App: "alpha", Name: "0003_third"}
	migrationLifecycleB1     = migrations.MigrationKey{App: "beta", Name: "0001_initial"}
	migrationLifecycleLegacy = migrations.MigrationKey{App: "legacy", Name: "0099_retired"}
)

var errForcedMigrationLifecycleMiddleFailure = errors.New("forced middle lifecycle failure")

type migrationLifecycleSetup uint8

const (
	migrationLifecycleSetupNone migrationLifecycleSetup = iota
	migrationLifecycleSetupPrefix
	migrationLifecycleSetupFull
	migrationLifecycleSetupLegacy
	migrationLifecycleSetupInconsistent
	migrationLifecycleSetupFailure
)

type migrationLifecycleTarget struct {
	key  migrations.MigrationKey
	zero bool
}

type migrationLifecycleRequestSpec struct {
	mode    string
	latest  bool
	targets []migrationLifecycleTarget
}

func migrationLifecycleLatestRequest() migrationLifecycleRequestSpec {
	return migrationLifecycleRequestSpec{
		mode:   "latest",
		latest: true,
	}
}

func migrationLifecycleNamedRequest(key migrations.MigrationKey) migrationLifecycleRequestSpec {
	return migrationLifecycleRequestSpec{mode: "named", targets: []migrationLifecycleTarget{{key: key}}}
}

func migrationLifecycleZeroRequest(app string) migrationLifecycleRequestSpec {
	return migrationLifecycleRequestSpec{mode: "app_zero", targets: []migrationLifecycleTarget{{key: migrations.MigrationKey{App: app}, zero: true}}}
}

func (spec migrationLifecycleRequestSpec) lifecycleRequest() (migrations.LifecycleRequest, error) {
	if spec.latest {
		return migrations.LatestLifecycleRequest(), nil
	}
	targets, err := spec.plannerTargets()
	if err != nil {
		return migrations.LifecycleRequest{}, err
	}
	if len(targets) == 0 {
		return migrations.LifecycleRequest{}, errors.New("migration lifecycle targeted request is empty")
	}
	return migrations.TargetedLifecycleRequest(targets[0], targets[1:]...), nil
}

func (spec migrationLifecycleRequestSpec) plannerTargets() ([]migrations.Target, error) {
	targets := make([]migrations.Target, 0, len(spec.targets))
	for _, target := range spec.targets {
		if target.zero {
			if target.key.App == "" || target.key.Name != "" {
				return nil, fmt.Errorf("invalid migration lifecycle zero target %#v", target.key)
			}
			targets = append(targets, migrations.ZeroTarget(target.key.App))
			continue
		}
		if target.key.App == "" || target.key.Name == "" {
			return nil, fmt.Errorf("invalid migration lifecycle named target %#v", target.key)
		}
		targets = append(targets, migrations.NamedTarget(target.key))
	}
	return targets, nil
}

func (spec migrationLifecycleRequestSpec) value(definitions []migrations.Migration) protocol.Value {
	requestTargets := spec.targets
	if spec.latest {
		keys := migrationLifecycleLatestKeys(definitions)
		requestTargets = make([]migrationLifecycleTarget, len(keys))
		for index, key := range keys {
			requestTargets[index] = migrationLifecycleTarget{key: key}
		}
	}
	targets := make([]protocol.Value, 0, len(requestTargets))
	for _, target := range requestTargets {
		name := protocol.String(target.key.Name)
		if target.zero {
			name = protocol.Null()
		}
		targets = append(targets, protocol.Object(map[string]protocol.Value{
			"app":  protocol.String(target.key.App),
			"name": name,
		}))
	}
	return protocol.Object(map[string]protocol.Value{
		"mode":    protocol.String(spec.mode),
		"targets": protocol.List(targets...),
	})
}

type migrationLifecycleFixture struct {
	phase        protocol.Phase
	setup        migrationLifecycleSetup
	setupTargets []migrationLifecycleTarget
	request      migrationLifecycleRequestSpec
	definitions  []migrations.Migration
	fault        *migrations.MigrationKey
	restart      bool
}

var migrationLifecycleFixtures = map[string]func() migrationLifecycleFixture{
	"django.migration.lifecycle.fresh_latest": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, request: migrationLifecycleLatestRequest()}
	},
	"django.migration.lifecycle.applied_prefix_latest": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, setup: migrationLifecycleSetupPrefix, request: migrationLifecycleLatestRequest()}
	},
	"django.migration.lifecycle.fully_applied_latest_noop": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, setup: migrationLifecycleSetupFull, request: migrationLifecycleLatestRequest()}
	},
	"django.migration.lifecycle.named_forward_target": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, request: migrationLifecycleNamedRequest(migrationLifecycleA2)}
	},
	"django.migration.lifecycle.named_reverse_target": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, setup: migrationLifecycleSetupFull, request: migrationLifecycleNamedRequest(migrationLifecycleA1)}
	},
	"django.migration.lifecycle.zero_target": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, setup: migrationLifecycleSetupFull, request: migrationLifecycleZeroRequest("alpha")}
	},
	"django.migration.lifecycle.unknown_legacy_tail": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, setup: migrationLifecycleSetupLegacy, request: migrationLifecycleLatestRequest()}
	},
	"django.migration.lifecycle.inconsistent_history_preflight": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseEvaluation, setup: migrationLifecycleSetupInconsistent, request: migrationLifecycleLatestRequest()}
	},
	"django.migration.lifecycle.middle_forward_failure": func() migrationLifecycleFixture {
		key := migrationLifecycleA2
		return migrationLifecycleFixture{phase: protocol.PhaseRollback, request: migrationLifecycleLatestRequest(), fault: &key}
	},
	"django.migration.lifecycle.restart_after_failure": func() migrationLifecycleFixture {
		return migrationLifecycleFixture{phase: protocol.PhaseCommit, setup: migrationLifecycleSetupFailure, request: migrationLifecycleLatestRequest(), restart: true}
	},
}

func migrationLifecycleScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	factory, ok := migrationLifecycleFixtures[contract.Scenario]
	if !ok {
		return protocol.Observation{}, fmt.Errorf("unsupported migration lifecycle scenario %q", contract.Scenario)
	}
	fixture := factory()
	if fixture.phase != contract.Phase {
		return protocol.Observation{}, fmt.Errorf("scenario %q phase = %q, manifest requires %q", contract.Scenario, fixture.phase, contract.Phase)
	}
	return runMigrationLifecycleFixture(ctx, contract.ID, fixture)
}

func runMigrationLifecycleFixture(
	ctx context.Context,
	contractID string,
	fixture migrationLifecycleFixture,
) (observation protocol.Observation, resultErr error) {
	if ctx == nil {
		return protocol.Observation{}, errors.New("migration lifecycle scenario: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return protocol.Observation{}, err
	}

	directory, err := os.MkdirTemp("", "godj-migration-lifecycle-")
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("create migration lifecycle directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(directory)) }()
	databasePath := filepath.Join(directory, "lifecycle.sqlite3")
	observer, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, observer.Close()) }()
	if err := observer.PingContext(ctx); err != nil {
		return protocol.Observation{}, fmt.Errorf("ping migration lifecycle observer: %w", err)
	}

	setupBackend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := provisionMigrationLifecycleProbe(ctx, setupBackend); err != nil {
		return protocol.Observation{}, errors.Join(err, setupBackend.Close())
	}
	definitions := fixture.definitions
	if definitions == nil {
		definitions = migrationLifecycleDefinitions()
	}
	setupValue, setupErr := setupMigrationLifecycleDatabase(
		ctx,
		setupBackend,
		observer,
		fixture.setup,
		definitions,
		fixture.setupTargets,
	)
	setupCloseErr := setupBackend.Close()
	if setupErr != nil || setupCloseErr != nil {
		return protocol.Observation{}, errors.Join(setupErr, setupCloseErr)
	}

	captureBackend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, captureBackend.Close()) }()
	before, err := migrationLifecycleDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("snapshot migration lifecycle database before execution: %w", err)
	}
	plan, historyValid, planInvoked, planErr := migrationLifecyclePlan(definitions, before.records, fixture.request)
	trace := newMigrationLifecycleTraceBackend(captureBackend, fixture.fault)
	request, err := fixture.request.lifecycleRequest()
	if err != nil {
		return protocol.Observation{}, err
	}
	returnedState, executionErr := migrationLifecycleMigrate(
		ctx,
		migrations.Executor{Backend: trace},
		definitions,
		request,
	)
	if err := trace.validate(plan, planErr, executionErr); err != nil {
		return protocol.Observation{}, fmt.Errorf("validate migration lifecycle trace: %w", err)
	}
	if err := validateMigrationLifecyclePlanningError(planErr, executionErr, fixture.fault); err != nil {
		return protocol.Observation{}, err
	}
	after, err := migrationLifecycleDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("snapshot migration lifecycle database after execution: %w", err)
	}
	connection, err := migrationExecutionConnectionValue(ctx, captureBackend, observer)
	if err != nil {
		return protocol.Observation{}, err
	}
	steps, unstarted, err := migrationLifecycleStepValues(plan, trace.steps)
	if err != nil {
		return protocol.Observation{}, err
	}

	metrics := map[string]protocol.Value{
		"capture_boundary": protocol.String("fresh_file_connection_loader_executor"),
		"connection":       connection,
		"effects": protocol.Object(map[string]protocol.Value{
			"database_state_changed": protocol.Boolean(!reflect.DeepEqual(before.value, after.value)),
			"ddl_observed":           protocol.Boolean(trace.ddlObserved()),
			"transaction_observed":   protocol.Boolean(len(trace.steps) != 0),
			"write_observed":         protocol.Boolean(len(trace.steps) != 0),
		}),
		"history_preflight": protocol.Object(map[string]protocol.Value{
			"history_check_invoked": protocol.Boolean(trace.snapshotReads == 1),
			"history_valid":         protocol.Boolean(historyValid),
			"migrate_invoked":       protocol.Boolean(planInvoked),
			"plan_invoked":          protocol.Boolean(planInvoked),
		}),
		"recorder_bootstrap":   protocol.String(migrationLifecycleRecorderBootstrap(before, after)),
		"request":              fixture.request.value(definitions),
		"steps":                protocol.List(steps...),
		"unstarted_tail_count": protocol.Integer(fmt.Sprint(unstarted)),
	}
	if fixture.fault != nil {
		metrics["failure_step"] = migrationLifecycleKeyValue(*fixture.fault)
	}
	if fixture.restart {
		metrics["restart"] = protocol.Object(map[string]protocol.Value{
			"connection_reopened": protocol.Boolean(setupBackend != captureBackend),
			"database_kind":       protocol.String("temporary_file"),
			"setup":               setupValue,
		})
	}

	observation = protocol.Observation{
		ID:     contractID,
		Status: protocol.StatusObserved,
		Phase:  fixture.phase,
		DBState: valuePointer(protocol.Object(map[string]protocol.Value{
			"after":  after.value,
			"before": before.value,
		})),
		Metrics: valuePointer(protocol.Object(metrics)),
	}
	if executionErr == nil {
		stateValue, err := migrationStateProjectValue(returnedState)
		if err != nil {
			return protocol.Observation{}, err
		}
		observation.Result = valuePointer(protocol.Object(map[string]protocol.Value{
			"plan":           protocol.List(migrationExecutionPlanValues(plan)...),
			"returned_state": stateValue,
		}))
		return observation, nil
	}
	observedError, err := migrationLifecycleErrorValue(executionErr)
	if err != nil {
		return protocol.Observation{}, err
	}
	observation.Error = observedError
	return observation, nil
}

func provisionMigrationLifecycleProbe(ctx context.Context, backend *sqlite.Backend) error {
	statements := []string{
		`CREATE TABLE "external_execution_probe" ("value" INTEGER NOT NULL)`,
		`INSERT INTO "external_execution_probe" ("value") VALUES (1)`,
	}
	for _, statement := range statements {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision migration lifecycle connection probe: %w", err)
		}
	}
	return nil
}

func setupMigrationLifecycleDatabase(
	ctx context.Context,
	backend *sqlite.Backend,
	observer *sql.DB,
	setup migrationLifecycleSetup,
	definitions []migrations.Migration,
	setupTargets []migrationLifecycleTarget,
) (protocol.Value, error) {
	executor := migrations.Executor{Backend: backend}
	switch setup {
	case migrationLifecycleSetupNone:
		if len(setupTargets) != 0 {
			return protocol.Value{}, errors.New("migration lifecycle no-op setup cannot have targets")
		}
		return protocol.Null(), nil
	case migrationLifecycleSetupPrefix:
		request, err := migrationLifecycleSetupRequest(
			[]migrationLifecycleTarget{{key: migrationLifecycleA1}},
			setupTargets,
		)
		if err != nil {
			return protocol.Value{}, err
		}
		_, err = migrationLifecycleMigrate(
			ctx,
			executor,
			definitions,
			request,
		)
		return protocol.Null(), err
	case migrationLifecycleSetupFull:
		if len(setupTargets) != 0 {
			return protocol.Value{}, errors.New("migration lifecycle full setup cannot have targets")
		}
		_, err := migrationLifecycleMigrate(ctx, executor, definitions, migrations.LatestLifecycleRequest())
		return protocol.Null(), err
	case migrationLifecycleSetupLegacy:
		setupDefinitions := append(append([]migrations.Migration(nil), definitions...), migrations.Migration{
			App: migrationLifecycleLegacy.App, Name: migrationLifecycleLegacy.Name,
		})
		request, err := migrationLifecycleSetupRequest(
			[]migrationLifecycleTarget{{key: migrationLifecycleA1}, {key: migrationLifecycleLegacy}},
			setupTargets,
		)
		if err != nil {
			return protocol.Value{}, err
		}
		_, err = migrationLifecycleMigrate(
			ctx,
			executor,
			setupDefinitions,
			request,
		)
		return protocol.Null(), err
	case migrationLifecycleSetupInconsistent:
		if len(setupTargets) != 0 {
			return protocol.Value{}, errors.New("migration lifecycle inconsistent setup cannot have targets")
		}
		_, err := migrationLifecycleMigrate(
			ctx,
			executor,
			[]migrations.Migration{{App: migrationLifecycleA2.App, Name: migrationLifecycleA2.Name}},
			migrations.LatestLifecycleRequest(),
		)
		return protocol.Null(), err
	case migrationLifecycleSetupFailure:
		if len(setupTargets) != 0 {
			return protocol.Value{}, errors.New("migration lifecycle failure setup cannot have targets")
		}
		return setupFailedMigrationLifecycle(ctx, backend, observer, definitions)
	default:
		return protocol.Value{}, fmt.Errorf("unsupported migration lifecycle setup %d", setup)
	}
}

func migrationLifecycleSetupRequest(
	defaultTargets []migrationLifecycleTarget,
	overrideTargets []migrationLifecycleTarget,
) (migrations.LifecycleRequest, error) {
	targets := defaultTargets
	if len(overrideTargets) != 0 {
		targets = overrideTargets
	}
	return (migrationLifecycleRequestSpec{mode: "setup", targets: targets}).lifecycleRequest()
}

func setupFailedMigrationLifecycle(
	ctx context.Context,
	backend *sqlite.Backend,
	observer *sql.DB,
	definitions []migrations.Migration,
) (protocol.Value, error) {
	before, err := migrationLifecycleDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	requestSpec := migrationLifecycleLatestRequest()
	plan, _, _, err := migrationLifecyclePlan(definitions, before.records, requestSpec)
	if err != nil {
		return protocol.Value{}, err
	}
	fault := migrationLifecycleA2
	trace := newMigrationLifecycleTraceBackend(backend, &fault)
	_, executionErr := migrationLifecycleMigrate(
		ctx,
		migrations.Executor{Backend: trace},
		definitions,
		migrations.LatestLifecycleRequest(),
	)
	if executionErr == nil {
		return protocol.Value{}, errors.New("migration lifecycle failure setup unexpectedly succeeded")
	}
	if err := trace.validate(plan, nil, executionErr); err != nil {
		return protocol.Value{}, err
	}
	migrationErr := new(migrations.Error)
	if !errors.As(executionErr, &migrationErr) || migrationErr.Code != migrations.CodeOperationFailed {
		return protocol.Value{}, fmt.Errorf("migration lifecycle failure setup error = %T %v", executionErr, executionErr)
	}
	after, err := migrationLifecycleDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	steps, _, err := migrationLifecycleStepValues(plan, trace.steps)
	if err != nil {
		return protocol.Value{}, err
	}
	return protocol.Object(map[string]protocol.Value{
		"durable_prefix":     after.recordsValue,
		"error_code":         protocol.String(string(migrationErr.Code)),
		"plan":               protocol.List(migrationExecutionPlanValues(plan)...),
		"recorder_bootstrap": protocol.String(migrationLifecycleRecorderBootstrap(before, after)),
		"steps":              protocol.List(steps...),
	}), nil
}

func migrationLifecycleDefinitions() []migrations.Migration {
	return []migrations.Migration{
		{
			App: migrationLifecycleA1.App, Name: migrationLifecycleA1.Name,
			Operations: []migrations.Operation{migrations.CreateModel{
				AppLabel: migrationLifecycleA1.App,
				Model: ir.Model{
					Name: "entry", GoName: "Entry", DBTable: migrationLifecycleAlphaTable,
					Fields: []ir.Field{
						migrationExecutionAutoField(),
						{
							Name: "a1_marker", GoName: "A1Marker", Column: "a1_marker", Kind: ir.FieldChar, MaxLength: 16,
							Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "a1"},
						},
					},
				},
			}},
		},
		{
			App: migrationLifecycleA2.App, Name: migrationLifecycleA2.Name,
			Dependencies: []migrations.MigrationKey{migrationLifecycleA1},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: migrationLifecycleA2.App, ModelName: "entry",
				Field: ir.Field{
					Name: "a2_marker", GoName: "A2Marker", Column: "a2_marker", Kind: ir.FieldBoolean,
					Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false},
				},
			}},
		},
		{
			App: migrationLifecycleA3.App, Name: migrationLifecycleA3.Name,
			Dependencies: []migrations.MigrationKey{migrationLifecycleA2},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: migrationLifecycleA3.App, ModelName: "entry",
				Field: ir.Field{
					Name: "a3_marker", GoName: "A3Marker", Column: "a3_marker", Kind: ir.FieldChar, Nullable: true, MaxLength: 16,
				},
			}},
		},
		{
			App: migrationLifecycleB1.App, Name: migrationLifecycleB1.Name,
			Dependencies: []migrations.MigrationKey{migrationLifecycleA1},
			Operations: []migrations.Operation{migrations.CreateModel{
				AppLabel: migrationLifecycleB1.App,
				Model: ir.Model{
					Name: "branch", GoName: "Branch", DBTable: migrationLifecycleBetaTable,
					Fields: []ir.Field{
						migrationExecutionAutoField(),
						{
							Name: "b1_marker", GoName: "B1Marker", Column: "b1_marker", Kind: ir.FieldChar, MaxLength: 16,
							Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "b1"},
						},
					},
				},
			}},
		},
	}
}

func migrationLifecycleMigrate(
	ctx context.Context,
	executor migrations.Executor,
	definitions []migrations.Migration,
	request migrations.LifecycleRequest,
) (migrations.ProjectState, error) {
	loaded, _, err := definition.Load(migrationLifecycleDefinitionSources(definitions)...)
	if err != nil {
		return migrations.ProjectState{}, fmt.Errorf("load migration lifecycle definitions: %w", err)
	}
	return executor.Migrate(ctx, loaded, request)
}

func migrationLifecycleDefinitionSources(definitions []migrations.Migration) []definition.Source {
	sources := make([]definition.Source, len(definitions))
	for index, migration := range definitions {
		dependencies := make([]any, len(migration.Dependencies))
		for dependencyIndex, dependency := range migration.Dependencies {
			dependencies[dependencyIndex] = map[string]any{
				"app":  dependency.App,
				"name": dependency.Name,
			}
		}
		operations := make([]any, len(migration.Operations))
		for operationIndex, operation := range migration.Operations {
			operations[operationIndex] = migrationLifecycleDefinitionOperation(operation)
		}
		document := map[string]any{
			"format_version": definition.DefinitionFormatVersion,
			"migration": map[string]any{
				"app":          migration.App,
				"dependencies": dependencies,
				"name":         migration.Name,
				"operations":   operations,
			},
			"producer": map[string]any{
				"name":    "godj-conformance",
				"version": "current",
			},
		}
		sources[index] = definition.Source{
			SourceID: fmt.Sprintf("lifecycle-%04d-%s-%s", index, migration.App, migration.Name),
			Document: migrationDefinitionMarshal(document, false),
		}
	}
	return sources
}

func migrationLifecycleDefinitionOperation(operation migrations.Operation) map[string]any {
	switch current := operation.(type) {
	case migrations.CreateModel:
		return map[string]any{
			"app_label": current.AppLabel,
			"kind":      "create_model",
			"model":     migrationLifecycleDefinitionModel(current.Model),
		}
	case *migrations.CreateModel:
		if current == nil {
			return nil
		}
		return migrationLifecycleDefinitionOperation(*current)
	case migrations.AddField:
		return map[string]any{
			"app_label":  current.AppLabel,
			"field":      migrationLifecycleDefinitionField(current.Field),
			"kind":       "add_field",
			"model_name": current.ModelName,
		}
	case *migrations.AddField:
		if current == nil {
			return nil
		}
		return migrationLifecycleDefinitionOperation(*current)
	default:
		return nil
	}
}

func migrationLifecycleDefinitionModel(model ir.Model) map[string]any {
	fields := make([]any, len(model.Fields))
	for index, field := range model.Fields {
		fields[index] = migrationLifecycleDefinitionField(field)
	}
	return map[string]any{
		"db_table": model.DBTable,
		"fields":   fields,
		"go_name":  model.GoName,
		"name":     model.Name,
	}
}

func migrationLifecycleDefinitionField(field ir.Field) map[string]any {
	document := map[string]any{
		"column":      field.Column,
		"default":     migrationLifecycleDefinitionDefault(field.Default),
		"go_name":     field.GoName,
		"kind":        string(field.Kind),
		"max_length":  field.MaxLength,
		"name":        field.Name,
		"nullable":    field.Nullable,
		"primary_key": field.PrimaryKey,
	}
	if field.Relation != nil {
		document["relation"] = map[string]any{
			"cardinality": string(field.Relation.Cardinality),
			"on_delete":   string(field.Relation.OnDelete),
			"reverse": map[string]any{
				"disabled": field.Relation.Reverse.Disabled,
				"name":     field.Relation.Reverse.Name,
			},
			"target": map[string]any{
				"app_label":  field.Relation.Target.AppLabel,
				"model_name": field.Relation.Target.ModelName,
			},
		}
	}
	return document
}

func migrationLifecycleDefinitionDefault(value *ir.ScalarDefault) any {
	if value == nil {
		return nil
	}
	switch value.Kind {
	case ir.ScalarString:
		return map[string]any{"kind": string(value.Kind), "string": value.String}
	case ir.ScalarBoolean:
		return map[string]any{"boolean": value.Boolean, "kind": string(value.Kind)}
	case ir.ScalarInteger:
		return map[string]any{"integer": value.Integer, "kind": string(value.Kind)}
	default:
		return map[string]any{"kind": string(value.Kind)}
	}
}

type migrationLifecycleSnapshot struct {
	value           protocol.Value
	records         []migrations.MigrationKey
	recordsValue    protocol.Value
	recorderPresent bool
}

func migrationLifecycleDatabaseSnapshot(ctx context.Context, observer *sql.DB) (migrationLifecycleSnapshot, error) {
	rows, err := observer.QueryContext(
		ctx,
		`SELECT "name" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" LIKE 'godj_lifecycle_%' ORDER BY "name"`,
	)
	if err != nil {
		return migrationLifecycleSnapshot{}, fmt.Errorf("list migration lifecycle schema: %w", err)
	}
	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return migrationLifecycleSnapshot{}, fmt.Errorf("scan migration lifecycle table: %w", err)
		}
		tableNames = append(tableNames, name)
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil || closeErr != nil {
		return migrationLifecycleSnapshot{}, errors.Join(iterateErr, closeErr)
	}
	managedSchema := make([]protocol.Value, 0, len(tableNames))
	for _, table := range tableNames {
		columns, err := migrationExecutionColumns(ctx, observer, table)
		if err != nil {
			return migrationLifecycleSnapshot{}, err
		}
		managedSchema = append(managedSchema, protocol.Object(map[string]protocol.Value{
			"columns": protocol.List(columns...),
			"name":    protocol.String(table),
		}))
	}
	recorderPresent, err := sqliteTableExistsContext(ctx, observer, goDjMigrationRecordTable)
	if err != nil {
		return migrationLifecycleSnapshot{}, err
	}
	records := make([]migrations.MigrationKey, 0)
	recordValues := make([]protocol.Value, 0)
	if recorderPresent {
		rows, err := observer.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
		if err != nil {
			return migrationLifecycleSnapshot{}, fmt.Errorf("read migration lifecycle records: %w", err)
		}
		for rows.Next() {
			var key migrations.MigrationKey
			if err := rows.Scan(&key.App, &key.Name); err != nil {
				_ = rows.Close()
				return migrationLifecycleSnapshot{}, fmt.Errorf("scan migration lifecycle record: %w", err)
			}
			records = append(records, key)
			recordValues = append(recordValues, migrationLifecycleKeyValue(key))
		}
		iterateErr := rows.Err()
		closeErr := rows.Close()
		if iterateErr != nil || closeErr != nil {
			return migrationLifecycleSnapshot{}, errors.Join(iterateErr, closeErr)
		}
	}
	recordsValue := protocol.List(recordValues...)
	value := protocol.Object(map[string]protocol.Value{
		"managed_schema":    protocol.List(managedSchema...),
		"migration_records": recordsValue,
		"recorder_present":  protocol.Boolean(recorderPresent),
	})
	return migrationLifecycleSnapshot{
		value: value, records: records, recordsValue: recordsValue, recorderPresent: recorderPresent,
	}, nil
}

func migrationLifecyclePlan(
	definitions []migrations.Migration,
	records []migrations.MigrationKey,
	request migrationLifecycleRequestSpec,
) ([]migrations.PlanStep, bool, bool, error) {
	planner, err := migrations.NewPlanner(definitions...)
	if err != nil {
		return nil, false, false, err
	}
	applied, err := migrations.NewAppliedState(records...)
	if err != nil {
		return nil, false, false, err
	}
	if err := planner.CheckHistory(applied); err != nil {
		return nil, false, false, err
	}
	targets, err := migrationLifecyclePlanTargets(definitions, request)
	if err != nil {
		return nil, true, false, err
	}
	plan, err := planner.Plan(applied, targets...)
	return plan, true, true, err
}

func migrationLifecyclePlanTargets(
	definitions []migrations.Migration,
	request migrationLifecycleRequestSpec,
) ([]migrations.Target, error) {
	if !request.latest {
		return request.plannerTargets()
	}
	keys := migrationLifecycleLatestKeys(definitions)
	targets := make([]migrations.Target, len(keys))
	for index, key := range keys {
		targets[index] = migrations.NamedTarget(key)
	}
	return targets, nil
}

func migrationLifecycleLatestKeys(definitions []migrations.Migration) []migrations.MigrationKey {
	hasSameAppChild := make(map[migrations.MigrationKey]bool, len(definitions))
	keys := make([]migrations.MigrationKey, 0, len(definitions))
	for _, definition := range definitions {
		key := migrations.MigrationKey{App: definition.App, Name: definition.Name}
		keys = append(keys, key)
		for _, dependency := range definition.Dependencies {
			if dependency.App == definition.App {
				hasSameAppChild[dependency] = true
			}
		}
	}
	leaves := make([]migrations.MigrationKey, 0, len(keys))
	for _, key := range keys {
		if !hasSameAppChild[key] {
			leaves = append(leaves, key)
		}
	}
	sort.Slice(leaves, func(left, right int) bool {
		if leaves[left].App != leaves[right].App {
			return leaves[left].App < leaves[right].App
		}
		return leaves[left].Name < leaves[right].Name
	})
	return leaves
}

func migrationLifecycleRecorderBootstrap(before, after migrationLifecycleSnapshot) string {
	if before.recorderPresent {
		return "existing"
	}
	if after.recorderPresent {
		return "created"
	}
	return "absent"
}

func migrationLifecycleKeyValue(key migrations.MigrationKey) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"app":  protocol.String(key.App),
		"name": protocol.String(key.Name),
	})
}

func validateMigrationLifecyclePlanningError(planErr, executionErr error, fault *migrations.MigrationKey) error {
	if planErr != nil {
		if executionErr == nil {
			return errors.New("migration lifecycle planning failed but Migrate succeeded")
		}
		want := new(migrations.PlanningError)
		got := new(migrations.PlanningError)
		if !errors.As(planErr, &want) || !errors.As(executionErr, &got) || want.Category != got.Category || want.Code != got.Code {
			return fmt.Errorf("migration lifecycle planning mismatch: expected %v, got %v", planErr, executionErr)
		}
		return nil
	}
	if fault == nil && executionErr != nil {
		return fmt.Errorf("migration lifecycle execution unexpectedly failed: %w", executionErr)
	}
	if fault != nil && executionErr == nil {
		return errors.New("migration lifecycle fault unexpectedly succeeded")
	}
	return nil
}

func migrationLifecycleErrorValue(err error) (*protocol.ObservedError, error) {
	category := migrations.ErrorCategory("")
	code := migrations.ErrorCode("")
	pythonType := ""
	planningErr := new(migrations.PlanningError)
	migrationErr := new(migrations.Error)
	switch {
	case errors.As(err, &planningErr):
		category = planningErr.Category
		code = planningErr.Code
		if code == migrations.CodeInconsistentAppliedHistory {
			pythonType = "django.db.migrations.exceptions.InconsistentMigrationHistory"
		}
	case errors.As(err, &migrationErr):
		category = migrationErr.Category
		code = migrationErr.Code
		if code == migrations.CodeOperationFailed && errors.Is(err, errForcedMigrationLifecycleMiddleFailure) {
			pythonType = "conformance.runners.django.migration_lifecycle_scenarios.ConformanceLifecycleOperationFailure"
		}
	default:
		return nil, fmt.Errorf("migration lifecycle returned unstructured error %T: %w", err, err)
	}
	if pythonType == "" {
		return nil, fmt.Errorf("migration lifecycle error %s/%s has no protocol type mapping", category, code)
	}
	return &protocol.ObservedError{
		Category:          string(category),
		Code:              string(code),
		PythonType:        pythonType,
		MessageIsContract: boolPointer(false),
	}, nil
}

type migrationLifecycleTraceBackend struct {
	delegate      *sqlite.Backend
	fault         *migrations.MigrationKey
	snapshotReads int
	sessionCloses int
	steps         []*migrationLifecycleTraceStep
}

var _ migrationbackend.RevisionFencedBackend = (*migrationLifecycleTraceBackend)(nil)

func newMigrationLifecycleTraceBackend(
	delegate *sqlite.Backend,
	fault *migrations.MigrationKey,
) *migrationLifecycleTraceBackend {
	var faultCopy *migrations.MigrationKey
	if fault != nil {
		copy := *fault
		faultCopy = &copy
	}
	return &migrationLifecycleTraceBackend{delegate: delegate, fault: faultCopy}
}

func (trace *migrationLifecycleTraceBackend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	return trace.delegate.MigrationCapabilities()
}

func (trace *migrationLifecycleTraceBackend) OpenRevisionFencedSession(
	ctx context.Context,
) (migrationbackend.RevisionFencedSession, error) {
	session, err := trace.delegate.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	return &migrationLifecycleTraceSession{delegate: session, owner: trace}, nil
}

func (trace *migrationLifecycleTraceBackend) ddlObserved() bool {
	for _, step := range trace.steps {
		if step.schemaStarted {
			return true
		}
	}
	return false
}

func (trace *migrationLifecycleTraceBackend) validate(plan []migrations.PlanStep, planErr, executionErr error) error {
	var validationErr error
	if trace.snapshotReads != 1 {
		validationErr = errors.Join(validationErr, fmt.Errorf("history snapshot reads = %d, want 1", trace.snapshotReads))
	}
	if trace.sessionCloses != 1 {
		validationErr = errors.Join(validationErr, fmt.Errorf("session closes = %d, want 1", trace.sessionCloses))
	}
	if planErr != nil {
		if len(trace.steps) != 0 {
			validationErr = errors.Join(validationErr, fmt.Errorf("history failure opened %d transactions", len(trace.steps)))
		}
		return validationErr
	}
	if executionErr == nil && len(trace.steps) != len(plan) {
		validationErr = errors.Join(validationErr, fmt.Errorf("successful lifecycle opened %d transactions for %d plan steps", len(trace.steps), len(plan)))
	}
	if len(trace.steps) > len(plan) {
		validationErr = errors.Join(validationErr, fmt.Errorf("lifecycle opened %d transactions for %d plan steps", len(trace.steps), len(plan)))
	}
	for index, step := range trace.steps {
		if index >= len(plan) {
			break
		}
		if step.key != plan[index].Key || step.direction != plan[index].Direction {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"transaction %d = %s.%s/%s, want %s.%s/%s",
				index,
				step.key.App,
				step.key.Name,
				step.direction,
				plan[index].Key.App,
				plan[index].Key.Name,
				plan[index].Direction,
			))
		}
		if step.committed == step.rolledBack {
			validationErr = errors.Join(validationErr, fmt.Errorf("transaction %d terminal committed=%t rolled_back=%t", index, step.committed, step.rolledBack))
		}
		if step.committed && (!step.schemaStarted || !step.recorderSucceeded) {
			validationErr = errors.Join(validationErr, fmt.Errorf("transaction %d committed without schema and recorder boundaries", index))
		}
	}
	return validationErr
}

type migrationLifecycleTraceSession struct {
	delegate migrationbackend.RevisionFencedSession
	owner    *migrationLifecycleTraceBackend
}

func (session *migrationLifecycleTraceSession) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	session.owner.snapshotReads++
	return session.delegate.ReadAppliedMigrations(ctx)
}

func (session *migrationLifecycleTraceSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	direction := migrations.DirectionForward
	if transition.Kind == migrationbackend.HistoryTransitionUnapply {
		direction = migrations.DirectionBackward
	} else if transition.Kind != migrationbackend.HistoryTransitionApply {
		return nil, fmt.Errorf("migration lifecycle trace saw transition kind %d", transition.Kind)
	}
	delegate, err := session.delegate.BeginMigration(ctx, transition, intent)
	if err != nil {
		return nil, err
	}
	step := &migrationLifecycleTraceStep{
		key:       migrations.MigrationKey{App: transition.Migration.App, Name: transition.Migration.Name},
		direction: direction,
	}
	session.owner.steps = append(session.owner.steps, step)
	return &migrationLifecycleTraceTransaction{delegate: delegate, owner: session.owner, step: step}, nil
}

func (session *migrationLifecycleTraceSession) Close(ctx context.Context) error {
	err := session.delegate.Close(ctx)
	session.owner.sessionCloses++
	return err
}

type migrationLifecycleTraceStep struct {
	key               migrations.MigrationKey
	direction         migrations.Direction
	schemaStarted     bool
	recorderStarted   bool
	recorderSucceeded bool
	committed         bool
	rolledBack        bool
}

type migrationLifecycleTraceTransaction struct {
	delegate migrationbackend.RevisionFencedTransaction
	owner    *migrationLifecycleTraceBackend
	step     *migrationLifecycleTraceStep
}

func (transaction *migrationLifecycleTraceTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	transaction.step.schemaStarted = true
	return transaction.delegate.CreateModel(ctx, model)
}

func (transaction *migrationLifecycleTraceTransaction) DeleteModel(ctx context.Context, model ir.Model) error {
	transaction.step.schemaStarted = true
	return transaction.delegate.DeleteModel(ctx, model)
}

func (transaction *migrationLifecycleTraceTransaction) AddField(ctx context.Context, model ir.Model, field ir.Field) error {
	transaction.step.schemaStarted = true
	if err := transaction.delegate.AddField(ctx, model, field); err != nil {
		return err
	}
	if transaction.owner.fault != nil && *transaction.owner.fault == transaction.step.key {
		return errForcedMigrationLifecycleMiddleFailure
	}
	return nil
}

func (transaction *migrationLifecycleTraceTransaction) RemoveField(ctx context.Context, model ir.Model, field ir.Field) error {
	transaction.step.schemaStarted = true
	return transaction.delegate.RemoveField(ctx, model, field)
}

func (transaction *migrationLifecycleTraceTransaction) RecordApplied(ctx context.Context, app, name string) error {
	transaction.step.recorderStarted = true
	err := transaction.delegate.RecordApplied(ctx, app, name)
	transaction.step.recorderSucceeded = err == nil
	return err
}

func (transaction *migrationLifecycleTraceTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	transaction.step.recorderStarted = true
	err := transaction.delegate.RecordUnapplied(ctx, app, name)
	transaction.step.recorderSucceeded = err == nil
	return err
}

func (transaction *migrationLifecycleTraceTransaction) CommitFenced(
	ctx context.Context,
) (migrationbackend.CommitOutcome, error) {
	outcome, err := transaction.delegate.CommitFenced(ctx)
	switch outcome.Durability {
	case migrationbackend.CommitCommitted:
		transaction.step.committed = true
	case migrationbackend.CommitRolledBack:
		transaction.step.rolledBack = true
	}
	return outcome, err
}

func (transaction *migrationLifecycleTraceTransaction) Rollback(ctx context.Context) error {
	err := transaction.delegate.Rollback(ctx)
	transaction.step.rolledBack = err == nil
	return err
}

func migrationLifecycleStepValues(
	plan []migrations.PlanStep,
	started []*migrationLifecycleTraceStep,
) ([]protocol.Value, int, error) {
	steps := make([]protocol.Value, 0, len(plan))
	unstarted := 0
	for index, planStep := range plan {
		fields := map[string]protocol.Value{
			"app":       protocol.String(planStep.Key.App),
			"direction": protocol.String(string(planStep.Direction)),
			"name":      protocol.String(planStep.Key.Name),
		}
		if index >= len(started) {
			fields["outcome"] = protocol.String("not_started")
			fields["recorder_outcome"] = protocol.String("not_started")
			fields["schema_outcome"] = protocol.String("not_started")
			unstarted++
			steps = append(steps, protocol.Object(fields))
			continue
		}
		step := started[index]
		switch {
		case step.committed && !step.rolledBack:
			fields["outcome"] = protocol.String("committed")
			if planStep.Direction == migrations.DirectionForward {
				fields["recorder_outcome"] = protocol.String("applied")
				fields["schema_outcome"] = protocol.String("applied")
			} else {
				fields["recorder_outcome"] = protocol.String("unapplied")
				fields["schema_outcome"] = protocol.String("reversed")
			}
		case step.rolledBack && !step.committed:
			fields["outcome"] = protocol.String("rolled_back")
			if step.recorderStarted {
				fields["recorder_outcome"] = protocol.String("rolled_back")
			} else {
				fields["recorder_outcome"] = protocol.String("not_started")
			}
			fields["schema_outcome"] = protocol.String("rolled_back")
		default:
			return nil, 0, fmt.Errorf("migration lifecycle step %s.%s has no terminal outcome", planStep.Key.App, planStep.Key.Name)
		}
		steps = append(steps, protocol.Object(fields))
	}
	return steps, unstarted, nil
}
