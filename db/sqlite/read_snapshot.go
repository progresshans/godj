package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/readscope"
	"github.com/progresshans/godj/db/internal/streamconn"
	"github.com/progresshans/godj/query"
)

var _ db.SnapshotReader = (*Backend)(nil)

// ReadSnapshot retains SQLite's first-read snapshot on one connection. It
// takes no writer fence and exposes only the ordinary SELECT-plan boundary.
func (b *Backend) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) (err error) {
	if err := b.validateBackendContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "read snapshot callback is nil"}
	}
	admission, err := b.relationRetention.acquire(ctx)
	if err != nil {
		return err
	}
	defer admission.release()
	if err := b.validateBackendContext(ctx); err != nil {
		return err
	}
	connection, err := b.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite read snapshot connection: %w", err)
	}
	owner := streamconn.New(connection)
	defer func() { err = errors.Join(err, owner.Finish()) }()
	lease, err := owner.Acquire()
	if err != nil {
		return err
	}
	// Rollback/discard transfers an uncertain connection to retention. Do not
	// unconditionally release this lease and return that connection to the pool.
	if _, err := lease.ExecContext(ctx, "BEGIN"); err != nil {
		confirmed, cleanupErr := forceDiscardRelationConnection(lease)
		if !confirmed {
			cleanupErr = errors.Join(cleanupErr, admission.retain(lease))
		}
		return errors.Join(fmt.Errorf("begin SQLite read snapshot: %w", err), cleanupErr)
	}
	lifetime, finish := context.WithCancelCause(ctx)
	session := &relationSession{connection: lease, queryCount: &b.queryCount, lifetime: lifetime, active: true}
	return readscope.Run(ctx, session, callback, func() {
		session.deactivate()
		finish(sql.ErrTxDone)
	}, func() error {
		rowsErr := lease.CloseRows()
		_, cleanupErr := rollbackRelationConnection(ctx, lease, admission)
		return errors.Join(rowsErr, cleanupErr)
	})
}
