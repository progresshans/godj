package postgres

import (
	"context"
	"reflect"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *postgresRevisionFencedTransaction) AlterField(ctx context.Context, model ir.Model, before, after ir.Field) error {
	return transaction.executeSchema(ctx, "alter PostgreSQL field choices", func(executor migrationSQLExecutor) error {
		return transaction.schema.AlterField(ctx, executor, model, before, after)
	})
}

func (schema *postgresMigrationSchema) AlterField(ctx context.Context, executor migrationSQLExecutor, model ir.Model, before, after ir.Field) error {
	operation, err := schema.postgresMigrationCurrentOperation(ctx, executor, migrationbackend.MigrationAlterField)
	if err != nil {
		return err
	}
	wantBefore, wantAfter, err := migrationbackend.ChangedChoiceField(operation.Before, operation.After)
	if err != nil || !reflect.DeepEqual(model, operation.Before) || !before.Equal(wantBefore) || !after.Equal(wantAfter) {
		return postgresMigrationIntentIntegrity("AlterField arguments differ from the sealed choices transition", err)
	}
	// No DDL is needed; the unchanged physical schema is still checked before
	// history publication by the same complete migration intent.
	schema.cursor++
	return nil
}

// Called only after full chronological target metadata was validated. Choices
// have no catalog representation; every other field property still matches.
func postgresChoiceStorageEqual(left, right ir.Model) bool {
	if !postgresMigrationSameModel(left, right) || len(left.Fields) != len(right.Fields) {
		return false
	}
	for index, before := range left.Fields {
		after := right.Fields[index]
		before.Choices, after.Choices = nil, nil
		if !before.Equal(after) {
			return false
		}
	}
	return true
}
