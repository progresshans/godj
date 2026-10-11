package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/internal/identifiers"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// Index names have a separate domain and length-framed table/column identity.
// They remain stable through a table remake and a uniqueness-only reversal.
func sqliteUniqueIndexName(table, column string) (string, error) {
	for _, identifier := range []string{table, column} {
		if _, err := quoteIdentifier(identifier); err != nil {
			return "", fmt.Errorf("SQLite unique index identifier: %w", err)
		}
	}
	hash := sha256.New()
	for _, value := range []string{"godj/sqlite/unique/v1", table, column} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	return "godj_uq_" + hex.EncodeToString(hash.Sum(nil)[:24]), nil
}

func sqliteNamedUniqueIndexName(table, name string) (string, error) {
	if _, err := quoteIdentifier(table); err != nil {
		return "", err
	}
	if !identifiers.SQL(name) {
		return "", fmt.Errorf("invalid logical unique constraint name %q", name)
	}
	hash := sha256.New()
	for _, value := range []string{"godj/sqlite/model-unique/v1", table, name} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	return "godj_uq_" + hex.EncodeToString(hash.Sum(nil)[:24]), nil
}

func compileSQLiteNamedUniqueAlter(model ir.Model, constraint ir.UniqueConstraint, add bool) (string, error) {
	name, err := sqliteNamedUniqueIndexName(model.DBTable, constraint.Name)
	if err != nil {
		return "", err
	}
	index, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	fields, err := migrationbackend.UniqueConstraintFields(model, constraint)
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
	columns := make([]string, len(fields))
	for position, field := range fields {
		columns[position], err = quoteIdentifier(field.Column)
		if err != nil {
			return "", err
		}
	}
	return `CREATE UNIQUE INDEX "main".` + index + " ON " + table + " (" + strings.Join(columns, ", ") + ")", nil
}

func compileSQLiteUniqueAlter(model ir.Model, after ir.Field) (string, error) {
	if after.PrimaryKey {
		return "", errors.New("SQLite primary key cannot be altered as column uniqueness")
	}
	name, err := sqliteUniqueIndexName(model.DBTable, after.Column)
	if err != nil {
		return "", err
	}
	index, err := quoteIdentifier(name)
	if err != nil {
		return "", err
	}
	if !after.Unique {
		return `DROP INDEX "main".` + index, nil
	}
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", err
	}
	column, err := quoteIdentifier(after.Column)
	if err != nil {
		return "", err
	}
	// SQLite resolves the table in the index's explicit schema. The column's
	// canonical declaration supplies BINARY collation; SQL NULLs are distinct.
	return `CREATE UNIQUE INDEX "main".` + index + " ON " + table + " (" + column + ")", nil
}

func compileSQLiteIndexes(model ir.Model) ([]string, error) {
	var statements []string
	for _, field := range model.Fields {
		indexes, err := compileSQLiteFieldIndexes(model, ir.Field{}, field)
		if err != nil {
			return nil, err
		}
		statements = append(statements, indexes...)
	}
	for _, constraint := range model.UniqueConstraints {
		statement, err := compileSQLiteNamedUniqueAlter(model, constraint, true)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}

func compileSQLiteCreateModelStatements(model ir.Model, targets []migrationbackend.MigrationTarget) ([]string, error) {
	statement, err := compileSQLiteRelationCreateModel(model, targets)
	if err != nil {
		return nil, err
	}
	indexes, err := compileSQLiteIndexes(model)
	if err != nil {
		return nil, err
	}
	return append([]string{statement}, indexes...), nil
}

func compileSQLiteAddFieldStatements(model ir.Model, field ir.Field, targets []migrationbackend.MigrationTarget) ([]string, error) {
	var statement string
	var err error
	if field.Kind == ir.FieldForeignKey {
		statement, err = compileSQLiteRelationAddField(model, field, targets)
	} else {
		statement, err = compileMigrationAddField(model, field)
	}
	if err != nil {
		return nil, err
	}
	statements := []string{statement}
	indexes, err := compileSQLiteFieldIndexes(model, ir.Field{}, field)
	if err != nil {
		return nil, err
	}
	statements = append(statements, indexes...)
	return statements, nil
}

// Only the revision-fenced owner executes these streams. Its failure state
// prevents commit after any body fails; rollback owns all prior statements.
func executeSQLiteMigrationStatements(ctx context.Context, executor migrationSQLExecutor, statements []string) error {
	for index, statement := range statements {
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			if index > 0 {
				return newSQLiteMigrationDDLExecutionError("execute remaining SQLite migration SQL", err)
			}
			return err
		}
	}
	return nil
}

func sqliteModelHasIndexes(model ir.Model) bool {
	if len(model.UniqueConstraints) != 0 {
		return true
	}
	for _, field := range model.Fields {
		if field.Unique || field.DBIndex {
			return true
		}
	}
	return false
}

type sqliteIndexOwner struct {
	name, table, column, constraint string
}

func sqliteModelIndexOwners(model ir.Model) ([]sqliteIndexOwner, error) {
	var owners []sqliteIndexOwner
	for _, field := range model.Fields {
		if !field.Unique && !field.HasColumnIndex() {
			continue
		}
		var name string
		var err error
		if field.Unique {
			name, err = sqliteUniqueIndexName(model.DBTable, field.Column)
		} else {
			name, err = sqliteColumnIndexName(model.DBTable, field.Column)
		}
		if err != nil {
			return nil, err
		}
		owners = append(owners, sqliteIndexOwner{name: name, table: model.DBTable, column: field.Column})
	}
	for _, constraint := range model.UniqueConstraints {
		name, err := sqliteNamedUniqueIndexName(model.DBTable, constraint.Name)
		if err != nil {
			return nil, err
		}
		owners = append(owners, sqliteIndexOwner{name: name, table: model.DBTable, constraint: constraint.Name})
	}
	return owners, nil
}

// This union reserves intermediate names as well as boundary names. Exact
// existence and shape still belong to the initial/final model inspections.
func sqliteIndexOwners(seal *sqliteRelationIntentSeal) (map[string]sqliteIndexOwner, error) {
	owners := make(map[string]sqliteIndexOwner)
	add := func(model ir.Model) error {
		declared, err := sqliteModelIndexOwners(model)
		if err != nil {
			return err
		}
		for _, owner := range declared {
			key := sqliteRelationIdentifierKey(owner.name)
			if previous, exists := owners[key]; exists && previous != owner {
				return relationIntentIntegrity("SQLite index has multiple declared owners")
			}
			owners[key] = owner
		}
		return nil
	}
	for position, operation := range seal.intent.Operations {
		for _, model := range []ir.Model{operation.Before, operation.After} {
			if err := add(model); err != nil {
				return nil, err
			}
		}
		graph, exists := seal.graphPlan.Operation(position)
		if !exists {
			return nil, relationIntentIntegrity("index namespace has no sealed operation graph")
		}
		for _, snapshot := range graph.Models() {
			if err := add(snapshot.Model); err != nil {
				return nil, err
			}
		}
	}
	for _, change := range seal.graphPlan.StorageChanges() {
		for _, model := range []ir.Model{change.Before, change.After} {
			if err := add(model); err != nil {
				return nil, err
			}
		}
	}
	for _, snapshot := range append(seal.graphPlan.InitialModels(), seal.graphPlan.FinalModels()...) {
		if err := add(snapshot.Model); err != nil {
			return nil, err
		}
	}
	return owners, nil
}

type sqliteIndexColumn struct {
	column   string
	position int
}

func assertSQLiteIndexes(ctx context.Context, executor migrationSQLExecutor, model ir.Model, layout []ir.Field) (resultErr error) {
	// Rowid is the primary-key B-tree. Every separate index must belong to an
	// exact declared column or model constraint; unmanaged indexes remain drift.
	expected := make(map[string][]sqliteIndexColumn)
	uniqueIndexes := make(map[string]bool)
	order := make([]string, 0)
	columns := make(map[string]sqliteIndexColumn, len(layout))
	for position, field := range layout {
		columns[field.Column] = sqliteIndexColumn{column: field.Column, position: position}
	}
	add := func(name string, fields []ir.Field, unique bool) error {
		if _, exists := expected[name]; exists {
			return relationIntentIntegrity("multiple declarations own index %q", name)
		}
		keys := make([]sqliteIndexColumn, len(fields))
		for position, field := range fields {
			column, exists := columns[field.Column]
			if !exists {
				return relationPhysicalDrift("index %q is missing column %q", name, field.Column)
			}
			keys[position] = column
		}
		expected[name] = keys
		uniqueIndexes[name] = unique
		order = append(order, name)
		return nil
	}
	for _, field := range model.Fields {
		if !field.Unique && !field.HasColumnIndex() {
			continue
		}
		var name string
		var err error
		if field.Unique {
			name, err = sqliteUniqueIndexName(model.DBTable, field.Column)
		} else {
			name, err = sqliteColumnIndexName(model.DBTable, field.Column)
		}
		if err != nil {
			return err
		}
		if err := add(name, []ir.Field{field}, field.Unique); err != nil {
			return err
		}
	}
	for _, constraint := range model.UniqueConstraints {
		name, err := sqliteNamedUniqueIndexName(model.DBTable, constraint.Name)
		if err != nil {
			return err
		}
		fields, err := migrationbackend.UniqueConstraintFields(model, constraint)
		if err != nil {
			return err
		}
		if err := add(name, fields, true); err != nil {
			return err
		}
	}
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return err
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA main.index_list(`+table+`)`)
	if err != nil {
		return classifyRevisionIO("list declared indexes", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIO("close index list", rows.Close()))
	}()
	seen := make(map[string]struct{}, len(expected))
	for rows.Next() {
		if len(seen) >= len(expected) {
			return relationPhysicalDrift("table %q has undeclared indexes", model.DBTable)
		}
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			return classifyRevisionIO("scan index list", err)
		}
		_, declared := expected[name]
		_, duplicate := seen[name]
		wantUnique := 0
		if uniqueIndexes[name] {
			wantUnique = 1
		}
		if !declared || duplicate || sequence < 0 || unique != wantUnique || origin != "c" || partial != 0 {
			return relationPhysicalDrift("table %q index %q differs from its declared shape", model.DBTable, name)
		}
		seen[name] = struct{}{}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return classifyRevisionIO("read index list", err)
	}
	if len(seen) != len(expected) {
		return relationPhysicalDrift("table %q is missing declared indexes", model.DBTable)
	}
	for _, name := range order {
		if err := assertSQLiteIndexColumns(ctx, executor, name, expected[name]); err != nil {
			return err
		}
	}
	return nil
}

func assertSQLiteIndexColumns(ctx context.Context, executor migrationSQLExecutor, name string, columns []sqliteIndexColumn) (resultErr error) {
	index, err := quoteIdentifier(name)
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		return relationIntentIntegrity("index %q has no declared key", name)
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA main.index_xinfo(`+index+`)`)
	if err != nil {
		return classifyRevisionIO("inspect index columns", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIO("close index columns", rows.Close()))
	}()
	count := 0
	for rows.Next() {
		var sequence, cid, descending, key int
		var field, collation sql.NullString
		if err := rows.Scan(&sequence, &cid, &field, &descending, &collation, &key); err != nil {
			return classifyRevisionIO("scan index columns", err)
		}
		if count > len(columns) || sequence != count || descending != 0 || !collation.Valid || collation.String != "BINARY" {
			return relationPhysicalDrift("index %q differs from ordered BINARY ASC keys and auxiliary rowid", name)
		}
		if count < len(columns) {
			want := columns[count]
			if cid != want.position || !field.Valid || field.String != want.column || key != 1 {
				return relationPhysicalDrift("index %q differs at key %d", name, count)
			}
		} else if cid != -1 || field.Valid || key != 0 {
			return relationPhysicalDrift("index %q has an invalid auxiliary rowid", name)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return classifyRevisionIO("read index columns", err)
	}
	if count != len(columns)+1 {
		return relationPhysicalDrift("index %q is missing declared keys or auxiliary rowid", name)
	}
	return nil
}
