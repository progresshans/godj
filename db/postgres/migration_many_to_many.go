package postgres

import (
	"context"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
)

func (transaction *postgresRevisionFencedTransaction) AlterManyToMany(ctx context.Context, before, after ir.Model) error {
	return transaction.executeSchema(ctx, "change explicit PostgreSQL ManyToMany", func(executor migrationSQLExecutor) error {
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
		// Preflight locks and verifies the complete through/endpoint graph. Only
		// metadata/history changes; retained link PKs, payload and sequences stay put.
		transaction.schema.cursor++
		return nil
	})
}
