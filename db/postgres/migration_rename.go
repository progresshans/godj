package postgres

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

var _ backend.FieldRenameEditor = (*postgresRevisionFencedTransaction)(nil)

func (tx *postgresRevisionFencedTransaction) RenameField(ctx context.Context, model ir.Model, before, after ir.Field) error {
	return tx.executeSchema(ctx, "rename PostgreSQL field", func(exec migrationSQLExecutor) error {
		op, err := tx.schema.postgresMigrationCurrentOperation(ctx, exec, backend.MigrationRenameField)
		if err != nil {
			return err
		}
		app := tx.transition.Migration.App
		old, next, err := op.RenamedField(app)
		if err != nil || !model.Equal(op.Before) || !before.Equal(old) || !after.Equal(next) {
			return postgresMigrationIntentIntegrity("RenameField arguments differ from their sealed transition", err)
		}
		statements, err := compilePostgresFieldRename(tx.schema.namespace, model, old, next)
		if err != nil {
			return err
		}
		if len(statements) != 0 {
			if err := assertPostgresFieldRenameDependencies(ctx, exec, tx.schema.namespace, op, app, old); err != nil {
				return err
			}
			for _, stmt := range statements {
				if _, err := exec.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("rename PostgreSQL field: %w", err)
				}
			}
		}
		tx.schema.cursor++
		return nil
	})
}

func compilePostgresFieldRename(namespace string, model ir.Model, before, after ir.Field) ([]string, error) {
	if before.Column == after.Column {
		return []string{}, nil
	}
	table, err := quoteTable(namespace, model.DBTable)
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
	sql := []string{"ALTER TABLE " + table + " RENAME COLUMN " + old + " TO " + next}
	rename := func(kind string, derive func(string, string) (string, error)) error {
		oldName, err := derive(model.DBTable, before.Column)
		if err != nil {
			return err
		}
		newName, err := derive(model.DBTable, after.Column)
		if err != nil {
			return err
		}
		next, err := quoteIdentifier(newName)
		if err != nil {
			return err
		}
		if kind == "CONSTRAINT" {
			old, err := quoteIdentifier(oldName)
			if err != nil {
				return err
			}
			sql = append(sql, "ALTER TABLE "+table+" RENAME CONSTRAINT "+old+" TO "+next)
		} else {
			old, err := quoteTable(namespace, oldName)
			if err != nil {
				return err
			}
			sql = append(sql, "ALTER "+kind+" "+old+" RENAME TO "+next)
		}
		return nil
	}
	if before.PrimaryKey {
		if err := rename("SEQUENCE", postgresIdentitySequenceName); err != nil {
			return nil, err
		}
	}
	if before.Relation != nil {
		if err := rename("CONSTRAINT", postgresForeignKeyConstraintName); err != nil {
			return nil, err
		}
	}
	if before.Unique {
		if err := rename("CONSTRAINT", postgresUniqueConstraintName); err != nil {
			return nil, err
		}
	}
	if before.HasColumnIndex() {
		if err := rename("INDEX", postgresColumnIndexName); err != nil {
			return nil, err
		}
	}
	return sql, nil
}

func assertPostgresFieldRenameDependencies(ctx context.Context, exec migrationSQLExecutor, namespace string, op backend.MigrationOperation, app string, before ir.Field) (resultErr error) {
	allowed := op.RenameReferences(app, before)
	count := 0
	for _, fields := range allowed {
		count += len(fields)
	}
	// The source table and declared referencing tables are already locked.
	// An external view or unsealed incoming column must not be rewritten.
	rows, err := exec.QueryContext(ctx, `WITH source AS (
		SELECT c.oid, a.attnum FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND NOT a.attisdropped
		WHERE n.nspname=$1 AND c.relname=$2 AND a.attname=$3
	)
	SELECT ns.nspname, child.relname, att.attname, cardinality(k.conkey)
	FROM source JOIN pg_catalog.pg_constraint k ON k.confrelid=source.oid
		AND k.contype='f' AND source.attnum=ANY(k.confkey)
	JOIN pg_catalog.pg_class child ON child.oid=k.conrelid
	JOIN pg_catalog.pg_namespace ns ON ns.oid=child.relnamespace
	JOIN pg_catalog.pg_attribute att ON att.attrelid=child.oid AND att.attnum=k.conkey[1]
	UNION ALL SELECT '', '', '', 0 FROM source JOIN pg_catalog.pg_depend d
		ON d.refobjid=source.oid AND d.refobjsubid IN (0,source.attnum)
		AND d.refclassid='pg_catalog.pg_class'::regclass AND d.classid='pg_catalog.pg_rewrite'::regclass
	LIMIT $4`, namespace, op.Before.DBTable, before.Column, count+1)
	if err != nil {
		return err
	}
	defer func() {
		if err := rows.Close(); resultErr == nil {
			resultErr = err
		}
	}()
	for rows.Next() {
		var schema, table, column string
		var arity int
		if err := rows.Scan(&schema, &table, &column, &arity); err != nil {
			return err
		}
		if schema != namespace || arity != 1 || !allowed[table][column] {
			return postgresMigrationCatalogDrift(op.Before.DBTable, "has a dependent view or foreign key outside its field rename authority")
		}
		delete(allowed[table], column)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, fields := range allowed {
		if len(fields) != 0 {
			return postgresMigrationCatalogDrift(op.Before.DBTable, "is missing a declared incoming key for the field rename")
		}
	}
	return nil
}
