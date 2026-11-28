package postgres

import (
	"context"
	"reflect"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *postgresRevisionFencedTransaction) AddConstraint(ctx context.Context, model ir.Model, constraint ir.UniqueConstraint) error {
	return transaction.executeSchema(ctx, "add PostgreSQL constraint", func(executor migrationSQLExecutor) error {
		return transaction.schema.changeConstraint(ctx, executor, model, constraint, migrationbackend.MigrationAddConstraint)
	})
}

func (transaction *postgresRevisionFencedTransaction) RemoveConstraint(ctx context.Context, model ir.Model, constraint ir.UniqueConstraint) error {
	return transaction.executeSchema(ctx, "remove PostgreSQL constraint", func(executor migrationSQLExecutor) error {
		return transaction.schema.changeConstraint(ctx, executor, model, constraint, migrationbackend.MigrationRemoveConstraint)
	})
}

func (schema *postgresMigrationSchema) changeConstraint(ctx context.Context, executor migrationSQLExecutor, model ir.Model, constraint ir.UniqueConstraint, kind migrationbackend.MigrationOperationKind) error {
	operation, err := schema.postgresMigrationCurrentOperation(ctx, executor, kind)
	if err != nil {
		return err
	}
	want, err := operation.ChangedConstraint()
	if err != nil || !reflect.DeepEqual(model, operation.Before) || !constraint.Equal(want) {
		return postgresMigrationIntentIntegrity("named constraint arguments differ from the sealed transition", err)
	}
	statement, err := compilePostgresNamedUniqueAlter(schema.namespace, model, want, kind == migrationbackend.MigrationAddConstraint)
	if err != nil {
		return err
	}
	if _, err := executor.ExecContext(ctx, statement); err != nil {
		return classifyPostgresRevisionContention(ctx, "change PostgreSQL named unique constraint", err)
	}
	schema.cursor++
	return nil
}
