package postgres

import (
	"context"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *postgresRevisionFencedTransaction) AddConstraint(ctx context.Context, _ ir.Model, _ ir.UniqueConstraint) error {
	return transaction.executeSchema(ctx, "add PostgreSQL constraint", func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("named_unique_constraints", "named constraints require native ownership before migration execution", nil)
	})
}

func (transaction *postgresRevisionFencedTransaction) RemoveConstraint(ctx context.Context, _ ir.Model, _ ir.UniqueConstraint) error {
	return transaction.executeSchema(ctx, "remove PostgreSQL constraint", func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("named_unique_constraints", "named constraints require native ownership before migration execution", nil)
	})
}
