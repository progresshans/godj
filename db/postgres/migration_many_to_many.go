package postgres

import (
	"context"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
)

func (transaction *postgresRevisionFencedTransaction) AlterManyToMany(ctx context.Context, before, after ir.Model) error {
	return transaction.executeSchema(ctx, "change PostgreSQL ManyToMany", func(executor migrationSQLExecutor) error {
		op, err := transaction.schema.postgresMigrationCurrentOperation(ctx, executor, migrationbackend.MigrationAlterManyToMany)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, op.Before) || !reflect.DeepEqual(after, op.After) {
			return postgresMigrationIntentIntegrity("ManyToMany arguments differ from the sealed transition", nil)
		}
		if _, _, err := op.ChangedManyToMany(); err != nil {
			return postgresMigrationIntentIntegrity("invalid ManyToMany delta", err)
		}
		changes, err := op.AutomaticStorageChanges(transaction.schema.transition.Migration.App)
		if err != nil {
			return err
		}
		for _, change := range changes {
			if change.Before.Name != "" && change.After.Name != "" && change.Before.DBTable != change.After.DBTable {
				if err := assertPostgresAutomaticRenameDependencies(ctx, executor, transaction.schema.namespace, change.Before.DBTable); err != nil {
					return err
				}
			}
		}
		statements, err := compilePostgresStorageOperation(transaction.schema.namespace, transaction.schema.transition.Migration.App, op)
		if err != nil {
			return err
		}
		for _, statement := range statements {
			if _, err := executor.ExecContext(ctx, statement); err != nil {
				return classifyPostgresRevisionContention(ctx, "change PostgreSQL owned storage", err)
			}
		}
		// Advance once after the complete owned statement group; explicit
		// intermediary changes remain metadata-only.
		transaction.schema.cursor++
		return nil
	})
}
