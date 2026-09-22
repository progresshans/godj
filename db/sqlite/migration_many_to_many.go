package sqlite

import (
	"context"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
)

func (transaction *sqliteRevisionFencedTransaction) AlterManyToMany(ctx context.Context, before, after ir.Model) error {
	return transaction.execute(ctx, "change explicit ManyToMany", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected ManyToMany change at cursor %d", stateCursor(state))
		}
		if err := verifySQLiteRelationIntentSeal(&state.seal); err != nil {
			return err
		}
		op := state.seal.intent.Operations[state.cursor]
		if op.Kind != migrationbackend.MigrationAlterManyToMany || !reflect.DeepEqual(before, op.Before) || !reflect.DeepEqual(after, op.After) {
			return relationIntentIntegrity("ManyToMany arguments differ from the sealed transition")
		}
		if _, _, err := op.ChangedManyToMany(); err != nil {
			return relationIntentIntegrity("invalid ManyToMany delta: %v", err)
		}
		// The sealed graph and physical preflight include both endpoints, the
		// existing intermediary and its transitive FKs. No row or DDL rewrite is
		// needed; final catalog/history verification still owns publication.
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
	})
}
