package godj

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const (
	migrationApp             = "godj_migration"
	migrationArticleTable    = "godj_migration_article"
	migrationFailureApp      = "godj_failure"
	migrationFailureTable    = "godj_failure_broken"
	externalConflictTable    = "external_migration_conflict"
	externalRecoveryTable    = "external_migration_recovery"
	goDjMigrationRecordTable = "godj_migrations"
)

type migrationDatabaseScenario func(context.Context, *sqlite.Backend, *sql.DB) (protocol.Observation, error)

// withMigrationDatabase gives product code a single-connection SQLite Backend
// while a second database/sql handle observes committed external state. The
// observer never applies schema or recorder changes, so the differential
// result still exercises only GoDj's migration executor and backend.
func withMigrationDatabase(ctx context.Context, contractID string, scenario migrationDatabaseScenario) (protocol.Observation, error) {
	name := fmt.Sprintf(
		"godj-migration-conformance-%s-%d",
		strings.ToLower(strings.ReplaceAll(contractID, "-", "_")),
		databaseSequence.Add(1),
	)
	backend, err := sqlite.OpenMemory(ctx, name)
	if err != nil {
		return protocol.Observation{}, err
	}
	dataSourceName := "file:" + url.PathEscape(name) + "?mode=memory&cache=shared"
	observer, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return protocol.Observation{}, errors.Join(err, backend.Close())
	}
	if err := observer.PingContext(ctx); err != nil {
		return protocol.Observation{}, errors.Join(err, observer.Close(), backend.Close())
	}

	observation, scenarioErr := scenario(ctx, backend, observer)
	cleanupErr := errors.Join(observer.Close(), backend.Close())
	if scenarioErr != nil {
		return protocol.Observation{}, errors.Join(scenarioErr, cleanupErr)
	}
	if cleanupErr != nil {
		return protocol.Observation{}, cleanupErr
	}
	return observation, nil
}

func migrationCreateModel(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withMigrationDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend, observer *sql.DB) (protocol.Observation, error) {
		executor := migrations.Executor{Backend: backend}
		if _, err := executor.Apply(ctx, migrations.EmptyProjectState(), initialMigration()); err != nil {
			return protocol.Observation{}, err
		}
		return migrationObservation(ctx, observer, contractID, protocol.PhaseCommit, migrationArticleTable, []string{"title", "published"}, nil, nil)
	})
}

func migrationAddNullableField(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withMigrationDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend, observer *sql.DB) (protocol.Observation, error) {
		executor := migrations.Executor{Backend: backend}
		state1, err := executor.Apply(ctx, migrations.EmptyProjectState(), initialMigration())
		if err != nil {
			return protocol.Observation{}, err
		}
		if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published") VALUES (?, ?)`, "Existing", false); err != nil {
			return protocol.Observation{}, fmt.Errorf("seed existing migration row: %w", err)
		}
		if _, err := executor.Apply(ctx, state1, summaryMigration()); err != nil {
			return protocol.Observation{}, err
		}
		return migrationObservation(ctx, observer, contractID, protocol.PhaseCommit, migrationArticleTable, []string{"title", "published", "summary"}, nil, nil)
	})
}

func migrationReverseNullableField(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withMigrationDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend, observer *sql.DB) (protocol.Observation, error) {
		executor := migrations.Executor{Backend: backend}
		state1, err := executor.Apply(ctx, migrations.EmptyProjectState(), initialMigration())
		if err != nil {
			return protocol.Observation{}, err
		}
		if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published") VALUES (?, ?)`, "Existing", false); err != nil {
			return protocol.Observation{}, fmt.Errorf("seed row before AddField: %w", err)
		}
		state2, err := executor.Apply(ctx, state1, summaryMigration())
		if err != nil {
			return protocol.Observation{}, err
		}
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "godj_migration_article" ("title", "published", "summary") VALUES (?, ?, ?)`,
			"After add",
			false,
			"Discarded column",
		); err != nil {
			return protocol.Observation{}, fmt.Errorf("seed row after AddField: %w", err)
		}
		state1Again, err := executor.Unapply(ctx, state2, summaryMigration())
		if err != nil {
			return protocol.Observation{}, err
		}
		if !state1Again.Equal(state1) {
			return protocol.Observation{}, errors.New("reversed migration state does not equal state before AddField")
		}
		return migrationObservation(ctx, observer, contractID, protocol.PhaseCommit, migrationArticleTable, []string{"title", "published"}, nil, nil)
	})
}

func migrationAtomicFailure(ctx context.Context, contractID string) (protocol.Observation, error) {
	return withMigrationDatabase(ctx, contractID, func(ctx context.Context, backend *sqlite.Backend, observer *sql.DB) (protocol.Observation, error) {
		provision := []string{
			`CREATE TABLE "external_migration_conflict" ("sentinel" INTEGER NOT NULL)`,
			`CREATE TABLE "external_migration_recovery" ("one" INTEGER NOT NULL, "two" INTEGER NOT NULL)`,
			`INSERT INTO "external_migration_recovery" ("one", "two") VALUES (1, 2)`,
		}
		for _, statement := range provision {
			if _, err := backend.ExecContext(ctx, statement); err != nil {
				return protocol.Observation{}, fmt.Errorf("provision migration failure fixture: %w", err)
			}
		}

		before := migrations.EmptyProjectState()
		observedBackend := &observedMigrationBackend{Backend: backend}
		after, executionErr := (migrations.Executor{Backend: observedBackend}).Apply(ctx, before, failureMigration())
		if executionErr == nil {
			return protocol.Observation{}, errors.New("atomic failure migration unexpectedly succeeded")
		}
		if !after.Equal(before) {
			return protocol.Observation{}, errors.New("atomic failure changed returned project state")
		}
		var migrationErr *migrations.Error
		if !errors.As(executionErr, &migrationErr) {
			return protocol.Observation{}, fmt.Errorf("atomic failure error = %T, want *migrations.Error", executionErr)
		}
		if migrationErr.Category != migrations.CategoryExecution ||
			migrationErr.Code != migrations.CodeOperationFailed ||
			migrationErr.OperationIndex != 1 {
			return protocol.Observation{}, fmt.Errorf("atomic failure classification = %#v", migrationErr)
		}
		if observedBackend.createModelCalls != 2 {
			return protocol.Observation{}, fmt.Errorf("atomic failure CreateModel calls = %d, want 2", observedBackend.createModelCalls)
		}
		if exists, err := sqliteTableExistsContext(ctx, observer, externalConflictTable); err != nil || !exists {
			return protocol.Observation{}, fmt.Errorf("external conflict table was not preserved: exists=%t err=%v", exists, err)
		}
		if exists, err := sqliteTableExistsContext(ctx, observer, migrationFailureTable); err != nil || exists {
			return protocol.Observation{}, fmt.Errorf("failed migration table survived rollback: exists=%t err=%v", exists, err)
		}
		if exists, err := sqliteTableExistsContext(ctx, observer, goDjMigrationRecordTable); err != nil || exists {
			return protocol.Observation{}, fmt.Errorf("migration recorder table survived rollback: exists=%t err=%v", exists, err)
		}

		queryResult, err := readRecoveryProbe(ctx, backend, "one")
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("query after failed migration: %w", err)
		}
		autocommitRestored, err := verifyAutocommitRestored(ctx, backend, observer)
		if err != nil {
			return protocol.Observation{}, err
		}
		transactionResult := 0
		outsideAtomic := false
		if err := backend.Atomic(ctx, func(session db.Session) error {
			var err error
			transactionResult, err = readRecoveryProbe(ctx, session, "two")
			return err
		}); err != nil {
			return protocol.Observation{}, fmt.Errorf("transaction after failed migration: %w", err)
		}
		outsideAtomic = true
		metrics := protocol.Object(map[string]protocol.Value{
			"connection_recovery": protocol.Object(map[string]protocol.Value{
				"autocommit_restored":           protocol.Boolean(autocommitRestored),
				"outside_atomic":                protocol.Boolean(outsideAtomic),
				"subsequent_query_result":       protocol.Integer(strconv.Itoa(queryResult)),
				"subsequent_transaction_result": protocol.Integer(strconv.Itoa(transactionResult)),
			}),
		})
		observedError := &protocol.ObservedError{
			Category:          string(migrationErr.Category),
			Code:              string(migrationErr.Code),
			Message:           executionErr.Error(),
			MessageIsContract: boolPointer(false),
		}
		return migrationObservation(ctx, observer, contractID, protocol.PhaseRollback, migrationFailureTable, nil, observedError, &metrics)
	})
}

type observedMigrationBackend struct {
	Backend          *sqlite.Backend
	createModelCalls int
}

func (backend *observedMigrationBackend) BeginMigration(ctx context.Context) (migrationbackend.Transaction, error) {
	transaction, err := backend.Backend.BeginMigration(ctx)
	if err != nil {
		return nil, err
	}
	return &observedMigrationTransaction{Transaction: transaction, backend: backend}, nil
}

type observedMigrationTransaction struct {
	migrationbackend.Transaction
	backend *observedMigrationBackend
}

func (transaction *observedMigrationTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	transaction.backend.createModelCalls++
	if err := transaction.Transaction.CreateModel(ctx, model); err != nil {
		return err
	}
	inspector, ok := transaction.Transaction.(interface {
		TableExists(context.Context, string) (bool, error)
	})
	if !ok {
		return errors.New("SQLite migration transaction does not expose in-transaction schema inspection")
	}
	exists, err := inspector.TableExists(ctx, model.DBTable)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("CreateModel returned without creating %s inside its transaction", model.DBTable)
	}
	return nil
}

func initialMigration() migrations.Migration {
	return migrations.Migration{
		App:  migrationApp,
		Name: "0001_initial",
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: migrationApp, Model: migrationArticleModel(false)},
		},
	}
}

func summaryMigration() migrations.Migration {
	return migrations.Migration{
		App:  migrationApp,
		Name: "0002_summary",
		Operations: []migrations.Operation{
			migrations.AddField{
				AppLabel:  migrationApp,
				ModelName: "article",
				Field: ir.Field{
					Name:      "summary",
					GoName:    "Summary",
					Column:    "summary",
					Kind:      ir.FieldChar,
					Nullable:  true,
					MaxLength: 200,
				},
			},
		},
	}
}

func failureMigration() migrations.Migration {
	return migrations.Migration{
		App:  migrationFailureApp,
		Name: "0001_failure",
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: migrationFailureApp, Model: ir.Model{
				Name:    "broken",
				GoName:  "Broken",
				DBTable: migrationFailureTable,
				Fields:  []ir.Field{autoField()},
			}},
			migrations.CreateModel{AppLabel: migrationFailureApp, Model: ir.Model{
				Name:    "conflict",
				GoName:  "Conflict",
				DBTable: externalConflictTable,
				Fields:  []ir.Field{autoField()},
			}},
		},
	}
}

func migrationArticleModel(withSummary bool) ir.Model {
	fields := []ir.Field{
		autoField(),
		{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		{
			Name:    "published",
			GoName:  "Published",
			Column:  "published",
			Kind:    ir.FieldBoolean,
			Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false},
		},
	}
	if withSummary {
		fields = append(fields, ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200})
	}
	return ir.Model{Name: "article", GoName: "Article", DBTable: migrationArticleTable, Fields: fields}
}

func autoField() ir.Field {
	return ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
}

func migrationObservation(
	ctx context.Context,
	observer *sql.DB,
	contractID string,
	phase protocol.Phase,
	table string,
	rowFields []string,
	observedError *protocol.ObservedError,
	metrics *protocol.Value,
) (protocol.Observation, error) {
	state, err := migrationDatabaseState(ctx, observer, table, rowFields)
	if err != nil {
		return protocol.Observation{}, err
	}
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   phase,
		Error:   observedError,
		DBState: valuePointer(state),
		Metrics: metrics,
	}, nil
}

func migrationDatabaseState(ctx context.Context, observer *sql.DB, table string, rowFields []string) (protocol.Value, error) {
	managedTables, err := readManagedTables(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	records, err := readMigrationRecords(ctx, observer)
	if err != nil {
		return protocol.Value{}, err
	}
	schema, exists, err := readSQLiteSchema(ctx, observer, table)
	if err != nil {
		return protocol.Value{}, err
	}
	rows := protocol.List()
	if exists && len(rowFields) > 0 {
		rows, err = readMigrationRows(ctx, observer, table, rowFields)
		if err != nil {
			return protocol.Value{}, err
		}
	}
	return protocol.Object(map[string]protocol.Value{
		"managed_tables":    managedTables,
		"migration_records": records,
		"rows":              rows,
		"schema":            schema,
	}), nil
}

func readManagedTables(ctx context.Context, observer *sql.DB) (protocol.Value, error) {
	rows, err := observer.QueryContext(ctx, `SELECT "name" FROM "sqlite_schema" WHERE "type" = 'table' ORDER BY "name"`)
	if err != nil {
		return protocol.Value{}, fmt.Errorf("list SQLite tables: %w", err)
	}
	defer rows.Close()
	values := make([]protocol.Value, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return protocol.Value{}, fmt.Errorf("scan SQLite table: %w", err)
		}
		if strings.HasPrefix(name, "godj_failure_") || strings.HasPrefix(name, "godj_migration_") {
			values = append(values, protocol.String(name))
		}
	}
	if err := rows.Err(); err != nil {
		return protocol.Value{}, fmt.Errorf("iterate SQLite tables: %w", err)
	}
	return protocol.List(values...), nil
}

func readMigrationRecords(ctx context.Context, observer *sql.DB) (protocol.Value, error) {
	exists, err := sqliteTableExistsContext(ctx, observer, goDjMigrationRecordTable)
	if err != nil {
		return protocol.Value{}, err
	}
	if !exists {
		return protocol.List(), nil
	}
	rows, err := observer.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
	if err != nil {
		return protocol.Value{}, fmt.Errorf("read migration records: %w", err)
	}
	defer rows.Close()
	values := make([]protocol.Value, 0)
	for rows.Next() {
		var app, name string
		if err := rows.Scan(&app, &name); err != nil {
			return protocol.Value{}, fmt.Errorf("scan migration record: %w", err)
		}
		values = append(values, protocol.Object(map[string]protocol.Value{
			"app":  protocol.String(app),
			"name": protocol.String(name),
		}))
	}
	if err := rows.Err(); err != nil {
		return protocol.Value{}, fmt.Errorf("iterate migration records: %w", err)
	}
	return protocol.List(values...), nil
}

func readSQLiteSchema(ctx context.Context, observer *sql.DB, table string) (protocol.Value, bool, error) {
	exists, err := sqliteTableExistsContext(ctx, observer, table)
	if err != nil {
		return protocol.Value{}, false, err
	}
	columns := make([]protocol.Value, 0)
	if exists {
		rows, err := observer.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
		if err != nil {
			return protocol.Value{}, false, fmt.Errorf("describe SQLite table %s: %w", table, err)
		}
		defer rows.Close()
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
				return protocol.Value{}, false, fmt.Errorf("scan SQLite column: %w", err)
			}
			typeFamily, err := sqliteTypeFamily(declaredType)
			if err != nil {
				return protocol.Value{}, false, fmt.Errorf("column %s.%s: %w", table, name, err)
			}
			columns = append(columns, protocol.Object(map[string]protocol.Value{
				"has_database_default": protocol.Boolean(defaultValue.Valid),
				"name":                 protocol.String(name),
				"nullable":             protocol.Boolean(notNull == 0 && primaryKey == 0),
				"primary_key":          protocol.Boolean(primaryKey != 0),
				"type_family":          protocol.String(typeFamily),
			}))
		}
		if err := rows.Err(); err != nil {
			return protocol.Value{}, false, fmt.Errorf("iterate SQLite columns: %w", err)
		}
	}
	return protocol.Object(map[string]protocol.Value{
		"columns": protocol.List(columns...),
		"exists":  protocol.Boolean(exists),
		"name":    protocol.String(table),
	}), exists, nil
}

func readMigrationRows(ctx context.Context, observer *sql.DB, table string, fields []string) (protocol.Value, error) {
	columns := []string{quoteSQLiteIdentifier("id")}
	for _, field := range fields {
		columns = append(columns, quoteSQLiteIdentifier(field))
	}
	statement := "SELECT " + strings.Join(columns, ", ") + " FROM " + quoteSQLiteIdentifier(table) + ` ORDER BY "id"`
	rows, err := observer.QueryContext(ctx, statement)
	if err != nil {
		return protocol.Value{}, fmt.Errorf("read migration table rows: %w", err)
	}
	defer rows.Close()
	values := make([]protocol.Value, 0)
	for rows.Next() {
		var (
			id        int64
			title     string
			published bool
			summary   sql.NullString
		)
		destinations := []any{&id}
		for _, field := range fields {
			switch field {
			case "title":
				destinations = append(destinations, &title)
			case "published":
				destinations = append(destinations, &published)
			case "summary":
				destinations = append(destinations, &summary)
			default:
				return protocol.Value{}, fmt.Errorf("unsupported migration row field %q", field)
			}
		}
		if err := rows.Scan(destinations...); err != nil {
			return protocol.Value{}, fmt.Errorf("scan migration row: %w", err)
		}
		row := map[string]protocol.Value{"id": primaryKeyValue(id)}
		for _, field := range fields {
			switch field {
			case "title":
				row[field] = protocol.String(title)
			case "published":
				row[field] = protocol.Boolean(published)
			case "summary":
				if summary.Valid {
					row[field] = protocol.String(summary.String)
				} else {
					row[field] = protocol.Null()
				}
			}
		}
		values = append(values, protocol.Object(row))
	}
	if err := rows.Err(); err != nil {
		return protocol.Value{}, fmt.Errorf("iterate migration rows: %w", err)
	}
	return protocol.List(values...), nil
}

func sqliteTableExistsContext(ctx context.Context, observer *sql.DB, table string) (bool, error) {
	var count int
	if err := observer.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?`,
		table,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check SQLite table %s: %w", table, err)
	}
	return count == 1, nil
}

func sqliteTypeFamily(declaredType string) (string, error) {
	upper := strings.ToUpper(declaredType)
	switch {
	case strings.Contains(upper, "BOOL"):
		return "boolean", nil
	case strings.Contains(upper, "INT"):
		return "integer", nil
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		return "text", nil
	default:
		return "", fmt.Errorf("unsupported declared type %q", declaredType)
	}
}

func quoteSQLiteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func readRecoveryProbe(ctx context.Context, queryer db.Queryer, column string) (int, error) {
	field := query.NewFieldRef(column, column, query.FieldInteger, false)
	rows, err := queryer.Query(ctx, query.NewPlan(externalRecoveryTable, []query.FieldRef{field}))
	if err != nil {
		return 0, err
	}
	if rows == nil {
		return 0, errors.New("recovery probe returned nil rows")
	}
	if !rows.Next() {
		iterateErr := rows.Err()
		closeErr := rows.Close()
		return 0, errors.Join(errors.New("recovery probe returned no rows"), iterateErr, closeErr)
	}
	var value int
	if err := rows.Scan(&value); err != nil {
		return 0, errors.Join(err, rows.Close())
	}
	if rows.Next() {
		return 0, errors.Join(errors.New("recovery probe returned more than one row"), rows.Close())
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil || closeErr != nil {
		return 0, errors.Join(iterateErr, closeErr)
	}
	return value, nil
}

func verifyAutocommitRestored(ctx context.Context, backend *sqlite.Backend, observer *sql.DB) (bool, error) {
	if _, err := backend.ExecContext(ctx, `UPDATE "external_migration_recovery" SET "one" = 3`); err != nil {
		return false, fmt.Errorf("autocommit recovery write: %w", err)
	}
	var visible int
	if err := observer.QueryRowContext(ctx, `SELECT "one" FROM "external_migration_recovery"`).Scan(&visible); err != nil {
		return false, fmt.Errorf("observe autocommit recovery write: %w", err)
	}
	if visible != 3 {
		return false, fmt.Errorf("autocommit recovery value = %d, want 3", visible)
	}
	if _, err := backend.ExecContext(ctx, `UPDATE "external_migration_recovery" SET "one" = 1`); err != nil {
		return false, fmt.Errorf("restore autocommit recovery fixture: %w", err)
	}
	if err := observer.QueryRowContext(ctx, `SELECT "one" FROM "external_migration_recovery"`).Scan(&visible); err != nil {
		return false, fmt.Errorf("observe restored recovery fixture: %w", err)
	}
	return visible == 1, nil
}
