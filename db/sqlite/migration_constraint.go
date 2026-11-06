package sqlite

import (
	"context"
	"fmt"
	"reflect"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *sqliteRevisionFencedTransaction) AddConstraint(ctx context.Context, model ir.Model, constraint ir.UniqueConstraint) error {
	return transaction.changeConstraint(ctx, model, constraint, migrationbackend.MigrationAddConstraint)
}

func (transaction *sqliteRevisionFencedTransaction) RemoveConstraint(ctx context.Context, model ir.Model, constraint ir.UniqueConstraint) error {
	return transaction.changeConstraint(ctx, model, constraint, migrationbackend.MigrationRemoveConstraint)
}

func (transaction *sqliteRevisionFencedTransaction) changeConstraint(ctx context.Context, model ir.Model, constraint ir.UniqueConstraint, kind migrationbackend.MigrationOperationKind) error {
	return transaction.execute(ctx, "change named unique constraint", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected named constraint at cursor %d", stateCursor(state))
		}
		operation := state.seal.intent.Operations[state.cursor]
		want, err := operation.ChangedConstraint()
		if err != nil || operation.Kind != kind || !reflect.DeepEqual(model, operation.Before) || !constraint.Equal(want) {
			return relationIntentIntegrity("named constraint differs from sealed transition at cursor %d", state.cursor)
		}
		statement, err := compileSQLiteNamedUniqueAlter(model, want, kind == migrationbackend.MigrationAddConstraint)
		if err != nil {
			return err
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("change SQLite named unique constraint: %w", err)
		}
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
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
