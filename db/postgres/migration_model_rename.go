package postgres

import (
	"context"

	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

var _ backend.ModelRenameEditor = (*postgresRevisionFencedTransaction)(nil)

func (tx *postgresRevisionFencedTransaction) RenameModel(ctx context.Context, before, after ir.Model) error {
	return tx.executeSchema(ctx, "rename PostgreSQL model", func(exec migrationSQLExecutor) error {
		op, err := tx.schema.postgresMigrationCurrentOperation(ctx, exec, backend.MigrationRenameModel)
		if err != nil {
			return err
		}
		if !op.Before.Equal(before) || !op.After.Equal(after) {
			return postgresMigrationIntentIntegrity("RenameModel arguments differ from the sealed transition", nil)
		}
		app, namespace := tx.transition.Migration.App, tx.schema.namespace
		graph, err := migrationgraph.ResolveMigrationGraph(app, op)
		if err != nil {
			return err
		}
		tables, err := op.RenamedTables(app, graph)
		if err != nil {
			return err
		}
		for _, table := range tables {
			if table.Before.DBTable == table.After.DBTable {
				continue
			}
			key, err := postgresMigrationPrimaryKey(table.Before)
			if err != nil {
				return err
			}
			if err := assertPostgresRenameDependencies(ctx, exec, namespace, table.Before.DBTable, key.Column, true, table.Incoming); err != nil {
				return err
			}
		}
		for _, table := range tables {
			statements, err := compilePostgresTableRename(namespace, table.Before, table.After)
			if err != nil {
				return err
			}
			for _, statement := range statements {
				if _, err := exec.ExecContext(ctx, statement); err != nil {
					return classifyPostgresRevisionContention(ctx, "rename PostgreSQL model "+table.Before.DBTable, err)
				}
			}
		}
		tx.schema.cursor++
		return nil
	})
}
