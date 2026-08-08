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
	"strings"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

const (
	migrationStateDivergentTable  = "godj_state_live_decoy"
	migrationStateDivergentColumn = "wrong"
)

var (
	migrationStateAlphaRoot   = migrations.MigrationKey{App: "alpha", Name: "0002_root"}
	migrationStateAlphaMiddle = migrations.MigrationKey{App: "alpha", Name: "0001_middle"}
	migrationStateAlphaLeaf   = migrations.MigrationKey{App: "alpha", Name: "0003_leaf"}
	migrationStateBetaRoot    = migrations.MigrationKey{App: "beta", Name: "0001_root"}
	migrationStateGammaRoot   = migrations.MigrationKey{App: "gamma", Name: "0001_root"}
	migrationStateDeltaRoot   = migrations.MigrationKey{App: "delta", Name: "0001_root"}
	migrationStateLegacy      = migrations.MigrationKey{App: "legacy", Name: "0099_retired"}
)

type migrationStateRequestMode uint8

const (
	migrationStateExplicitEmpty migrationStateRequestMode = iota + 1
	migrationStateBefore
	migrationStateAfter
	migrationStateLatest
	migrationStateApplied
)

type migrationStateFixture struct {
	phase                protocol.Phase
	mode                 migrationStateRequestMode
	targets              []migrations.MigrationKey
	applied              []migrations.MigrationKey
	definitions          []migrations.Migration
	divergentTable       string
	divergentColumn      string
	additionalDivergence []migrationStateDivergentSchema
}

type migrationStateDivergentSchema struct {
	table  string
	column string
}

var migrationStateReconstructionFixtures = map[string]func() migrationStateFixture{
	"django.migration.state_reconstruction.explicit_empty": func() migrationStateFixture {
		return newMigrationStateFixture(migrationStateExplicitEmpty)
	},
	"django.migration.state_reconstruction.first_before": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateBefore)
		fixture.targets = []migrations.MigrationKey{migrationStateAlphaRoot}
		return fixture
	},
	"django.migration.state_reconstruction.first_after": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateAfter)
		fixture.targets = []migrations.MigrationKey{migrationStateAlphaRoot}
		return fixture
	},
	"django.migration.state_reconstruction.linear_middle_after": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateAfter)
		fixture.targets = []migrations.MigrationKey{migrationStateAlphaMiddle}
		return fixture
	},
	"django.migration.state_reconstruction.linear_middle_before": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateBefore)
		fixture.targets = []migrations.MigrationKey{migrationStateAlphaMiddle}
		return fixture
	},
	"django.migration.state_reconstruction.cross_app_dependency": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateAfter)
		fixture.targets = []migrations.MigrationKey{migrationStateBetaRoot}
		return fixture
	},
	"django.migration.state_reconstruction.multiple_targets_shared_dependency": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateAfter)
		fixture.targets = []migrations.MigrationKey{migrationStateBetaRoot, migrationStateGammaRoot}
		return fixture
	},
	"django.migration.state_reconstruction.latest_leaves": func() migrationStateFixture {
		return newMigrationStateFixture(migrationStateLatest)
	},
	"django.migration.state_reconstruction.applied_prefix_startup": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateApplied)
		fixture.applied = []migrations.MigrationKey{migrationStateAlphaRoot, migrationStateAlphaMiddle}
		return fixture
	},
	"django.migration.state_reconstruction.unrelated_known_unknown_startup": func() migrationStateFixture {
		fixture := newMigrationStateFixture(migrationStateApplied)
		fixture.applied = []migrations.MigrationKey{migrationStateAlphaRoot, migrationStateDeltaRoot, migrationStateLegacy}
		return fixture
	},
}

func newMigrationStateFixture(mode migrationStateRequestMode) migrationStateFixture {
	return migrationStateFixture{
		phase:           protocol.PhaseEvaluation,
		mode:            mode,
		definitions:     migrationStateDefinitions(),
		divergentTable:  migrationStateDivergentTable,
		divergentColumn: migrationStateDivergentColumn,
	}
}

func migrationStateReconstructionScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	factory, ok := migrationStateReconstructionFixtures[contract.Scenario]
	if !ok {
		return protocol.Observation{}, fmt.Errorf("unsupported scenario %q", contract.Scenario)
	}
	fixture := factory()
	if fixture.phase != contract.Phase {
		return protocol.Observation{}, fmt.Errorf("scenario %q phase = %q, manifest requires %q", contract.Scenario, fixture.phase, contract.Phase)
	}
	return runMigrationStateReconstructionFixture(ctx, contract.ID, fixture)
}

func runMigrationStateReconstructionFixture(ctx context.Context, contractID string, fixture migrationStateFixture) (protocol.Observation, error) {
	if ctx == nil {
		return protocol.Observation{}, errors.New("migration state reconstruction scenario: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return protocol.Observation{}, err
	}
	directory, err := os.MkdirTemp("", "godj-migration-state-")
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("create migration state directory: %w", err)
	}
	path := filepath.Join(directory, "default.sqlite3")
	observation, runErr := runMigrationStateReconstructionDatabase(ctx, contractID, path, fixture)
	cleanupErr := os.RemoveAll(directory)
	if runErr != nil || cleanupErr != nil {
		return protocol.Observation{}, errors.Join(runErr, cleanupErr)
	}
	return observation, nil
}

func runMigrationStateReconstructionDatabase(ctx context.Context, contractID, path string, fixture migrationStateFixture) (result protocol.Observation, resultErr error) {
	if err := setupMigrationStateReconstructionDatabase(ctx, path, fixture); err != nil {
		return protocol.Observation{}, err
	}

	dataSourceName := migrationRestartReadOnlyDataSource(path)
	observer, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("open migration state observer: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, observer.Close()) }()
	if err := observer.PingContext(ctx); err != nil {
		return protocol.Observation{}, fmt.Errorf("ping migration state observer: %w", err)
	}
	var appliedReader *migrationStateObservedReader
	if fixture.mode == migrationStateApplied {
		readerBackend, err := sqlite.Open(ctx, dataSourceName)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("open fresh migration state reader: %w", err)
		}
		defer func() { resultErr = errors.Join(resultErr, readerBackend.Close()) }()
		appliedReader = &migrationStateObservedReader{delegate: readerBackend}
	}

	before, err := migrationStateDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("snapshot migration state database before reconstruction: %w", err)
	}

	state, appliedKeys, err := captureMigrationStateReconstruction(ctx, fixture, appliedReader)
	if err != nil {
		return protocol.Observation{}, err
	}
	stateValue, err := migrationStateProjectValue(state)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("normalize reconstructed migration state: %w", err)
	}

	after, err := migrationStateDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("snapshot migration state database after reconstruction: %w", err)
	}
	if !reflect.DeepEqual(before, after) {
		return protocol.Observation{}, errors.New("migration state reconstruction mutated database state")
	}
	if err := migrationStateAssertDivergent(state, fixture); err != nil {
		return protocol.Observation{}, err
	}

	resultValue := protocol.Object(map[string]protocol.Value{"state": stateValue})
	if fixture.mode == migrationStateApplied {
		resultValue = migrationStateAppliedResult(stateValue, appliedKeys, fixture.definitions)
	}
	databaseState := protocol.Object(map[string]protocol.Value{"after": after, "before": before})
	metrics := migrationStateMetrics(fixture, before, after)
	return protocol.Observation{
		ID: contractID, Status: protocol.StatusObserved, Phase: fixture.phase,
		Result: &resultValue, DBState: &databaseState, Metrics: &metrics,
	}, nil
}

// captureMigrationStateReconstruction is the measured reconstruction seam.
// Its only I/O-capable input is an observed reader whose concrete delegate is
// opened read-only by the caller. Database setup, connection opening, and
// snapshot introspection stay outside this interval; the public reconstructor
// itself receives no database handle.
func captureMigrationStateReconstruction(ctx context.Context, fixture migrationStateFixture, appliedReader *migrationStateObservedReader) (migrations.ProjectState, []migrations.MigrationKey, error) {
	reconstructor, err := migrations.NewStateReconstructor(fixture.definitions...)
	if err != nil {
		return migrations.ProjectState{}, nil, fmt.Errorf("build migration state reconstructor: %w", err)
	}
	request, appliedKeys, err := migrationStateRequest(ctx, fixture, appliedReader)
	if err != nil {
		return migrations.ProjectState{}, nil, err
	}
	state, err := reconstructor.Reconstruct(request)
	if err != nil {
		return migrations.ProjectState{}, nil, fmt.Errorf("reconstruct migration state: %w", err)
	}
	return state, appliedKeys, nil
}

func setupMigrationStateReconstructionDatabase(ctx context.Context, path string, fixture migrationStateFixture) (resultErr error) {
	writer, err := sqlite.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("open migration state writer: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, writer.Close()) }()
	divergentSchemas := append([]migrationStateDivergentSchema{{
		table: fixture.divergentTable, column: fixture.divergentColumn,
	}}, fixture.additionalDivergence...)
	for _, divergent := range divergentSchemas {
		statement := fmt.Sprintf(
			"CREATE TABLE %s (%s integer NOT NULL)",
			quoteSQLiteIdentifier(divergent.table),
			quoteSQLiteIdentifier(divergent.column),
		)
		if _, err := writer.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create divergent migration state table %s: %w", divergent.table, err)
		}
	}
	for _, key := range fixture.applied {
		if err := writeMigrationStateRecord(ctx, writer, key); err != nil {
			return err
		}
	}
	return nil
}

func writeMigrationStateRecord(ctx context.Context, writer *sqlite.Backend, key migrations.MigrationKey) (resultErr error) {
	transaction, err := writer.BeginMigration(ctx)
	if err != nil {
		return fmt.Errorf("begin migration state recorder transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, transaction.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	if err := transaction.RecordApplied(ctx, key.App, key.Name); err != nil {
		return fmt.Errorf("record migration state identity %s.%s: %w", key.App, key.Name, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration state identity %s.%s: %w", key.App, key.Name, err)
	}
	return nil
}

type migrationStateObservedReader struct {
	delegate migrationbackend.AppliedMigrationReader
	calls    int
	records  []migrationbackend.AppliedMigration
}

func (reader *migrationStateObservedReader) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	reader.calls++
	records, err := reader.delegate.ReadAppliedMigrations(ctx)
	reader.records = append([]migrationbackend.AppliedMigration(nil), records...)
	return append([]migrationbackend.AppliedMigration(nil), records...), err
}

func migrationStateRequest(ctx context.Context, fixture migrationStateFixture, appliedReader *migrationStateObservedReader) (migrations.StateRequest, []migrations.MigrationKey, error) {
	if fixture.mode != migrationStateApplied && appliedReader != nil {
		return migrations.StateRequest{}, nil, errors.New("non-applied migration state request received an applied reader")
	}
	switch fixture.mode {
	case migrationStateExplicitEmpty:
		return migrations.EmptyStateRequest(), nil, nil
	case migrationStateBefore:
		if len(fixture.targets) == 0 {
			return migrations.StateRequest{}, nil, errors.New("migration state before fixture has no target")
		}
		return migrations.BeforeStateRequest(fixture.targets[0], fixture.targets[1:]...), nil, nil
	case migrationStateAfter:
		if len(fixture.targets) == 0 {
			return migrations.StateRequest{}, nil, errors.New("migration state after fixture has no target")
		}
		return migrations.AfterStateRequest(fixture.targets[0], fixture.targets[1:]...), nil, nil
	case migrationStateLatest:
		return migrations.LatestStateRequest(), nil, nil
	case migrationStateApplied:
		if appliedReader == nil {
			return migrations.StateRequest{}, nil, errors.New("applied migration state request has no reader")
		}
		appliedState, err := migrations.LoadAppliedState(ctx, appliedReader)
		if err != nil {
			return migrations.StateRequest{}, nil, fmt.Errorf("load migration state applied history: %w", err)
		}
		if appliedReader.calls != 1 {
			return migrations.StateRequest{}, nil, fmt.Errorf("migration state applied reader calls = %d, want 1", appliedReader.calls)
		}
		keys := make([]migrations.MigrationKey, 0, len(appliedReader.records))
		for _, record := range appliedReader.records {
			keys = append(keys, migrations.MigrationKey{App: record.App, Name: record.Name})
		}
		return migrations.AppliedStateRequest(appliedState), keys, nil
	default:
		return migrations.StateRequest{}, nil, fmt.Errorf("unsupported migration state request mode %d", fixture.mode)
	}
}

func migrationStateDefinitions() []migrations.Migration {
	return []migrations.Migration{
		{
			App: migrationStateAlphaRoot.App, Name: migrationStateAlphaRoot.Name,
			Operations: []migrations.Operation{
				migrations.CreateModel{AppLabel: "alpha", Model: ir.Model{
					Name: "zulu", GoName: "Zulu", DBTable: "godj_state_alpha_zulu",
					Fields: []ir.Field{
						migrationStateAutoField(),
						{Name: "active", GoName: "Active", Column: "active", Kind: ir.FieldBoolean, Default: migrationStateBooleanDefault(true)},
					},
				}},
				migrations.CreateModel{AppLabel: "alpha", Model: ir.Model{
					Name: "entry", GoName: "Entry", DBTable: "godj_state_alpha_entry",
					Fields: []ir.Field{
						migrationStateAutoField(),
						{Name: "headline", GoName: "Headline", Column: "headline_text", Kind: ir.FieldChar, MaxLength: 64, Default: migrationStateStringDefault("")},
					},
				}},
			},
		},
		{
			App: migrationStateAlphaMiddle.App, Name: migrationStateAlphaMiddle.Name,
			Dependencies: []migrations.MigrationKey{migrationStateAlphaRoot},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: "alpha", ModelName: "entry",
				Field: ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean, Default: migrationStateBooleanDefault(false)},
			}},
		},
		{
			App: migrationStateAlphaLeaf.App, Name: migrationStateAlphaLeaf.Name,
			Dependencies: []migrations.MigrationKey{migrationStateAlphaMiddle},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: "alpha", ModelName: "entry",
				Field: ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 255},
			}},
		},
		{
			App: migrationStateBetaRoot.App, Name: migrationStateBetaRoot.Name,
			Dependencies: []migrations.MigrationKey{migrationStateAlphaRoot},
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "beta", Model: ir.Model{
				Name: "audit", GoName: "Audit", DBTable: "godj_state_beta_audit",
				Fields: []ir.Field{
					migrationStateAutoField(),
					{Name: "code", GoName: "Code", Column: "code", Kind: ir.FieldChar, Nullable: true, MaxLength: 32},
				},
			}}},
		},
		{
			App: migrationStateGammaRoot.App, Name: migrationStateGammaRoot.Name,
			Dependencies: []migrations.MigrationKey{migrationStateAlphaRoot},
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "gamma", Model: ir.Model{
				Name: "flag", GoName: "Flag", DBTable: "godj_state_gamma_flag",
				Fields: []ir.Field{
					migrationStateAutoField(),
					{Name: "enabled", GoName: "Enabled", Column: "enabled", Kind: ir.FieldBoolean, Default: migrationStateBooleanDefault(true)},
				},
			}}},
		},
		{
			App: migrationStateDeltaRoot.App, Name: migrationStateDeltaRoot.Name,
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "delta", Model: ir.Model{
				Name: "archive", GoName: "Archive", DBTable: "godj_state_delta_archive",
				Fields: []ir.Field{
					migrationStateAutoField(),
					{Name: "label", GoName: "Label", Column: "label", Kind: ir.FieldChar, MaxLength: 48, Default: migrationStateStringDefault("archive")},
				},
			}}},
		},
	}
}

func migrationStateAutoField() ir.Field {
	return ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
}

func migrationStateStringDefault(value string) *ir.ScalarDefault {
	return &ir.ScalarDefault{Kind: ir.ScalarString, String: value}
}

func migrationStateBooleanDefault(value bool) *ir.ScalarDefault {
	return &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: value}
}

func migrationStateProjectValue(state migrations.ProjectState) (protocol.Value, error) {
	apps := make([]protocol.Value, 0, len(state.Apps()))
	for _, appLabel := range state.Apps() {
		schema, exists := state.Schema(appLabel)
		if !exists {
			return protocol.Value{}, fmt.Errorf("state app %q disappeared during normalization", appLabel)
		}
		if schema.AppLabel != appLabel {
			return protocol.Value{}, fmt.Errorf("state app key %q does not match schema label %q", appLabel, schema.AppLabel)
		}
		models := append([]ir.Model(nil), schema.Models...)
		sort.Slice(models, func(left, right int) bool { return models[left].Name < models[right].Name })
		modelValues := make([]protocol.Value, 0, len(models))
		for _, model := range models {
			if model.Name == "" || strings.ToLower(model.Name) != model.Name {
				return protocol.Value{}, fmt.Errorf("state model name %q is not canonical lowercase", model.Name)
			}
			fieldValues := make([]protocol.Value, 0, len(model.Fields))
			for _, field := range model.Fields {
				fieldValue, err := migrationStateFieldValue(field)
				if err != nil {
					return protocol.Value{}, fmt.Errorf("state field %s.%s.%s: %w", appLabel, model.Name, field.Name, err)
				}
				fieldValues = append(fieldValues, fieldValue)
			}
			modelValues = append(modelValues, protocol.Object(map[string]protocol.Value{
				"db_table": protocol.String(model.DBTable),
				"fields":   protocol.List(fieldValues...),
				"name":     protocol.String(model.Name),
			}))
		}
		apps = append(apps, protocol.Object(map[string]protocol.Value{
			"label":  protocol.String(appLabel),
			"models": protocol.List(modelValues...),
		}))
	}
	return protocol.Object(map[string]protocol.Value{
		"apps":           protocol.List(apps...),
		"format_version": protocol.Integer(fmt.Sprint(state.FormatVersion())),
	}), nil
}

func migrationStateFieldValue(field ir.Field) (protocol.Value, error) {
	maxLength := protocol.Null()
	switch field.Kind {
	case ir.FieldChar:
		if field.MaxLength <= 0 {
			return protocol.Value{}, errors.New("char max_length must be positive")
		}
		maxLength = protocol.Integer(fmt.Sprint(field.MaxLength))
	case ir.FieldAuto, ir.FieldBoolean:
		if field.MaxLength != 0 {
			return protocol.Value{}, errors.New("non-char max_length must be zero")
		}
	default:
		return protocol.Value{}, fmt.Errorf("unsupported field kind %q", field.Kind)
	}
	defaultValue, err := migrationStateDefaultValue(field.Default)
	if err != nil {
		return protocol.Value{}, err
	}
	return protocol.Object(map[string]protocol.Value{
		"column":      protocol.String(field.Column),
		"default":     defaultValue,
		"kind":        protocol.String(string(field.Kind)),
		"max_length":  maxLength,
		"name":        protocol.String(field.Name),
		"nullable":    protocol.Boolean(field.Nullable),
		"primary_key": protocol.Boolean(field.PrimaryKey),
	}), nil
}

func migrationStateDefaultValue(value *ir.ScalarDefault) (protocol.Value, error) {
	if value == nil {
		return protocol.Object(map[string]protocol.Value{
			"present": protocol.Boolean(false),
			"type":    protocol.String("absent"),
			"value":   protocol.Null(),
		}), nil
	}
	var defaultType string
	var normalized protocol.Value
	switch value.Kind {
	case ir.ScalarString:
		defaultType = "string"
		normalized = protocol.String(value.String)
	case ir.ScalarBoolean:
		defaultType = "bool"
		normalized = protocol.Boolean(value.Boolean)
	default:
		return protocol.Value{}, fmt.Errorf("unsupported scalar default kind %q", value.Kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"present": protocol.Boolean(true),
		"type":    protocol.String(defaultType),
		"value":   normalized,
	}), nil
}

func migrationStateAppliedResult(state protocol.Value, applied []migrations.MigrationKey, definitions []migrations.Migration) protocol.Value {
	knownSet := make(map[migrations.MigrationKey]struct{}, len(definitions))
	for _, definition := range definitions {
		knownSet[definition.Key()] = struct{}{}
	}
	known := make([]migrations.MigrationKey, 0, len(applied))
	unknown := make([]migrations.MigrationKey, 0, len(applied))
	for _, key := range applied {
		if _, exists := knownSet[key]; exists {
			known = append(known, key)
		} else {
			unknown = append(unknown, key)
		}
	}
	return protocol.Object(map[string]protocol.Value{
		"applied_migrations":         protocol.List(planningKeyValuesSorted(applied)...),
		"known_applied_migrations":   protocol.List(planningKeyValuesSorted(known)...),
		"state":                      state,
		"unknown_applied_migrations": protocol.List(planningKeyValuesSorted(unknown)...),
	})
}

func migrationStateDatabaseSnapshot(ctx context.Context, observer *sql.DB) (protocol.Value, error) {
	recorderPresent, err := sqliteTableExistsContext(ctx, observer, goDjMigrationRecordTable)
	if err != nil {
		return protocol.Value{}, err
	}
	applied, err := readMigrationRecords(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	managedSchema, err := migrationStateManagedSchema(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	return protocol.Object(map[string]protocol.Value{
		"applied_migrations": applied,
		"managed_schema":     managedSchema,
		"recorder_present":   protocol.Boolean(recorderPresent),
	}), nil
}

func migrationStateManagedSchema(ctx context.Context, observer *sql.DB) (protocol.Value, error) {
	rows, err := observer.QueryContext(ctx, `SELECT "name" FROM "sqlite_master" WHERE "type" = 'table' AND "name" GLOB 'godj_state_*' ORDER BY "name"`)
	if err != nil {
		return protocol.Value{}, fmt.Errorf("list managed migration state tables: %w", err)
	}
	tables := make([]string, 0)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return protocol.Value{}, fmt.Errorf("scan managed migration state table: %w", err)
		}
		tables = append(tables, table)
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil {
		iterateErr = fmt.Errorf("iterate divergent migration state columns: %w", iterateErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close divergent migration state columns: %w", closeErr)
	}
	if iterateErr != nil || closeErr != nil {
		return protocol.Value{}, errors.Join(iterateErr, closeErr)
	}

	values := make([]protocol.Value, 0, len(tables))
	for _, table := range tables {
		columns, err := migrationStateDatabaseColumns(ctx, observer, table)
		if err != nil {
			return protocol.Value{}, err
		}
		values = append(values, protocol.Object(map[string]protocol.Value{
			"columns": columns,
			"name":    protocol.String(table),
		}))
	}
	return protocol.List(values...), nil
}

func migrationStateDatabaseColumns(ctx context.Context, observer *sql.DB, table string) (protocol.Value, error) {
	rows, err := observer.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
	if err != nil {
		return protocol.Value{}, fmt.Errorf("describe divergent migration state table: %w", err)
	}
	values := make([]protocol.Value, 0)
	for rows.Next() {
		var (
			sequence     int
			name         string
			declaredType string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(&sequence, &name, &declaredType, &notNull, &defaultValue, &primaryKey); err != nil {
			return protocol.Value{}, errors.Join(fmt.Errorf("scan divergent migration state column: %w", err), rows.Close())
		}
		typeFamily, err := sqliteTypeFamily(declaredType)
		if err != nil {
			return protocol.Value{}, errors.Join(err, rows.Close())
		}
		values = append(values, protocol.Object(map[string]protocol.Value{
			"name":        protocol.String(name),
			"nullable":    protocol.Boolean(notNull == 0 && primaryKey == 0),
			"type_family": protocol.String(typeFamily),
		}))
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil || closeErr != nil {
		return protocol.Value{}, errors.Join(iterateErr, closeErr)
	}
	return protocol.List(values...), nil
}

func migrationStateMetrics(fixture migrationStateFixture, before, after protocol.Value) protocol.Value {
	nodes, dependencies := migrationRestartGraphFacts(fixture.definitions)
	return protocol.Object(map[string]protocol.Value{
		"capture_boundary":           protocol.String(migrationStateCaptureBoundary(fixture.mode)),
		"ddl_statement_count":        protocol.Integer("0"),
		"graph":                      planningGraphValue(nodes, dependencies),
		"non_select_statement_count": protocol.Integer("0"),
		"replay_source":              protocol.String("loaded_migration_definitions"),
		"request":                    migrationStateRequestValue(fixture),
		"state_unchanged":            protocol.Boolean(reflect.DeepEqual(before, after)),
		"write_statement_count":      protocol.Integer("0"),
	})
}

func migrationStateCaptureBoundary(mode migrationStateRequestMode) string {
	if mode == migrationStateApplied {
		return "fresh_executor"
	}
	return "fresh_loader"
}

func migrationStateRequestValue(fixture migrationStateFixture) protocol.Value {
	mode := "explicit_nodes"
	position := "after"
	if fixture.mode == migrationStateBefore {
		position = "before"
	}
	if fixture.mode == migrationStateLatest {
		mode = "latest"
	}
	if fixture.mode == migrationStateApplied {
		mode = "applied_history"
	}
	values := make([]protocol.Value, 0, len(fixture.targets))
	for _, target := range fixture.targets {
		values = append(values, planningKeyValue(target))
	}
	return protocol.Object(map[string]protocol.Value{
		"mode":     protocol.String(mode),
		"position": protocol.String(position),
		"targets":  protocol.List(values...),
	})
}

func migrationStateAssertDivergent(state migrations.ProjectState, fixture migrationStateFixture) error {
	liveTables := map[string]struct{}{fixture.divergentTable: {}}
	for _, divergent := range fixture.additionalDivergence {
		liveTables[divergent.table] = struct{}{}
	}
	for _, app := range state.Apps() {
		schema, _ := state.Schema(app)
		for _, model := range schema.Models {
			if _, exists := liveTables[model.DBTable]; exists {
				return fmt.Errorf("live divergent table %q unexpectedly matches reconstructed state", model.DBTable)
			}
		}
	}
	return nil
}
