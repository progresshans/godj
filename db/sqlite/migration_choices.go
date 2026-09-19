package sqlite

import (
	"context"
	"reflect"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *sqliteRevisionFencedTransaction) AlterField(ctx context.Context, model ir.Model, before, after ir.Field) error {
	return transaction.execute(ctx, "alter field choices", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected AlterField after intent cursor %d", stateCursor(state))
		}
		operation := state.seal.intent.Operations[state.cursor]
		if operation.Kind != migrationbackend.MigrationAlterField || !reflect.DeepEqual(model, operation.Before) {
			return relationIntentIntegrity("AlterField differs from the sealed model at cursor %d", state.cursor)
		}
		wantBefore, wantAfter, err := migrationbackend.ChangedChoiceField(operation.Before, operation.After)
		if err != nil || !before.Equal(wantBefore) || !after.Equal(wantAfter) {
			return relationIntentIntegrity("AlterField differs from the sealed field transition at cursor %d", state.cursor)
		}
		// Choices have no physical schema representation. Still consume exactly
		// one sealed operation and retain final catalog/revision validation.
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
	})
}

func (transaction *migrationTransaction) AlterField(ctx context.Context, _ ir.Model, _, _ ir.Field) error {
	return transaction.execute(ctx, func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("alter_field_choices", "choices changes require the revision-fenced migration lifecycle", nil)
	})
}

func validateSQLiteChoiceDelta(before, after ir.Model) error {
	if err := validateExactNormalizedRelationModel(before); err != nil {
		return err
	}
	if err := validateExactNormalizedRelationModel(after); err != nil {
		return err
	}
	_, _, err := migrationbackend.ChangedChoiceField(before, after)
	return err
}
