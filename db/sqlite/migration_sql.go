package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/progresshans/godj/schema/ir"
)

// migrationSQLExecutor is the common statement boundary shared by direct
// database/sql transactions and the loaded revision-fenced lifecycle. Keeping
// this private prevents the manual BEGIN IMMEDIATE implementation from leaking
// database/sql details into the backend-neutral migration ports.
type migrationSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var _ migrationSQLExecutor = (*sql.Tx)(nil)
var _ migrationSQLExecutor = (*sql.Conn)(nil)

func compileMigrationCreateModel(model ir.Model) (string, error) {
	if model.DBTable == "" {
		return "", fmt.Errorf("compile SQLite CreateModel: table is empty")
	}
	if len(model.Fields) == 0 {
		return "", fmt.Errorf("compile SQLite CreateModel %q: fields are empty", model.DBTable)
	}
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", fmt.Errorf("compile SQLite CreateModel table: %w", err)
	}
	columns := make([]string, len(model.Fields))
	for index, field := range model.Fields {
		columns[index], err = compileMigrationColumn(field)
		if err != nil {
			return "", fmt.Errorf("compile SQLite CreateModel %q field %d: %w", model.DBTable, index, err)
		}
	}
	return "CREATE TABLE " + table + " (" + strings.Join(columns, ", ") + ")", nil
}

func compileMigrationDeleteModel(model ir.Model) (string, error) {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", fmt.Errorf("compile SQLite DeleteModel table: %w", err)
	}
	return "DROP TABLE " + table, nil
}

func compileMigrationAddField(model ir.Model, field ir.Field) (string, error) {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", fmt.Errorf("compile SQLite AddField table: %w", err)
	}
	column, err := compileMigrationColumn(field)
	if err != nil {
		return "", fmt.Errorf("compile SQLite AddField %q: %w", model.DBTable, err)
	}
	return "ALTER TABLE " + table + " ADD COLUMN " + column, nil
}

func compileMigrationRemoveField(model ir.Model, field ir.Field) (string, error) {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", fmt.Errorf("compile SQLite RemoveField table: %w", err)
	}
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", fmt.Errorf("compile SQLite RemoveField column: %w", err)
	}
	return "ALTER TABLE " + table + " DROP COLUMN " + column, nil
}

func compileMigrationColumn(field ir.Field) (string, error) {
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", fmt.Errorf("column identifier: %w", err)
	}
	var declaration string
	switch field.Kind {
	case ir.FieldAuto:
		if !field.PrimaryKey || field.Nullable {
			return "", fmt.Errorf("AutoField must be a non-null primary key")
		}
		// Django's SQLite AutoField uses AUTOINCREMENT so deleting the current
		// maximum key cannot make a later insert reuse that identifier.
		declaration = "INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT"
	case ir.FieldChar:
		if field.MaxLength <= 0 {
			return "", fmt.Errorf("CharField max length must be positive")
		}
		declaration = fmt.Sprintf("VARCHAR(%d)", field.MaxLength)
		if field.Nullable {
			declaration += " NULL"
		} else {
			declaration += " NOT NULL"
		}
	case ir.FieldBoolean:
		if field.Nullable {
			return "", fmt.Errorf("nullable BooleanField is unsupported")
		}
		declaration = "BOOLEAN NOT NULL"
	default:
		return "", fmt.Errorf("unsupported field kind %q", field.Kind)
	}
	return column + " " + declaration, nil
}
