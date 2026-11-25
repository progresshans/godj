package sqlite

import (
	"context"
	"errors"

	"github.com/progresshans/godj/migrations/backend"
)

var _ backend.HistoricalDataTransaction = (*sqliteRevisionFencedTransaction)(nil)

func (tx *sqliteRevisionFencedTransaction) RunData(ctx context.Context, index int, callback backend.DataCallback) error {
	return tx.execute(ctx, "run SQLite historical data operation", func(executor migrationSQLExecutor) error {
		state := tx.relation
		if tx.recorderCalled || state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected data callback after the sealed operation inventory")
		}
		if err := verifySQLiteRelationIntentSeal(&state.seal); err != nil {
			return err
		}
		op := state.seal.intent.Operations[state.cursor]
		if op.Kind != backend.MigrationRunData || op.OperationIndex != index || tx.transition.Migration.HistoryDigest == "" {
			return relationIntentIntegrity("data callback does not match a bound historical operation")
		}
		session := newRelationSession(ctx, tx.connection, &tx.session.backend.queryCount)
		tx.failure = errors.New("SQLite data callback did not finish")
		err := func() (callbackErr error) {
			defer func() { callbackErr = session.scope.FinishCallback(callbackErr) }()
			return backend.InvokeDataCallback(ctx, session, op.RelatedModels, callback)
		}()
		if err != nil {
			return err
		}
		tx.failure = nil
		state.cursor++
		return tx.completeRelationOperationIfLast(ctx, executor)
	})
}
