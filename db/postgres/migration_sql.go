package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

const postgresConstraintDigestBytes = 24

// migrationSQLExecutor is deliberately narrower than *sql.Tx. The loaded
// lifecycle owns the transaction and connection; the schema slice only needs
// statement and catalog access through that exact transaction.
type migrationSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var _ migrationSQLExecutor = (*sql.Tx)(nil)

func compilePostgresMigrationCreateModel(
	namespace string,
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
) (string, error) {
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	if len(model.Fields) == 0 {
		return "", errors.New("PostgreSQL migration CreateModel fields are empty")
	}
	parts := make([]string, 0, len(model.Fields)+len(targets))
	targetIndex := 0
	for fieldIndex := range model.Fields {
		field := model.Fields[fieldIndex]
		column, err := compilePostgresMigrationColumnForTable(namespace, model.DBTable, field)
		if err != nil {
			return "", fmt.Errorf("compile PostgreSQL CreateModel %q field %d: %w", model.DBTable, fieldIndex, err)
		}
		parts = append(parts, column)
		if field.Kind != ir.FieldForeignKey {
			continue
		}
		if targetIndex >= len(targets) || !migrationFieldsEqual(targets[targetIndex].SourceField, field) {
			return "", errors.New("PostgreSQL CreateModel target metadata is not in exact relation field order")
		}
		constraint, err := compilePostgresMigrationForeignKey(namespace, model.DBTable, targets[targetIndex])
		if err != nil {
			return "", err
		}
		parts = append(parts, constraint)
		targetIndex++
	}
	if targetIndex != len(targets) {
		return "", fmt.Errorf("PostgreSQL CreateModel has %d unused relation targets", len(targets)-targetIndex)
	}
	primaryKey, err := postgresMigrationPrimaryKey(model)
	if err != nil {
		return "", err
	}
	primaryKeyName, err := postgresPrimaryKeyConstraintName(model.DBTable)
	if err != nil {
		return "", err
	}
	quotedName, err := quoteIdentifier(primaryKeyName)
	if err != nil {
		return "", err
	}
	quotedColumn, err := quoteIdentifier(primaryKey.Column)
	if err != nil {
		return "", err
	}
	parts = append(parts, "CONSTRAINT "+quotedName+" PRIMARY KEY ("+quotedColumn+")")
	return "CREATE TABLE " + table + " (" + strings.Join(parts, ", ") + ")", nil
}

func compilePostgresMigrationDeleteModel(namespace string, model ir.Model) (string, error) {
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	return "DROP TABLE " + table + " RESTRICT", nil
}

func compilePostgresMigrationAddField(
	namespace string,
	model ir.Model,
	field ir.Field,
	target *migrationbackend.MigrationTarget,
) (string, error) {
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	column, err := compilePostgresMigrationColumnForTable(namespace, model.DBTable, field)
	if err != nil {
		return "", err
	}
	statement := "ALTER TABLE " + table + " ADD COLUMN " + column
	if field.Kind != ir.FieldForeignKey {
		if target != nil {
			return "", errors.New("scalar PostgreSQL AddField carries relation target metadata")
		}
		return statement, nil
	}
	if target == nil || !migrationFieldsEqual(target.SourceField, field) {
		return "", errors.New("PostgreSQL ForeignKey AddField lacks its exact target metadata")
	}
	constraint, err := compilePostgresMigrationForeignKey(namespace, model.DBTable, *target)
	if err != nil {
		return "", err
	}
	return statement + ", ADD " + constraint, nil
}

func postgresMigrationAddFieldTarget(
	operation migrationbackend.MigrationOperation,
	field ir.Field,
) (*migrationbackend.MigrationTarget, error) {
	if field.Kind != ir.FieldForeignKey {
		return nil, nil
	}
	for index := range operation.Targets {
		if migrationFieldsEqual(operation.Targets[index].SourceField, field) {
			target := operation.Targets[index]
			return &target, nil
		}
	}
	return nil, postgresMigrationIntentIntegrity("sealed PostgreSQL ForeignKey AddField target is missing", nil)
}

func compilePostgresMigrationRemoveField(
	namespace string,
	model ir.Model,
	field ir.Field,
) (string, error) {
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", err
	}
	statement := "ALTER TABLE " + table
	if field.Kind == ir.FieldForeignKey {
		constraintName, err := postgresForeignKeyConstraintName(model.DBTable, field.Column)
		if err != nil {
			return "", err
		}
		constraint, err := quoteIdentifier(constraintName)
		if err != nil {
			return "", err
		}
		statement += " DROP CONSTRAINT " + constraint + ","
	}
	return statement + " DROP COLUMN " + column + " RESTRICT", nil
}

func compilePostgresMigrationColumn(field ir.Field) (string, error) {
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", err
	}
	var declaration string
	switch field.Kind {
	case ir.FieldAuto:
		if !field.PrimaryKey || field.Nullable || field.Default != nil || field.MaxLength != 0 || field.Relation != nil {
			return "", errors.New("AutoField must be an exact non-null generated primary key")
		}
		declaration = "BIGINT GENERATED BY DEFAULT AS IDENTITY NOT NULL"
	case ir.FieldChar:
		if field.PrimaryKey || field.MaxLength <= 0 || field.Relation != nil {
			return "", errors.New("CharField has an invalid PostgreSQL migration shape")
		}
		declaration = fmt.Sprintf("VARCHAR(%d)", field.MaxLength)
		if field.Nullable {
			declaration += " NULL"
		} else {
			declaration += " NOT NULL"
		}
	case ir.FieldBoolean:
		if field.PrimaryKey || field.Nullable || field.MaxLength != 0 || field.Relation != nil {
			return "", errors.New("BooleanField has an invalid PostgreSQL migration shape")
		}
		declaration = "BOOLEAN NOT NULL"
	case ir.FieldForeignKey:
		if field.PrimaryKey || field.MaxLength != 0 || field.Default != nil || field.Relation == nil {
			return "", errors.New("ForeignKey has an invalid PostgreSQL migration shape")
		}
		declaration = "BIGINT"
		if field.Nullable {
			declaration += " NULL"
		} else {
			declaration += " NOT NULL"
		}
	default:
		return "", fmt.Errorf("unsupported PostgreSQL migration field kind %q", field.Kind)
	}
	return column + " " + declaration, nil
}

func compilePostgresMigrationColumnForTable(namespace, table string, field ir.Field) (string, error) {
	if field.Kind != ir.FieldAuto {
		return compilePostgresMigrationColumn(field)
	}
	if !field.PrimaryKey || field.Nullable || field.Default != nil || field.MaxLength != 0 || field.Relation != nil {
		return "", errors.New("AutoField must be an exact non-null generated primary key")
	}
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", err
	}
	sequenceName, err := postgresIdentitySequenceName(table, field.Column)
	if err != nil {
		return "", err
	}
	sequence, err := quoteTable(namespace, sequenceName)
	if err != nil {
		return "", err
	}
	return column + " BIGINT GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME " + sequence +
		" START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 9223372036854775807 CACHE 1 NO CYCLE) NOT NULL", nil
}

func compilePostgresMigrationForeignKey(
	namespace,
	sourceTable string,
	target migrationbackend.MigrationTarget,
) (string, error) {
	if target.SourceField.Kind != ir.FieldForeignKey || target.SourceField.Relation == nil {
		return "", errors.New("PostgreSQL migration foreign key target has no relation source")
	}
	constraintName, err := postgresForeignKeyConstraintName(sourceTable, target.SourceField.Column)
	if err != nil {
		return "", err
	}
	constraint, err := quoteIdentifier(constraintName)
	if err != nil {
		return "", err
	}
	sourceColumn, err := quoteIdentifier(target.SourceField.Column)
	if err != nil {
		return "", err
	}
	targetTable, err := quoteTable(namespace, target.TargetModel.DBTable)
	if err != nil {
		return "", err
	}
	targetColumn, err := quoteIdentifier(target.TargetKey.Column)
	if err != nil {
		return "", err
	}
	return "CONSTRAINT " + constraint + " FOREIGN KEY (" + sourceColumn + ") REFERENCES " +
		targetTable + " (" + targetColumn + ") ON UPDATE NO ACTION ON DELETE NO ACTION NOT DEFERRABLE", nil
}

// postgresForeignKeyConstraintName uses a domain-separated SHA-256 digest of
// the complete normalized source table and column. The fixed 192-bit prefix
// keeps every name at 56 ASCII bytes, below PostgreSQL's 63-byte limit without
// relying on server-side truncation. Intent validation still detects a digest
// collision and fails before I/O.
func postgresForeignKeyConstraintName(table, column string) (string, error) {
	if err := validateIdentifier(table); err != nil {
		return "", fmt.Errorf("foreign key source table: %w", err)
	}
	if err := validateIdentifier(column); err != nil {
		return "", fmt.Errorf("foreign key source column: %w", err)
	}
	hash := sha256.New()
	for _, value := range []string{"godj/postgres/foreign-key/v1", table, column} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	digest := hash.Sum(nil)
	name := "godj_fk_" + hex.EncodeToString(digest[:postgresConstraintDigestBytes])
	if len(name) > postgresIdentifierMaxBytes {
		return "", errors.New("derived PostgreSQL foreign key constraint name exceeds the identifier limit")
	}
	return name, nil
}

func postgresPrimaryKeyConstraintName(table string) (string, error) {
	if err := validateIdentifier(table); err != nil {
		return "", fmt.Errorf("primary key source table: %w", err)
	}
	hash := sha256.New()
	for _, value := range []string{"godj/postgres/primary-key/v1", table} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	digest := hash.Sum(nil)
	name := "godj_pk_" + hex.EncodeToString(digest[:postgresConstraintDigestBytes])
	if len(name) > postgresIdentifierMaxBytes {
		return "", errors.New("derived PostgreSQL primary key constraint name exceeds the identifier limit")
	}
	return name, nil
}

func postgresIdentitySequenceName(table, column string) (string, error) {
	if err := validateIdentifier(table); err != nil {
		return "", fmt.Errorf("identity sequence source table: %w", err)
	}
	if err := validateIdentifier(column); err != nil {
		return "", fmt.Errorf("identity sequence source column: %w", err)
	}
	hash := sha256.New()
	for _, value := range []string{"godj/postgres/identity-sequence/v1", table, column} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	digest := hash.Sum(nil)
	name := "godj_seq_" + hex.EncodeToString(digest[:postgresConstraintDigestBytes])
	if len(name) > postgresIdentifierMaxBytes {
		return "", errors.New("derived PostgreSQL identity sequence name exceeds the identifier limit")
	}
	return name, nil
}

func postgresMigrationPrimaryKey(model ir.Model) (ir.Field, error) {
	var primaryKey ir.Field
	count := 0
	for index := range model.Fields {
		if model.Fields[index].PrimaryKey {
			primaryKey = model.Fields[index]
			count++
		}
	}
	if count != 1 || primaryKey.Kind != ir.FieldAuto || primaryKey.Nullable {
		return ir.Field{}, fmt.Errorf("PostgreSQL migration model %q requires exactly one non-null AutoField primary key", model.DBTable)
	}
	return primaryKey, nil
}

func postgresMigrationCapability(detail string, cause error) error {
	return migrationbackend.NewCapabilityError("postgres_schema_migration", detail, cause)
}
