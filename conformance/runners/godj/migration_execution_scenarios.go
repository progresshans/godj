package godj

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const (
	executionAlphaTable = "godj_exec_alpha"
	executionBetaTable  = "godj_exec_beta"
	executionProbeTable = "external_execution_probe"
)

var (
	executionA1 = migrations.MigrationKey{App: "alpha", Name: "0001_initial"}
	executionA2 = migrations.MigrationKey{App: "alpha", Name: "0002_second"}
	executionA3 = migrations.MigrationKey{App: "alpha", Name: "0003_third"}
	executionB1 = migrations.MigrationKey{App: "beta", Name: "0001_initial"}
)

type migrationExecutionFault struct {
	key       migrations.MigrationKey
	direction migrations.Direction
}

type migrationExecutionFixture struct {
	definitions                  []migrations.Migration
	seed                         []migrations.MigrationKey
	plan                         []migrations.PlanStep
	recorderPresent              bool
	operationFault               *migrationExecutionFault
	recorderFault                *migrationExecutionFault
	includeHistoricalTransitions bool
}

var migrationExecutionFixtures = map[string]func() migrationExecutionFixture{
	"django.migration.execute.linear_forward": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions:     migrationExecutionDefinitions(false),
			plan:            migrationExecutionForwardPlan(executionA1, executionA2, executionA3),
			recorderPresent: true,
		}
	},
	"django.migration.execute.linear_backward": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions:     migrationExecutionDefinitions(false),
			seed:            []migrations.MigrationKey{executionA1, executionA2, executionA3},
			plan:            migrationExecutionBackwardPlan(executionA3, executionA2, executionA1),
			recorderPresent: true,
		}
	},
	"django.migration.execute.applied_prefix_tail": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions:                  migrationExecutionDefinitions(false),
			seed:                         []migrations.MigrationKey{executionA1},
			plan:                         migrationExecutionForwardPlan(executionA2, executionA3),
			recorderPresent:              true,
			includeHistoricalTransitions: true,
		}
	},
	"django.migration.execute.rollback_branch_preserves_unrelated": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions: migrationExecutionDefinitions(true),
			seed: []migrations.MigrationKey{
				executionA1,
				executionA2,
				executionA3,
				executionB1,
			},
			plan:            migrationExecutionBackwardPlan(executionA3, executionA2),
			recorderPresent: true,
		}
	},
	"django.migration.execute.forward_operation_failure": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions:     migrationExecutionDefinitions(false),
			plan:            migrationExecutionForwardPlan(executionA1, executionA2, executionA3),
			recorderPresent: true,
			operationFault: &migrationExecutionFault{
				key:       executionA2,
				direction: migrations.DirectionForward,
			},
		}
	},
	"django.migration.execute.backward_operation_failure": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions:     migrationExecutionDefinitions(false),
			seed:            []migrations.MigrationKey{executionA1, executionA2, executionA3},
			plan:            migrationExecutionBackwardPlan(executionA3, executionA2, executionA1),
			recorderPresent: true,
			operationFault: &migrationExecutionFault{
				key:       executionA2,
				direction: migrations.DirectionBackward,
			},
		}
	},
	"django.migration.execute.forward_recorder_failure": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions:     migrationExecutionDefinitions(false),
			plan:            migrationExecutionForwardPlan(executionA1, executionA2, executionA3),
			recorderPresent: true,
			recorderFault: &migrationExecutionFault{
				key:       executionA2,
				direction: migrations.DirectionForward,
			},
		}
	},
	"django.migration.execute.backward_recorder_failure": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions:     migrationExecutionDefinitions(false),
			seed:            []migrations.MigrationKey{executionA1, executionA2, executionA3},
			plan:            migrationExecutionBackwardPlan(executionA3, executionA2, executionA1),
			recorderPresent: true,
			recorderFault: &migrationExecutionFault{
				key:       executionA2,
				direction: migrations.DirectionBackward,
			},
		}
	},
	"django.migration.execute.mixed_direction_rejected": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions: migrationExecutionDefinitions(false),
			plan: []migrations.PlanStep{
				{Key: executionA1, Direction: migrations.DirectionForward},
				{Key: executionA2, Direction: migrations.DirectionBackward},
			},
			recorderPresent: true,
		}
	},
	"django.migration.execute.empty_plan": func() migrationExecutionFixture {
		return migrationExecutionFixture{
			definitions: migrationExecutionDefinitions(false),
			plan:        []migrations.PlanStep{},
		}
	},
}

func migrationExecutionScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixtureFactory, ok := migrationExecutionFixtures[contract.Scenario]
	if !ok {
		return protocol.Observation{}, fmt.Errorf("unsupported migration execution scenario %q", contract.Scenario)
	}
	return runMigrationExecutionFixture(ctx, contract.ID, fixtureFactory())
}

func runMigrationExecutionFixture(ctx context.Context, contractID string, fixture migrationExecutionFixture) (protocol.Observation, error) {
	return withMigrationDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend, observer *sql.DB) (protocol.Observation, error) {
		if err := provisionMigrationExecutionDatabase(ctx, backend, fixture.recorderPresent); err != nil {
			return protocol.Observation{}, err
		}

		beforeState := migrations.EmptyProjectState()
		if len(fixture.seed) > 0 {
			seedPlan := migrationExecutionForwardPlan(fixture.seed...)
			var err error
			beforeState, err = (migrations.DirectExecutor{Backend: backend}).ExecutePlan(
				ctx,
				beforeState,
				fixture.definitions,
				seedPlan,
			)
			if err != nil {
				return protocol.Observation{}, fmt.Errorf("seed migration execution fixture: %w", err)
			}
		}

		beforeDatabase, err := migrationExecutionDatabaseSnapshot(ctx, observer)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("snapshot migration execution database before plan: %w", err)
		}

		trace := newMigrationExecutionTrace(
			backend,
			fixture.definitions,
			fixture.operationFault,
			fixture.recorderFault,
		)
		returnedState, executionErr := (migrations.DirectExecutor{Backend: trace}).ExecutePlan(
			ctx,
			beforeState,
			fixture.definitions,
			fixture.plan,
		)
		if err := trace.validate(fixture.plan, executionErr); err != nil {
			return protocol.Observation{}, fmt.Errorf("capture migration execution trace: %w", err)
		}

		afterDatabase, err := migrationExecutionDatabaseSnapshot(ctx, observer)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("snapshot migration execution database after plan: %w", err)
		}
		connection, err := migrationExecutionConnectionValue(ctx, backend, observer)
		if err != nil {
			return protocol.Observation{}, err
		}
		steps, err := migrationExecutionStepValues(
			ctx,
			trace,
			beforeState,
			fixture.definitions,
			fixture.plan,
			fixture.includeHistoricalTransitions,
		)
		if err != nil {
			return protocol.Observation{}, err
		}

		observation := protocol.Observation{
			ID:     contractID,
			Status: protocol.StatusObserved,
			DBState: valuePointer(protocol.Object(map[string]protocol.Value{
				"after":  afterDatabase,
				"before": beforeDatabase,
			})),
			Metrics: valuePointer(protocol.Object(map[string]protocol.Value{
				"connection": connection,
				"steps":      protocol.List(steps...),
			})),
		}
		if executionErr == nil {
			observation.Phase = protocol.PhaseCommit
			observation.Result = valuePointer(protocol.Object(map[string]protocol.Value{
				"plan":           protocol.List(migrationExecutionPlanValues(fixture.plan)...),
				"returned_state": migrationExecutionStateValue(returnedState),
			}))
			return observation, nil
		}

		migrationErr := new(migrations.Error)
		if !errors.As(executionErr, &migrationErr) {
			return protocol.Observation{}, fmt.Errorf("migration execution returned unstructured error %T: %w", executionErr, executionErr)
		}
		observation.Phase = protocol.PhaseRollback
		if len(trace.transactions) == 0 {
			observation.Phase = protocol.PhaseEvaluation
		}
		observation.Error = &protocol.ObservedError{
			Category:          string(migrationErr.Category),
			Code:              string(migrationErr.Code),
			Message:           executionErr.Error(),
			MessageIsContract: boolPointer(false),
		}
		return observation, nil
	})
}

func migrationExecutionDefinitions(includeBranch bool) []migrations.Migration {
	definitions := []migrations.Migration{
		{
			App:  executionA1.App,
			Name: executionA1.Name,
			Operations: []migrations.Operation{migrations.CreateModel{
				AppLabel: executionA1.App,
				Model: ir.Model{
					Name:    "entry",
					GoName:  "Entry",
					DBTable: executionAlphaTable,
					Fields: []ir.Field{
						migrationExecutionAutoField(),
						{Name: "a1_marker", GoName: "A1Marker", Column: "a1_marker", Kind: ir.FieldChar, MaxLength: 16},
					},
				},
			}},
		},
		{
			App:          executionA2.App,
			Name:         executionA2.Name,
			Dependencies: []migrations.MigrationKey{executionA1},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel:  executionA2.App,
				ModelName: "entry",
				Field: ir.Field{
					Name:   "a2_marker",
					GoName: "A2Marker",
					Column: "a2_marker",
					Kind:   ir.FieldBoolean,
				},
			}},
		},
		{
			App:          executionA3.App,
			Name:         executionA3.Name,
			Dependencies: []migrations.MigrationKey{executionA2},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel:  executionA3.App,
				ModelName: "entry",
				Field: ir.Field{
					Name:      "a3_marker",
					GoName:    "A3Marker",
					Column:    "a3_marker",
					Kind:      ir.FieldChar,
					Nullable:  true,
					MaxLength: 16,
				},
			}},
		},
	}
	if includeBranch {
		definitions = append(definitions, migrations.Migration{
			App:          executionB1.App,
			Name:         executionB1.Name,
			Dependencies: []migrations.MigrationKey{executionA1},
			Operations: []migrations.Operation{migrations.CreateModel{
				AppLabel: executionB1.App,
				Model: ir.Model{
					Name:    "branch",
					GoName:  "Branch",
					DBTable: executionBetaTable,
					Fields: []ir.Field{
						migrationExecutionAutoField(),
						{Name: "b1_marker", GoName: "B1Marker", Column: "b1_marker", Kind: ir.FieldChar, MaxLength: 16},
					},
				},
			}},
		})
	}
	return definitions
}

func migrationExecutionAutoField() ir.Field {
	return ir.Field{
		Name:       "id",
		GoName:     "ID",
		Column:     "id",
		Kind:       ir.FieldAuto,
		PrimaryKey: true,
	}
}

func migrationExecutionForwardPlan(keys ...migrations.MigrationKey) []migrations.PlanStep {
	plan := make([]migrations.PlanStep, 0, len(keys))
	for _, key := range keys {
		plan = append(plan, migrations.PlanStep{Key: key, Direction: migrations.DirectionForward})
	}
	return plan
}

func migrationExecutionBackwardPlan(keys ...migrations.MigrationKey) []migrations.PlanStep {
	plan := make([]migrations.PlanStep, 0, len(keys))
	for _, key := range keys {
		plan = append(plan, migrations.PlanStep{Key: key, Direction: migrations.DirectionBackward})
	}
	return plan
}

func provisionMigrationExecutionDatabase(ctx context.Context, backend *sqlite.Backend, recorderPresent bool) error {
	statements := []string{
		`CREATE TABLE "external_execution_probe" ("value" INTEGER NOT NULL)`,
		`INSERT INTO "external_execution_probe" ("value") VALUES (1)`,
	}
	if recorderPresent {
		statements = append(statements, `CREATE TABLE "godj_migrations" (`+
			`"app" VARCHAR(255) NOT NULL, `+
			`"name" VARCHAR(255) NOT NULL, `+
			`PRIMARY KEY ("app", "name"))`)
	}
	for _, statement := range statements {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision migration execution database: %w", err)
		}
	}
	return nil
}

func migrationExecutionDatabaseSnapshot(ctx context.Context, observer *sql.DB) (protocol.Value, error) {
	tables, err := observer.QueryContext(
		ctx,
		`SELECT "name" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" LIKE 'godj_exec_%' ORDER BY "name"`,
	)
	if err != nil {
		return protocol.Value{}, fmt.Errorf("list migration execution schema: %w", err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			_ = tables.Close()
			return protocol.Value{}, fmt.Errorf("scan migration execution table: %w", err)
		}
		names = append(names, name)
	}
	iterateErr := tables.Err()
	closeErr := tables.Close()
	if iterateErr != nil || closeErr != nil {
		return protocol.Value{}, errors.Join(iterateErr, closeErr)
	}

	managedSchema := make([]protocol.Value, 0, len(names))
	for _, name := range names {
		columns, err := migrationExecutionColumns(ctx, observer, name)
		if err != nil {
			return protocol.Value{}, err
		}
		managedSchema = append(managedSchema, protocol.Object(map[string]protocol.Value{
			"columns": protocol.List(columns...),
			"name":    protocol.String(name),
		}))
	}
	recorderPresent, err := sqliteTableExistsContext(ctx, observer, goDjMigrationRecordTable)
	if err != nil {
		return protocol.Value{}, err
	}
	records, err := readMigrationRecords(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	return protocol.Object(map[string]protocol.Value{
		"managed_schema":    protocol.List(managedSchema...),
		"migration_records": records,
		"recorder_present":  protocol.Boolean(recorderPresent),
	}), nil
}

func migrationExecutionColumns(ctx context.Context, observer *sql.DB, table string) ([]protocol.Value, error) {
	rows, err := observer.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
	if err != nil {
		return nil, fmt.Errorf("describe migration execution table %s: %w", table, err)
	}
	type column struct {
		name       string
		typeFamily string
		nullable   bool
		primaryKey bool
	}
	var columns []column
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
			_ = rows.Close()
			return nil, fmt.Errorf("scan migration execution column: %w", err)
		}
		typeFamily, err := sqliteTypeFamily(declaredType)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("migration execution column %s.%s: %w", table, name, err)
		}
		columns = append(columns, column{
			name:       name,
			typeFamily: typeFamily,
			nullable:   notNull == 0 && primaryKey == 0,
			primaryKey: primaryKey != 0,
		})
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil || closeErr != nil {
		return nil, errors.Join(iterateErr, closeErr)
	}
	sort.Slice(columns, func(left, right int) bool { return columns[left].name < columns[right].name })
	values := make([]protocol.Value, 0, len(columns))
	for _, column := range columns {
		values = append(values, protocol.Object(map[string]protocol.Value{
			"name":        protocol.String(column.name),
			"nullable":    protocol.Boolean(column.nullable),
			"primary_key": protocol.Boolean(column.primaryKey),
			"type_family": protocol.String(column.typeFamily),
		}))
	}
	return values, nil
}

func migrationExecutionConnectionValue(ctx context.Context, backend *sqlite.Backend, observer *sql.DB) (protocol.Value, error) {
	field := query.NewFieldRef("value", "value", query.FieldInteger, false)
	rows, err := backend.Query(ctx, query.NewPlan(executionProbeTable, []query.FieldRef{field}))
	if err != nil {
		return protocol.Value{}, fmt.Errorf("probe migration execution SELECT: %w", err)
	}
	selectUsable := false
	if rows.Next() {
		var value int
		if err := rows.Scan(&value); err != nil {
			_ = rows.Close()
			return protocol.Value{}, fmt.Errorf("scan migration execution SELECT probe: %w", err)
		}
		selectUsable = value == 1 && !rows.Next()
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil || closeErr != nil {
		return protocol.Value{}, errors.Join(iterateErr, closeErr)
	}

	if _, err := backend.ExecContext(ctx, `UPDATE "external_execution_probe" SET "value" = 2`); err != nil {
		return protocol.Value{}, fmt.Errorf("probe migration execution autocommit: %w", err)
	}
	var visible int
	if err := observer.QueryRowContext(ctx, `SELECT "value" FROM "external_execution_probe"`).Scan(&visible); err != nil {
		return protocol.Value{}, fmt.Errorf("observe migration execution autocommit: %w", err)
	}
	autocommitRestored := visible == 2
	if _, err := backend.ExecContext(ctx, `UPDATE "external_execution_probe" SET "value" = 1`); err != nil {
		return protocol.Value{}, fmt.Errorf("restore migration execution autocommit probe: %w", err)
	}

	outsideAtomicBlock := true
	if err := backend.Atomic(ctx, func(db.Session) error { return nil }); err != nil {
		outsideAtomicBlock = false
	}
	return protocol.Object(map[string]protocol.Value{
		"autocommit_restored":  protocol.Boolean(autocommitRestored),
		"outside_atomic_block": protocol.Boolean(outsideAtomicBlock),
		"select_usable":        protocol.Boolean(selectUsable),
	}), nil
}

func migrationExecutionStateValue(state migrations.ProjectState) protocol.Value {
	models := make([]protocol.Value, 0)
	for _, app := range state.Apps() {
		schema, exists := state.Schema(app)
		if !exists {
			continue
		}
		sort.Slice(schema.Models, func(left, right int) bool { return schema.Models[left].Name < schema.Models[right].Name })
		for _, model := range schema.Models {
			fields := make([]string, 0, len(model.Fields))
			for _, field := range model.Fields {
				fields = append(fields, field.Name)
			}
			sort.Strings(fields)
			fieldValues := make([]protocol.Value, 0, len(fields))
			for _, field := range fields {
				fieldValues = append(fieldValues, protocol.String(field))
			}
			models = append(models, protocol.Object(map[string]protocol.Value{
				"app":    protocol.String(app),
				"fields": protocol.List(fieldValues...),
				"name":   protocol.String(model.Name),
			}))
		}
	}
	return protocol.Object(map[string]protocol.Value{
		"models": protocol.List(models...),
	})
}

func migrationExecutionPlanValues(plan []migrations.PlanStep) []protocol.Value {
	values := make([]protocol.Value, 0, len(plan))
	for _, step := range plan {
		values = append(values, protocol.Object(map[string]protocol.Value{
			"app":       protocol.String(step.Key.App),
			"direction": protocol.String(string(step.Direction)),
			"name":      protocol.String(step.Key.Name),
		}))
	}
	return values
}

type migrationExecutionTrace struct {
	backend        *sqlite.Backend
	operationFault *migrationExecutionFault
	recorderFault  *migrationExecutionFault
	modelKeys      map[string]migrations.MigrationKey
	fieldKeys      map[string]migrations.MigrationKey
	transactions   []*migrationExecutionTransaction
	err            error
}

func newMigrationExecutionTrace(
	backend *sqlite.Backend,
	definitions []migrations.Migration,
	operationFault *migrationExecutionFault,
	recorderFault *migrationExecutionFault,
) *migrationExecutionTrace {
	trace := &migrationExecutionTrace{
		backend:        backend,
		operationFault: operationFault,
		recorderFault:  recorderFault,
		modelKeys:      make(map[string]migrations.MigrationKey),
		fieldKeys:      make(map[string]migrations.MigrationKey),
	}
	for _, migration := range definitions {
		for _, operation := range migration.Operations {
			switch operation := operation.(type) {
			case migrations.CreateModel:
				trace.registerModelKey(operation.Model.DBTable, migration.Key())
			case *migrations.CreateModel:
				if operation != nil {
					trace.registerModelKey(operation.Model.DBTable, migration.Key())
				}
			case migrations.AddField:
				trace.registerFieldKey(operation.ModelName, operation.Field.Name, migration.Key())
			case *migrations.AddField:
				if operation != nil {
					trace.registerFieldKey(operation.ModelName, operation.Field.Name, migration.Key())
				}
			}
		}
	}
	return trace
}

func (trace *migrationExecutionTrace) registerModelKey(table string, key migrations.MigrationKey) {
	if previous, exists := trace.modelKeys[table]; exists && previous != key {
		trace.err = errors.Join(trace.err, fmt.Errorf("migration execution fixture maps table %q to both %v and %v", table, previous, key))
		return
	}
	trace.modelKeys[table] = key
}

func (trace *migrationExecutionTrace) registerFieldKey(model, field string, key migrations.MigrationKey) {
	identity := model + "\x00" + field
	if previous, exists := trace.fieldKeys[identity]; exists && previous != key {
		trace.err = errors.Join(trace.err, fmt.Errorf("migration execution fixture maps field %s.%s to both %v and %v", model, field, previous, key))
		return
	}
	trace.fieldKeys[identity] = key
}

func (trace *migrationExecutionTrace) BeginMigration(ctx context.Context) (migrationbackend.Transaction, error) {
	transaction, err := trace.backend.BeginMigration(ctx)
	if err != nil {
		return nil, err
	}
	observed := &migrationExecutionTransaction{delegate: transaction, owner: trace}
	trace.transactions = append(trace.transactions, observed)
	return observed, nil
}

func (trace *migrationExecutionTrace) validate(plan []migrations.PlanStep, executionErr error) error {
	type stepIdentity struct {
		key       migrations.MigrationKey
		direction migrations.Direction
	}
	planned := make(map[stepIdentity]struct{}, len(plan))
	for _, step := range plan {
		planned[stepIdentity{key: step.Key, direction: step.Direction}] = struct{}{}
	}
	seen := make(map[stepIdentity]struct{}, len(trace.transactions))
	validationErr := trace.err
	if executionErr == nil && len(trace.transactions) != len(plan) {
		validationErr = errors.Join(validationErr, fmt.Errorf(
			"successful migration execution opened %d transactions for %d plan steps",
			len(trace.transactions),
			len(plan),
		))
	}
	stopped := false
	for index, transaction := range trace.transactions {
		identity := stepIdentity{key: transaction.key, direction: transaction.direction}
		if stopped {
			validationErr = errors.Join(validationErr, fmt.Errorf("transaction[%d] started after a rolled-back migration step", index))
		}
		if transaction.key == (migrations.MigrationKey{}) || transaction.direction == "" {
			validationErr = errors.Join(validationErr, fmt.Errorf("transaction[%d] began without binding to a migration step", index))
			continue
		}
		if _, exists := planned[identity]; !exists {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"transaction[%d] bound to unplanned migration step %s.%s/%s",
				index,
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
			))
		}
		if index >= len(plan) || plan[index].Key != transaction.key || plan[index].Direction != transaction.direction {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"transaction[%d] executed %s.%s/%s outside plan order",
				index,
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
			))
		}
		if _, exists := seen[identity]; exists {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"migration execution step %s.%s/%s started more than once",
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
			))
		}
		seen[identity] = struct{}{}
		if !transaction.operationStarted {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"migration execution step %s.%s/%s opened a transaction without an operation",
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
			))
		}
		if transaction.committed == transaction.rolledBack {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"migration execution step %s.%s/%s terminal state commit=%t rollback=%t",
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
				transaction.committed,
				transaction.rolledBack,
			))
		}
		if transaction.committed && (!transaction.recorderSucceeded || transaction.operationFailed || transaction.recorderFailed) {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"migration execution step %s.%s/%s committed without successful operation and recorder boundaries",
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
			))
		}
		if transaction.rolledBack && !transaction.operationFailed && !transaction.recorderFailed {
			validationErr = errors.Join(validationErr, fmt.Errorf(
				"migration execution step %s.%s/%s rolled back without a captured operation or recorder failure",
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
			))
		}
		stopped = transaction.rolledBack
	}
	return validationErr
}

type migrationExecutionTransaction struct {
	delegate          migrationbackend.Transaction
	owner             *migrationExecutionTrace
	key               migrations.MigrationKey
	direction         migrations.Direction
	operationStarted  bool
	operationFailed   bool
	recorderStarted   bool
	recorderSucceeded bool
	recorderFailed    bool
	committed         bool
	rolledBack        bool
}

func (transaction *migrationExecutionTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	key := transaction.owner.keyForModel(model)
	transaction.startOperation(key, migrations.DirectionForward)
	if transaction.shouldFailOperation() {
		transaction.operationFailed = true
		return errors.New("forced forward migration operation failure")
	}
	err := transaction.delegate.CreateModel(ctx, model)
	transaction.operationFailed = err != nil
	return err
}

func (transaction *migrationExecutionTransaction) DeleteModel(ctx context.Context, model ir.Model) error {
	key := transaction.owner.keyForModel(model)
	transaction.startOperation(key, migrations.DirectionBackward)
	if transaction.shouldFailOperation() {
		transaction.operationFailed = true
		return errors.New("forced backward migration operation failure")
	}
	err := transaction.delegate.DeleteModel(ctx, model)
	transaction.operationFailed = err != nil
	return err
}

func (transaction *migrationExecutionTransaction) AddField(ctx context.Context, model ir.Model, field ir.Field) error {
	key := transaction.owner.keyForField(model, field)
	transaction.startOperation(key, migrations.DirectionForward)
	if transaction.shouldFailOperation() {
		transaction.operationFailed = true
		return errors.New("forced forward migration operation failure")
	}
	err := transaction.delegate.AddField(ctx, model, field)
	transaction.operationFailed = err != nil
	return err
}

func (transaction *migrationExecutionTransaction) RemoveField(ctx context.Context, model ir.Model, field ir.Field) error {
	key := transaction.owner.keyForField(model, field)
	transaction.startOperation(key, migrations.DirectionBackward)
	if transaction.shouldFailOperation() {
		transaction.operationFailed = true
		return errors.New("forced backward migration operation failure")
	}
	err := transaction.delegate.RemoveField(ctx, model, field)
	transaction.operationFailed = err != nil
	return err
}

func (transaction *migrationExecutionTransaction) RecordApplied(ctx context.Context, app, name string) error {
	return transaction.record(ctx, migrations.MigrationKey{App: app, Name: name}, migrations.DirectionForward)
}

func (transaction *migrationExecutionTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	return transaction.record(ctx, migrations.MigrationKey{App: app, Name: name}, migrations.DirectionBackward)
}

func (transaction *migrationExecutionTransaction) record(ctx context.Context, key migrations.MigrationKey, direction migrations.Direction) error {
	transaction.bind(key, direction)
	transaction.recorderStarted = true
	if fault := transaction.owner.recorderFault; fault != nil && fault.key == key && fault.direction == direction {
		transaction.recorderFailed = true
		return errors.New("forced migration recorder failure before record write")
	}
	var err error
	if direction == migrations.DirectionForward {
		err = transaction.delegate.RecordApplied(ctx, key.App, key.Name)
	} else {
		err = transaction.delegate.RecordUnapplied(ctx, key.App, key.Name)
	}
	transaction.recorderFailed = err != nil
	transaction.recorderSucceeded = err == nil
	return err
}

func (transaction *migrationExecutionTransaction) Commit(ctx context.Context) error {
	err := transaction.delegate.Commit(ctx)
	transaction.committed = err == nil
	return err
}

func (transaction *migrationExecutionTransaction) Rollback(ctx context.Context) error {
	err := transaction.delegate.Rollback(ctx)
	transaction.rolledBack = err == nil
	return err
}

func (transaction *migrationExecutionTransaction) startOperation(key migrations.MigrationKey, direction migrations.Direction) {
	transaction.bind(key, direction)
	transaction.operationStarted = true
}

func (transaction *migrationExecutionTransaction) bind(key migrations.MigrationKey, direction migrations.Direction) {
	if transaction.key == (migrations.MigrationKey{}) {
		transaction.key = key
		transaction.direction = direction
		return
	}
	if transaction.key != key || transaction.direction != direction {
		transaction.owner.err = errors.Join(
			transaction.owner.err,
			fmt.Errorf(
				"one migration transaction crossed step boundary from %s.%s/%s to %s.%s/%s",
				transaction.key.App,
				transaction.key.Name,
				transaction.direction,
				key.App,
				key.Name,
				direction,
			),
		)
	}
}

func (transaction *migrationExecutionTransaction) shouldFailOperation() bool {
	fault := transaction.owner.operationFault
	return fault != nil && fault.key == transaction.key && fault.direction == transaction.direction
}

func (trace *migrationExecutionTrace) keyForModel(model ir.Model) migrations.MigrationKey {
	key, exists := trace.modelKeys[model.DBTable]
	if !exists {
		trace.err = errors.Join(trace.err, fmt.Errorf("migration execution operation used unregistered table %q", model.DBTable))
		return migrations.MigrationKey{App: "unknown", Name: model.DBTable}
	}
	return key
}

func (trace *migrationExecutionTrace) keyForField(model ir.Model, field ir.Field) migrations.MigrationKey {
	key, exists := trace.fieldKeys[model.Name+"\x00"+field.Name]
	if !exists {
		trace.err = errors.Join(trace.err, fmt.Errorf("migration execution operation used unregistered field %s.%s", model.Name, field.Name))
		return migrations.MigrationKey{App: "unknown", Name: field.Name}
	}
	return key
}

func migrationExecutionStepValues(
	ctx context.Context,
	trace *migrationExecutionTrace,
	before migrations.ProjectState,
	definitions []migrations.Migration,
	plan []migrations.PlanStep,
	includeHistoricalTransitions bool,
) ([]protocol.Value, error) {
	var historical map[migrations.MigrationKey]migrationExecutionHistoricalTransition
	if includeHistoricalTransitions {
		var err error
		historical, err = migrationExecutionHistoricalTransitions(ctx, before, definitions, plan)
		if err != nil {
			return nil, err
		}
	}
	steps := make([]protocol.Value, 0, len(plan))
	for _, step := range plan {
		fields := map[string]protocol.Value{
			"app":       protocol.String(step.Key.App),
			"direction": protocol.String(string(step.Direction)),
			"name":      protocol.String(step.Key.Name),
		}
		matched := trace.transaction(step)
		if matched == nil || !matched.operationStarted {
			fields["recorder_outcome"] = protocol.String("not_started")
			fields["schema_outcome"] = protocol.String("not_started")
			fields["status"] = protocol.String("not_started")
			fields["transaction_model"] = protocol.String("none")
		} else {
			fields["transaction_model"] = protocol.String("schema_and_record")
			switch {
			case matched.committed && !matched.rolledBack:
				if step.Direction == migrations.DirectionForward {
					fields["recorder_outcome"] = protocol.String("applied")
					fields["schema_outcome"] = protocol.String("applied")
				} else {
					fields["recorder_outcome"] = protocol.String("unapplied")
					fields["schema_outcome"] = protocol.String("reversed")
				}
				fields["status"] = protocol.String("committed")
			case matched.operationFailed && matched.rolledBack && !matched.committed:
				fields["recorder_outcome"] = protocol.String("not_started")
				fields["schema_outcome"] = protocol.String("rolled_back")
				fields["status"] = protocol.String("rolled_back")
			case matched.recorderFailed && matched.rolledBack && !matched.committed:
				fields["fault_point"] = protocol.String("before_record_write")
				if step.Direction == migrations.DirectionForward {
					fields["recorder_outcome"] = protocol.String("failed")
				} else {
					fields["recorder_outcome"] = protocol.String("retained")
				}
				fields["schema_outcome"] = protocol.String("rolled_back")
				fields["status"] = protocol.String("rolled_back")
			default:
				return nil, fmt.Errorf("migration execution step %s.%s has no terminal trace outcome", step.Key.App, step.Key.Name)
			}
		}
		if transition, ok := historical[step.Key]; ok {
			fields["historical_state_after"] = transition.after
			fields["historical_state_before"] = transition.before
		}
		steps = append(steps, protocol.Object(fields))
	}
	return steps, nil
}

func (trace *migrationExecutionTrace) transaction(step migrations.PlanStep) *migrationExecutionTransaction {
	var matched *migrationExecutionTransaction
	for _, transaction := range trace.transactions {
		if transaction.key != step.Key || transaction.direction != step.Direction {
			continue
		}
		if matched != nil {
			trace.err = errors.Join(trace.err, fmt.Errorf("migration execution step %s.%s started more than once", step.Key.App, step.Key.Name))
			return matched
		}
		matched = transaction
	}
	return matched
}

type migrationExecutionHistoricalTransition struct {
	before protocol.Value
	after  protocol.Value
}

func migrationExecutionHistoricalTransitions(
	ctx context.Context,
	before migrations.ProjectState,
	definitions []migrations.Migration,
	plan []migrations.PlanStep,
) (map[migrations.MigrationKey]migrationExecutionHistoricalTransition, error) {
	transitions := make(map[migrations.MigrationKey]migrationExecutionHistoricalTransition, len(plan))
	state := before.Clone()
	for _, step := range plan {
		from := migrationExecutionStateValue(state)
		next, err := (migrations.DirectExecutor{Backend: migrationExecutionStateBackend{}}).ExecutePlan(
			ctx,
			state,
			definitions,
			[]migrations.PlanStep{step},
		)
		if err != nil {
			return nil, fmt.Errorf("derive migration execution historical transition for %s.%s: %w", step.Key.App, step.Key.Name, err)
		}
		transitions[step.Key] = migrationExecutionHistoricalTransition{
			before: from,
			after:  migrationExecutionStateValue(next),
		}
		state = next
	}
	return transitions, nil
}

type migrationExecutionStateBackend struct{}

func (migrationExecutionStateBackend) BeginMigration(context.Context) (migrationbackend.Transaction, error) {
	return migrationExecutionStateTransaction{}, nil
}

type migrationExecutionStateTransaction struct{}

func (migrationExecutionStateTransaction) CreateModel(context.Context, ir.Model) error { return nil }
func (migrationExecutionStateTransaction) DeleteModel(context.Context, ir.Model) error { return nil }
func (migrationExecutionStateTransaction) AddField(context.Context, ir.Model, ir.Field) error {
	return nil
}
func (migrationExecutionStateTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	return nil
}
func (migrationExecutionStateTransaction) RecordApplied(context.Context, string, string) error {
	return nil
}
func (migrationExecutionStateTransaction) RecordUnapplied(context.Context, string, string) error {
	return nil
}
func (migrationExecutionStateTransaction) Commit(context.Context) error   { return nil }
func (migrationExecutionStateTransaction) Rollback(context.Context) error { return nil }
