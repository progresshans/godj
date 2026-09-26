package postgres

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/internal/identifiers"
	migrationbackend "github.com/progresshans/godj/migrations/backend"

	"github.com/progresshans/godj/schema/ir"
)

// Unique constraints and their backing indexes share a deterministic name.
// Length framing and the separate domain keep table/column pairs unambiguous.
func postgresUniqueConstraintName(table, column string) (string, error) {
	if err := validateIdentifier(table); err != nil {
		return "", fmt.Errorf("unique source table: %w", err)
	}
	if err := validateIdentifier(column); err != nil {
		return "", fmt.Errorf("unique source column: %w", err)
	}
	hash := sha256.New()
	for _, value := range []string{"godj/postgres/unique/v1", table, column} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "godj_uq_" + hex.EncodeToString(hash.Sum(nil)[:postgresConstraintDigestBytes]), nil
}

func postgresNamedUniqueConstraintName(table, name string) (string, error) {
	if err := validateIdentifier(table); err != nil {
		return "", err
	}
	// Logical names are hashed, never truncated into a PostgreSQL identifier.
	if !identifiers.SQL(name) {
		return "", fmt.Errorf("invalid logical constraint name %q", name)
	}
	hash := sha256.New()
	for _, value := range []string{"godj/postgres/model-unique/v1", table, name} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "godj_uq_" + hex.EncodeToString(hash.Sum(nil)[:postgresConstraintDigestBytes]), nil
}

func compilePostgresNamedUniqueConstraint(model ir.Model, constraint ir.UniqueConstraint) (string, error) {
	name, err := postgresNamedUniqueConstraintName(model.DBTable, constraint.Name)
	if err != nil {
		return "", err
	}
	quoted, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	fields, err := migrationbackend.UniqueConstraintFields(model, constraint)
	if err != nil {
		return "", err
	}
	columns := make([]string, len(fields))
	for index, field := range fields {
		columns[index], err = quoteIdentifier(field.Column)
		if err != nil {
			return "", err
		}
	}
	return "CONSTRAINT " + quoted + " UNIQUE (" + strings.Join(columns, ", ") + ")", nil
}

func compilePostgresNamedUniqueAlter(namespace string, model ir.Model, constraint ir.UniqueConstraint, add bool) (string, error) {
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	if add {
		clause, err := compilePostgresNamedUniqueConstraint(model, constraint)
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + table + " ADD " + clause, nil
	}
	if _, err := migrationbackend.UniqueConstraintFields(model, constraint); err != nil {
		return "", err
	}
	name, err := postgresNamedUniqueConstraintName(model.DBTable, constraint.Name)
	if err != nil {
		return "", err
	}
	quoted, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + table + " DROP CONSTRAINT " + quoted + " RESTRICT", nil
}

func compilePostgresUniqueConstraint(table string, field ir.Field) (string, error) {
	if !field.Unique || field.PrimaryKey {
		return "", errors.New("PostgreSQL unique constraint requires a declared non-primary column")
	}
	name, err := postgresUniqueConstraintName(table, field.Column)
	if err != nil {
		return "", err
	}
	constraint, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", err
	}
	// PostgreSQL's default is immediate enforcement with distinct SQL NULLs.
	return "CONSTRAINT " + constraint + " UNIQUE (" + column + ")", nil
}

func compilePostgresUniqueAlter(namespace string, model ir.Model, after ir.Field) (string, error) {
	if after.PrimaryKey {
		return "", errors.New("PostgreSQL primary key cannot be altered as column uniqueness")
	}
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	if after.Unique {
		constraint, err := compilePostgresUniqueConstraint(model.DBTable, after)
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + table + " ADD " + constraint, nil
	}
	name, err := postgresUniqueConstraintName(model.DBTable, after.Column)
	if err != nil {
		return "", err
	}
	constraint, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + table + " DROP CONSTRAINT " + constraint + " RESTRICT", nil
}

func postgresBtreeOperatorClass(kind ir.FieldKind) string {
	switch kind {
	case ir.FieldAuto, ir.FieldInteger, ir.FieldForeignKey:
		return "int8_ops"
	case ir.FieldChar, ir.FieldText:
		return "text_ops"
	case ir.FieldBoolean:
		return "bool_ops"
	case ir.FieldFloat:
		return "float8_ops"
	case ir.FieldDecimal:
		return "numeric_ops"
	case ir.FieldUUID:
		return "uuid_ops"
	case ir.FieldJSON:
		return "jsonb_ops"
	case ir.FieldDate:
		return "date_ops"
	case ir.FieldDateTime:
		return "timestamptz_ops"
	case ir.FieldTime:
		return "time_ops"
	case ir.FieldDuration:
		return "interval_ops"
	default:
		return ""
	}
}

func exactPostgresConstraintIndex(index postgresMigrationIndexCatalog, constraint postgresMigrationConstraintCatalog, fields []ir.Field) bool {
	if len(fields) == 0 || len(index.keys) != len(fields) || len(constraint.sourceAttributes) != len(fields) ||
		index.oid != constraint.indexOID || index.name != constraint.name || index.primary != (constraint.kind == "p") ||
		!index.unique || !index.immediate || !index.valid || !index.ready || !index.live || !index.vectorsExact ||
		index.keyCount != len(fields) || index.totalCount != len(fields) || index.hasPredicate || index.hasExpressions ||
		index.nullsNotDistinct || index.exclusion || index.accessMethod != "btree" || index.options != 0 {
		return false
	}
	for position, field := range fields {
		key := index.keys[position]
		opclass := postgresBtreeOperatorClass(field.Kind)
		if opclass == "" || key.attributeNumber != constraint.sourceAttributes[position] || key.attributeNumber <= 0 || key.columnOptions != 0 ||
			!key.columnCollation || key.operatorClassSchema != "pg_catalog" || key.operatorClassName != opclass || !key.operatorClassDefault || !key.operatorClassMethod {
			return false
		}
	}
	return true
}
