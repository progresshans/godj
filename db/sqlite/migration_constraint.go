package sqlite

import (
	"context"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *sqliteRevisionFencedTransaction) AddConstraint(ctx context.Context, _ ir.Model, _ ir.UniqueConstraint) error {
	return transaction.execute(ctx, "add constraint", func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("named_unique_constraints", "named constraints require native index ownership before migration execution", nil)
	})
}

func (transaction *sqliteRevisionFencedTransaction) RemoveConstraint(ctx context.Context, _ ir.Model, _ ir.UniqueConstraint) error {
	return transaction.execute(ctx, "remove constraint", func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("named_unique_constraints", "named constraints require native index ownership before migration execution", nil)
	})
}

func (transaction *migrationTransaction) AddConstraint(ctx context.Context, _ ir.Model, _ ir.UniqueConstraint) error {
	return transaction.execute(ctx, func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("named_unique_constraints", "named constraints require the revision-fenced migration lifecycle", nil)
	})
}

func (transaction *migrationTransaction) RemoveConstraint(ctx context.Context, _ ir.Model, _ ir.UniqueConstraint) error {
	return transaction.execute(ctx, func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("named_unique_constraints", "named constraints require the revision-fenced migration lifecycle", nil)
	})
}
