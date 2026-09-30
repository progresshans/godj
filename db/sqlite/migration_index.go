package sqlite

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/progresshans/godj/schema/ir"
)

func sqliteColumnIndexName(table, column string) (string, error) {
	for _, name := range []string{table, column} {
		if _, err := quoteIdentifier(name); err != nil {
			return "", err
		}
	}
	hash := sha256.New()
	for _, value := range []string{"godj/sqlite/column-index/v1", table, column} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "godj_ix_" + hex.EncodeToString(hash.Sum(nil)[:24]), nil
}

func compileSQLiteColumnIndex(model ir.Model, field ir.Field, add bool) (string, error) {
	name, err := sqliteColumnIndexName(model.DBTable, field.Column)
	if err != nil {
		return "", err
	}
	index, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	if !add {
		return `DROP INDEX "main".` + index, nil
	}
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", err
	}
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", err
	}
	return `CREATE INDEX "main".` + index + " ON " + table + " (" + column + ")", nil
}

// A uniqueness change replaces an ordinary index atomically. No statement
// adopts or drops an object until the sealed before-catalog has been checked.
func compileSQLiteFieldIndexes(model ir.Model, before, after ir.Field) ([]string, error) {
	var statements []string
	appendSQL := func(statement string, err error) error {
		if err == nil {
			statements = append(statements, statement)
		}
		return err
	}
	if before.HasColumnIndex() && !after.HasColumnIndex() {
		if err := appendSQL(compileSQLiteColumnIndex(model, before, false)); err != nil {
			return nil, err
		}
	}
	if before.Unique != after.Unique {
		if err := appendSQL(compileSQLiteUniqueAlter(model, after)); err != nil {
			return nil, err
		}
	}
	if !before.HasColumnIndex() && after.HasColumnIndex() {
		if err := appendSQL(compileSQLiteColumnIndex(model, after, true)); err != nil {
			return nil, err
		}
	}
	return statements, nil
}
