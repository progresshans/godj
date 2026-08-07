package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

const migrationRecorderTable = "godj_migrations"

const createMigrationRecorderTableSQL = `CREATE TABLE IF NOT EXISTS "godj_migrations" (` +
	`"app" VARCHAR(255) NOT NULL, ` +
	`"name" VARCHAR(255) NOT NULL, ` +
	`PRIMARY KEY ("app", "name"))`

var _ migrationbackend.AtomicBackend = (*Backend)(nil)
var _ migrationbackend.Transaction = (*migrationTransaction)(nil)

// migrationTransaction deliberately owns both the schema editor and recorder.
// Keeping one pinned sql.Conn and one sql.Tx prevents DDL and recorder state
// from becoming visible independently.
type migrationTransaction struct {
	mu          sync.Mutex
	connection  *sql.Conn
	transaction *sql.Tx
	done        bool
}

func (b *Backend) BeginMigration(ctx context.Context) (migrationbackend.Transaction, error) {
	if b == nil || b.database == nil || b.closed.Load() {
		return nil, errors.New("begin SQLite migration: backend is nil or closed")
	}
	if ctx == nil {
		return nil, errors.New("begin SQLite migration: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin SQLite migration: %w", err)
	}

	connection, err := b.database.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire SQLite migration connection: %w", err)
	}
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		closeErr := connection.Close()
		return nil, errors.Join(fmt.Errorf("begin SQLite migration transaction: %w", err), wrapCloseMigrationConnection(closeErr))
	}
	return &migrationTransaction{connection: connection, transaction: transaction}, nil
}

func (transaction *migrationTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	statement, err := compileMigrationCreateModel(model)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, func(sqlTransaction *sql.Tx) error {
		if _, err := sqlTransaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create SQLite model %q: %w", model.DBTable, err)
		}
		return nil
	})
}

func (transaction *migrationTransaction) DeleteModel(ctx context.Context, model ir.Model) error {
	statement, err := compileMigrationDeleteModel(model)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, func(sqlTransaction *sql.Tx) error {
		if _, err := sqlTransaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("delete SQLite model %q: %w", model.DBTable, err)
		}
		return nil
	})
}

// TableExists reports schema visibility inside this exact migration
// transaction. The concrete transaction type remains private; conformance
// instrumentation can opt into this narrow method through a local interface
// without broadening the backend-neutral executor contract.
func (transaction *migrationTransaction) TableExists(ctx context.Context, table string) (bool, error) {
	if table == "" {
		return false, errors.New("inspect SQLite migration table: name is empty")
	}
	var count int
	err := transaction.execute(ctx, func(sqlTransaction *sql.Tx) error {
		if err := sqlTransaction.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?`,
			table,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect SQLite migration table %q: %w", table, err)
		}
		return nil
	})
	return count == 1, err
}

func (transaction *migrationTransaction) AddField(ctx context.Context, model ir.Model, field ir.Field) error {
	if field.PrimaryKey {
		return migrationbackend.NewCapabilityError(
			"sqlite_add_field",
			fmt.Sprintf("field %s.%s must be non-primary-key", model.DBTable, field.Column),
			nil,
		)
	}
	if field.Default != nil {
		return migrationbackend.NewCapabilityError(
			"sqlite_add_field",
			fmt.Sprintf("field %s.%s has a default; one-time backfill without a persistent database default requires table rebuild", model.DBTable, field.Column),
			nil,
		)
	}
	statement, err := compileMigrationAddField(model, field)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, func(sqlTransaction *sql.Tx) error {
		if !field.Nullable {
			empty, err := sqliteTableEmpty(ctx, sqlTransaction, model.DBTable)
			if err != nil {
				return err
			}
			if !empty {
				return migrationbackend.NewCapabilityError(
					"sqlite_add_field",
					fmt.Sprintf("table %s contains rows; adding non-null field %s requires table rebuild", model.DBTable, field.Column),
					nil,
				)
			}
		}
		if _, err := sqlTransaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add SQLite field %s.%s: %w", model.DBTable, field.Column, err)
		}
		return nil
	})
}

func (transaction *migrationTransaction) RemoveField(ctx context.Context, model ir.Model, field ir.Field) error {
	if field.PrimaryKey {
		return migrationbackend.NewCapabilityError(
			"sqlite_drop_column",
			fmt.Sprintf("field %s.%s must be non-primary-key", model.DBTable, field.Column),
			nil,
		)
	}
	statement, err := compileMigrationRemoveField(model, field)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, func(sqlTransaction *sql.Tx) error {
		if err := preflightSQLiteDropColumn(ctx, sqlTransaction, model, field); err != nil {
			return err
		}
		if _, err := sqlTransaction.ExecContext(ctx, statement); err != nil {
			if sqliteDropColumnCapabilityFailure(err) {
				return migrationbackend.NewCapabilityError(
					"sqlite_drop_column",
					fmt.Sprintf("SQLite rejected native DROP COLUMN for %s.%s; table rebuild is disabled", model.DBTable, field.Column),
					err,
				)
			}
			return fmt.Errorf("remove SQLite field %s.%s: %w", model.DBTable, field.Column, err)
		}
		return nil
	})
}

func (transaction *migrationTransaction) RecordApplied(ctx context.Context, app, name string) error {
	if app == "" || name == "" {
		return errors.New("record applied SQLite migration: app and name are required")
	}
	return transaction.execute(ctx, func(sqlTransaction *sql.Tx) error {
		if err := ensureMigrationRecorder(ctx, sqlTransaction); err != nil {
			return err
		}
		if _, err := sqlTransaction.ExecContext(
			ctx,
			`INSERT INTO "godj_migrations" ("app", "name") VALUES (?, ?)`,
			app,
			name,
		); err != nil {
			return fmt.Errorf("record applied SQLite migration %s.%s: %w", app, name, err)
		}
		return nil
	})
}

func (transaction *migrationTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	if app == "" || name == "" {
		return errors.New("record unapplied SQLite migration: app and name are required")
	}
	return transaction.execute(ctx, func(sqlTransaction *sql.Tx) error {
		if err := ensureMigrationRecorder(ctx, sqlTransaction); err != nil {
			return err
		}
		result, err := sqlTransaction.ExecContext(
			ctx,
			`DELETE FROM "godj_migrations" WHERE "app" = ? AND "name" = ?`,
			app,
			name,
		)
		if err != nil {
			return fmt.Errorf("record unapplied SQLite migration %s.%s: %w", app, name, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count removed SQLite migration record %s.%s: %w", app, name, err)
		}
		if rowsAffected != 1 {
			return fmt.Errorf("record unapplied SQLite migration %s.%s: removed %d records, want 1", app, name, rowsAffected)
		}
		return nil
	})
}

func (transaction *migrationTransaction) Commit(ctx context.Context) error {
	if transaction == nil {
		return errors.New("commit SQLite migration: transaction is nil")
	}
	if ctx == nil {
		return errors.New("commit SQLite migration: context is nil")
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return errors.New("commit SQLite migration: transaction is already complete")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("commit SQLite migration: %w", err)
	}
	transaction.done = true
	commitErr := transaction.transaction.Commit()
	closeErr := transaction.connection.Close()
	return errors.Join(wrapCommitMigration(commitErr), wrapCloseMigrationConnection(closeErr))
}

func (transaction *migrationTransaction) Rollback(ctx context.Context) error {
	if transaction == nil {
		return errors.New("rollback SQLite migration: transaction is nil")
	}
	if ctx == nil {
		return errors.New("rollback SQLite migration: context is nil")
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	// Executor makes a best-effort Rollback after a failed Commit. At that
	// point database/sql has already resolved the transaction, so the repeated
	// terminal call is intentionally harmless.
	if transaction.done {
		return nil
	}
	transaction.done = true
	rollbackErr := transaction.transaction.Rollback()
	if errors.Is(rollbackErr, sql.ErrTxDone) {
		rollbackErr = nil
	}
	closeErr := transaction.connection.Close()
	return errors.Join(wrapRollbackMigration(rollbackErr), wrapCloseMigrationConnection(closeErr))
}

func (transaction *migrationTransaction) execute(ctx context.Context, operation func(*sql.Tx) error) error {
	if transaction == nil || transaction.transaction == nil {
		return errors.New("execute SQLite migration: transaction is nil")
	}
	if ctx == nil {
		return errors.New("execute SQLite migration: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("execute SQLite migration: %w", err)
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return errors.New("execute SQLite migration: transaction is already complete")
	}
	return operation(transaction.transaction)
}

func ensureMigrationRecorder(ctx context.Context, transaction *sql.Tx) error {
	if _, err := transaction.ExecContext(ctx, createMigrationRecorderTableSQL); err != nil {
		return fmt.Errorf("ensure SQLite migration recorder: %w", err)
	}
	return nil
}

func sqliteTableEmpty(ctx context.Context, transaction *sql.Tx, table string) (bool, error) {
	quotedTable, err := quoteIdentifier(table)
	if err != nil {
		return false, fmt.Errorf("inspect SQLite table %q: %w", table, err)
	}
	var hasRows int
	if err := transaction.QueryRowContext(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM "+quotedTable+" LIMIT 1)",
	).Scan(&hasRows); err != nil {
		return false, fmt.Errorf("inspect whether SQLite table %q is empty: %w", table, err)
	}
	return hasRows == 0, nil
}

func preflightSQLiteDropColumn(ctx context.Context, transaction *sql.Tx, model ir.Model, field ir.Field) error {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return fmt.Errorf("inspect SQLite table %q: %w", model.DBTable, err)
	}
	column, exists, _, primaryKey, err := sqliteColumnShape(ctx, transaction, table, field.Column)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("inspect SQLite drop column: field %s.%s does not exist", model.DBTable, field.Column)
	}
	if primaryKey {
		return migrationbackend.NewCapabilityError(
			"sqlite_drop_column",
			fmt.Sprintf("database column %s.%s must be non-primary-key", model.DBTable, column),
			nil,
		)
	}
	if err := rejectSQLiteIndexDependency(ctx, transaction, model.DBTable, field.Column); err != nil {
		return err
	}
	if err := rejectSQLiteTriggerDependency(ctx, transaction, model.DBTable, field.Column); err != nil {
		return err
	}
	if err := rejectSQLiteViewDependency(ctx, transaction, model.DBTable, field.Column); err != nil {
		return err
	}
	return rejectSQLiteForeignKeyDependency(ctx, transaction, model.DBTable, field.Column)
}

func sqliteColumnShape(ctx context.Context, transaction *sql.Tx, quotedTable, wanted string) (_ string, exists, nullable, primaryKey bool, resultErr error) {
	rows, err := transaction.QueryContext(ctx, "PRAGMA table_info("+quotedTable+")")
	if err != nil {
		return "", false, false, false, fmt.Errorf("inspect SQLite table columns: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, wrapCloseRows(rows.Close()))
	}()
	for rows.Next() {
		var (
			sequence     int
			name         string
			declaredType string
			notNull      int
			defaultValue sql.NullString
			primary      int
		)
		if err := rows.Scan(&sequence, &name, &declaredType, &notNull, &defaultValue, &primary); err != nil {
			return "", false, false, false, fmt.Errorf("scan SQLite table column: %w", err)
		}
		if name == wanted {
			return name, true, notNull == 0, primary != 0, rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, false, false, fmt.Errorf("iterate SQLite table columns: %w", err)
	}
	return "", false, false, false, nil
}

func rejectSQLiteIndexDependency(ctx context.Context, transaction *sql.Tx, table, column string) (resultErr error) {
	quotedTable, err := quoteIdentifier(table)
	if err != nil {
		return err
	}
	rows, err := transaction.QueryContext(ctx, "PRAGMA index_list("+quotedTable+")")
	if err != nil {
		return fmt.Errorf("inspect SQLite indexes for %q: %w", table, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, wrapCloseRows(rows.Close()))
	}()
	var indexes []string
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			return fmt.Errorf("scan SQLite index for %q: %w", table, err)
		}
		indexes = append(indexes, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite indexes for %q: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close SQLite index list for %q: %w", table, err)
	}

	for _, index := range indexes {
		quotedIndex, err := quoteIdentifier(index)
		if err != nil {
			return fmt.Errorf("inspect SQLite index %q: %w", index, err)
		}
		indexRows, err := transaction.QueryContext(ctx, "PRAGMA index_info("+quotedIndex+")")
		if err != nil {
			return fmt.Errorf("inspect SQLite index %q columns: %w", index, err)
		}
		depends := false
		for indexRows.Next() {
			var sequence, columnID int
			var name sql.NullString
			if err := indexRows.Scan(&sequence, &columnID, &name); err != nil {
				_ = indexRows.Close()
				return fmt.Errorf("scan SQLite index %q column: %w", index, err)
			}
			if name.Valid && name.String == column {
				depends = true
			}
		}
		iterateErr := indexRows.Err()
		closeErr := indexRows.Close()
		if iterateErr != nil || closeErr != nil {
			return errors.Join(
				wrapSQLiteIteration("index "+index, iterateErr),
				wrapCloseRows(closeErr),
			)
		}
		var definition sql.NullString
		if err := transaction.QueryRowContext(
			ctx,
			`SELECT "sql" FROM "sqlite_schema" WHERE "type" = 'index' AND "name" = ?`,
			index,
		).Scan(&definition); err != nil {
			return fmt.Errorf("read SQLite index %q definition: %w", index, err)
		}
		// index_info() omits columns used only by an expression or partial-index
		// predicate. Inspect the stored SQL as a conservative second gate.
		depends = depends || definition.Valid && identifierAppears(definition.String, column)
		if depends {
			return migrationbackend.NewCapabilityError(
				"sqlite_drop_column",
				fmt.Sprintf("column %s.%s is referenced by index %s", table, column, index),
				nil,
			)
		}
	}
	return nil
}

func rejectSQLiteTriggerDependency(ctx context.Context, transaction *sql.Tx, table, column string) (resultErr error) {
	rows, err := transaction.QueryContext(ctx, `SELECT "name", "tbl_name", "sql" FROM "sqlite_schema" WHERE "type" = 'trigger' AND "sql" IS NOT NULL ORDER BY "name"`)
	if err != nil {
		return fmt.Errorf("inspect SQLite triggers: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, wrapCloseRows(rows.Close()))
	}()
	for rows.Next() {
		var name, owner, definition string
		if err := rows.Scan(&name, &owner, &definition); err != nil {
			return fmt.Errorf("scan SQLite trigger: %w", err)
		}
		// A trigger owned by the table can depend on OLD/NEW fields or SELECT *
		// without spelling the column in a form SQLite's SQL text can expose.
		if owner == table || identifierAppears(definition, table) {
			return migrationbackend.NewCapabilityError(
				"sqlite_drop_column",
				fmt.Sprintf("column %s.%s may be referenced by trigger %s", table, column, name),
				nil,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite triggers: %w", err)
	}
	return nil
}

func rejectSQLiteViewDependency(ctx context.Context, transaction *sql.Tx, table, column string) (resultErr error) {
	rows, err := transaction.QueryContext(ctx, `SELECT "name", "sql" FROM "sqlite_schema" WHERE "type" = 'view' AND "sql" IS NOT NULL ORDER BY "name"`)
	if err != nil {
		return fmt.Errorf("inspect SQLite views: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, wrapCloseRows(rows.Close()))
	}()
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return fmt.Errorf("scan SQLite view: %w", err)
		}
		// A view selecting table.* (or simply *) depends on every column even
		// though its stored SQL never names this one. Conservatively reject any
		// view that names the table rather than guessing the projected columns.
		if identifierAppears(definition, table) {
			return migrationbackend.NewCapabilityError(
				"sqlite_drop_column",
				fmt.Sprintf("column %s.%s may be referenced by view %s", table, column, name),
				nil,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite views: %w", err)
	}
	return nil
}

func rejectSQLiteForeignKeyDependency(ctx context.Context, transaction *sql.Tx, table, column string) (resultErr error) {
	rows, err := transaction.QueryContext(ctx, `SELECT "name" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" NOT LIKE 'sqlite_%' ORDER BY "name"`)
	if err != nil {
		return fmt.Errorf("list SQLite tables for foreign-key inspection: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, wrapCloseRows(rows.Close()))
	}()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan SQLite table for foreign-key inspection: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite tables for foreign-key inspection: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close SQLite table list: %w", err)
	}

	for _, currentTable := range tables {
		quotedTable, err := quoteIdentifier(currentTable)
		if err != nil {
			return fmt.Errorf("inspect SQLite foreign keys for %q: %w", currentTable, err)
		}
		foreignKeys, err := transaction.QueryContext(ctx, "PRAGMA foreign_key_list("+quotedTable+")")
		if err != nil {
			return fmt.Errorf("inspect SQLite foreign keys for %q: %w", currentTable, err)
		}
		for foreignKeys.Next() {
			var id, sequence int
			var referencedTable, from, onUpdate, onDelete, match string
			var to sql.NullString
			if err := foreignKeys.Scan(&id, &sequence, &referencedTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				_ = foreignKeys.Close()
				return fmt.Errorf("scan SQLite foreign key for %q: %w", currentTable, err)
			}
			outbound := currentTable == table && from == column
			inbound := referencedTable == table && to.Valid && to.String == column
			if outbound || inbound {
				_ = foreignKeys.Close()
				return migrationbackend.NewCapabilityError(
					"sqlite_drop_column",
					fmt.Sprintf("column %s.%s is referenced by foreign key on table %s", table, column, currentTable),
					nil,
				)
			}
		}
		iterateErr := foreignKeys.Err()
		closeErr := foreignKeys.Close()
		if iterateErr != nil || closeErr != nil {
			return errors.Join(
				wrapSQLiteIteration("foreign keys for "+currentTable, iterateErr),
				wrapCloseRows(closeErr),
			)
		}
	}
	return nil
}

func identifierAppears(statement, identifier string) bool {
	statement = strings.ToLower(statement)
	identifier = strings.ToLower(identifier)
	for offset := 0; ; {
		index := strings.Index(statement[offset:], identifier)
		if index < 0 {
			return false
		}
		index += offset
		beforeBoundary := index == 0 || !sqliteIdentifierByte(statement[index-1])
		after := index + len(identifier)
		afterBoundary := after == len(statement) || !sqliteIdentifierByte(statement[after])
		if beforeBoundary && afterBoundary {
			return true
		}
		offset = index + len(identifier)
	}
}

func sqliteIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func sqliteDropColumnCapabilityFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cannot drop") ||
		strings.Contains(message, "error in index") ||
		strings.Contains(message, "error in trigger") ||
		strings.Contains(message, "error in view") ||
		(strings.Contains(message, "error in table") && strings.Contains(message, "after drop column")) ||
		strings.Contains(message, "foreign key")
}

func wrapCommitMigration(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("commit SQLite migration: %w", err)
}

func wrapRollbackMigration(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rollback SQLite migration: %w", err)
}

func wrapCloseMigrationConnection(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close SQLite migration connection: %w", err)
}

func wrapCloseRows(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close SQLite inspection rows: %w", err)
}

func wrapSQLiteIteration(subject string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("iterate SQLite %s: %w", subject, err)
}
