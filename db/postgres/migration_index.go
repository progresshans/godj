package postgres

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/progresshans/godj/schema/ir"
)

func postgresColumnIndexName(table, column string) (string, error) {
	for _, name := range []string{table, column} {
		if err := validateIdentifier(name); err != nil {
			return "", err
		}
	}
	hash := sha256.New()
	for _, value := range []string{"godj/postgres/column-index/v1", table, column} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "godj_ix_" + hex.EncodeToString(hash.Sum(nil)[:postgresConstraintDigestBytes]), nil
}

func compilePostgresColumnIndex(namespace string, model ir.Model, field ir.Field, add bool) (string, error) {
	name, err := postgresColumnIndexName(model.DBTable, field.Column)
	if err != nil {
		return "", err
	}
	if !add {
		index, err := quoteTable(namespace, name)
		if err != nil {
			return "", err
		}
		return "DROP INDEX " + index + " RESTRICT", nil
	}
	index, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", err
	}
	// PostgreSQL creates the index in the explicit table namespace. Default
	// btree operator classes and the declared column collation are intentional.
	return "CREATE INDEX " + index + " ON " + table + " (" + column + ")", nil
}

func appendPostgresColumnIndex(statements []string, namespace string, model ir.Model, field ir.Field) ([]string, error) {
	if !field.HasColumnIndex() {
		return statements, nil
	}
	statement, err := compilePostgresColumnIndex(namespace, model, field, true)
	if err != nil {
		return nil, err
	}
	return append(statements, statement), nil
}

func compilePostgresFieldIndexes(namespace string, model ir.Model, before, after ir.Field) ([]string, error) {
	var statements []string
	appendSQL := func(statement string, err error) error {
		if err == nil {
			statements = append(statements, statement)
		}
		return err
	}
	if before.HasColumnIndex() && !after.HasColumnIndex() {
		if err := appendSQL(compilePostgresColumnIndex(namespace, model, before, false)); err != nil {
			return nil, err
		}
	}
	if before.Unique != after.Unique {
		if err := appendSQL(compilePostgresUniqueAlter(namespace, model, after)); err != nil {
			return nil, err
		}
	}
	if !before.HasColumnIndex() && after.HasColumnIndex() {
		if err := appendSQL(compilePostgresColumnIndex(namespace, model, after, true)); err != nil {
			return nil, err
		}
	}
	return statements, nil
}

func exactPostgresColumnIndex(index postgresMigrationIndexCatalog, name string, field ir.Field, attributeNumber int) bool {
	if !field.HasColumnIndex() || attributeNumber <= 0 || index.oid <= 0 || index.name != name ||
		index.primary || index.unique || !index.immediate || !index.valid || !index.ready || !index.live || !index.vectorsExact ||
		index.keyCount != 1 || index.totalCount != 1 || len(index.keys) != 1 || index.hasPredicate || index.hasExpressions ||
		index.nullsNotDistinct || index.exclusion || index.accessMethod != "btree" || index.options != 0 {
		return false
	}
	key := index.keys[0]
	opclass := postgresBtreeOperatorClass(field.Kind)
	return opclass != "" && key.attributeNumber == attributeNumber && key.columnOptions == 0 && key.columnCollation &&
		key.operatorClassSchema == "pg_catalog" && key.operatorClassName == opclass && key.operatorClassDefault && key.operatorClassMethod
}
