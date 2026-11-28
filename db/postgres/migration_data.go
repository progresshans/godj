package postgres

import (
	"context"
	"errors"

	"github.com/progresshans/godj/migrations/backend"
)

var _ backend.HistoricalDataTransaction = (*postgresRevisionFencedTransaction)(nil)

func (tx *postgresRevisionFencedTransaction) RunData(ctx context.Context, index int, callback backend.DataCallback) error {
	return tx.executeSchema(ctx, "run PostgreSQL historical data operation", func(executor migrationSQLExecutor) error {
		op, err := tx.schema.postgresMigrationCurrentOperation(ctx, executor, backend.MigrationRunData)
		if err != nil {
			return err
		}
		if op.OperationIndex != index || tx.transition.Migration.HistoryDigest == "" {
			return postgresRevisionIntegrity("data callback does not match a bound historical operation", nil)
		}
		session := newTransactionSession(ctx, executor, tx.session.backend)
		tx.failure = errors.New("PostgreSQL data callback did not finish")
		err = func() (callbackErr error) {
			defer func() { callbackErr = session.scope.FinishCallback(callbackErr) }()
			return backend.InvokeDataCallback(ctx, session, op.RelatedModels, callback)
		}()
		if err != nil {
			return err
		}
		tx.failure = nil
		tx.schema.cursor++
		return nil
	})
}
