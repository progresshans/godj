package sqlite

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

var _ backend.FieldRenameEditor = (*sqliteRevisionFencedTransaction)(nil)

func (tx *sqliteRevisionFencedTransaction) RenameField(ctx context.Context, model ir.Model, before, after ir.Field) error {
	return tx.execute(ctx, "rename SQLite field", func(executor migrationSQLExecutor) error {
		state := tx.relation
		if tx.recorderCalled || state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected RenameField after the sealed cursor")
		}
		if err := verifySQLiteRelationIntentSeal(&state.seal); err != nil {
			return err
		}
		op := state.seal.intent.Operations[state.cursor]
		old, next, err := op.RenamedField(tx.transition.Migration.App)
		if err != nil || !model.Equal(op.Before) || !old.Equal(before) || !next.Equal(after) {
			return relationIntentIntegrity("RenameField arguments differ from the sealed transition")
		}
		statements, err := compileSQLiteFieldRename(model, old, next)
		if err != nil {
			return err
		}
		if len(statements) != 0 {
			if err := assertSQLiteFieldRenameDependencies(ctx, executor, op, tx.transition.Migration.App, old); err != nil {
				return err
			}
			if err := executeSQLiteMigrationStatements(ctx, executor, statements); err != nil {
				return fmt.Errorf("rename SQLite field: %w", err)
			}
		}
		state.cursor++
		return tx.completeRelationOperationIfLast(ctx, executor)
	})
}

func compileSQLiteFieldRename(model ir.Model, before, after ir.Field) ([]string, error) {
	if before.Column == after.Column {
		return []string{}, nil
	}
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return nil, err
	}
	old, err := quoteIdentifier(before.Column)
	if err != nil {
		return nil, err
	}
	next, err := quoteIdentifier(after.Column)
	if err != nil {
		return nil, err
	}
	statements := []string{`ALTER TABLE "main".` + table + " RENAME COLUMN " + old + " TO " + next}
	// Native column rename keeps the old managed index name. Install the
	// successor first so a unique column remains enforced throughout.
	var create, drop string
	switch {
	case before.Unique:
		create, err = compileSQLiteUniqueAlter(model, after)
		if err != nil {
			return nil, err
		}
		removed := before
		removed.Unique = false
		drop, err = compileSQLiteUniqueAlter(model, removed)
	case before.HasColumnIndex():
		create, err = compileSQLiteColumnIndex(model, after, true)
		if err != nil {
			return nil, err
		}
		drop, err = compileSQLiteColumnIndex(model, before, false)
	default:
		return statements, nil
	}
	if err != nil {
		return nil, err
	}
	return append(statements, create, drop), nil
}

func assertSQLiteFieldRenameDependencies(ctx context.Context, exec migrationSQLExecutor, op backend.MigrationOperation, app string, before ir.Field) error {
	catalog, err := loadSQLiteRelationCatalog(ctx, exec)
	if err != nil {
		return err
	}
	physical, err := buildSQLiteRelationPhysicalGraph(catalog)
	if err != nil {
		return err
	}
	allowed := op.RenameReferences(app, before)
	inspected := 0
	table := sqliteRelationIdentifierKey(op.Before.DBTable)
	for owner := range physical.incoming[table] {
		keys, err := readSQLiteRelationForeignKeys(ctx, exec, owner, -1)
		if err != nil {
			return err
		}
		inspected += len(keys)
		if inspected > sqliteRelationMaxNodes {
			return relationPhysicalDrift("foreign keys exceed the field rename inspection limit")
		}
		for _, key := range keys {
			if sqliteRelationIdentifierKey(key.table) != table || sqliteRelationIdentifierKey(key.to) != before.Column {
				continue
			}
			if !allowed[owner][key.from] {
				return relationPhysicalDrift("foreign key on %q.%q is outside the field rename authority", owner, key.from)
			}
			delete(allowed[owner], key.from)
		}
	}
	for _, fields := range allowed {
		if len(fields) != 0 {
			return relationPhysicalDrift("declared foreign key for field rename is missing")
		}
	}
	return nil
}
