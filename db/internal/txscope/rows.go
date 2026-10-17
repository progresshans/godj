package txscope

import (
	"context"
	"sync"

	"github.com/progresshans/godj/db"
)

// Rows transfers a successful query's context and rowset to its scope before
// releasing the query reservation. A failed query closes any returned rowset.
func (op *Operation) Rows(source db.Rows, queryErr error) (db.Rows, error) {
	if source == nil && queryErr == nil {
		queryErr = invalid("transaction query returned nil rows without an error")
	}
	state := op.scope.owner
	state.mu.Lock()
	queryErr = joined(queryErr, op.scope.validateLocked(op.ctx))
	state.mu.Unlock()
	if queryErr != nil {
		if source != nil {
			queryErr = joined(queryErr, source.Close())
		}
		return nil, op.End(queryErr)
	}
	rows := &ownedRows{source: source, scope: op.scope, ctx: op.ctx}
	stop := context.AfterFunc(op.scope.ctx, func() { op.cancel(context.Cause(op.scope.ctx)) })
	resource, err := op.Resource(func(context.Context) error {
		readErr := source.Err()
		closeErr := source.Close()
		readErr = joined(readErr, context.Cause(rows.ctx))
		rows.mu.Lock()
		rows.closed, rows.closeErr = true, closeErr
		rows.readErr = joined(rows.readErr, readErr)
		rows.mu.Unlock()
		stop()
		op.cancel(context.Canceled)
		// The caller may handle a failed/canceled query and continue its
		// transaction. Only failure to close the transport is a scope cleanup
		// failure; Rows.Err retains the query's independent error ownership.
		return closeErr
	})
	if err != nil {
		stop()
		return nil, op.End(joined(err, source.Close()))
	}
	rows.resource = resource
	state.mu.Lock()
	resource.interrupt = func() { op.cancel(context.Canceled) }
	err = op.scope.validateLocked(op.ctx)
	op.releaseLocked(false)
	state.mu.Unlock()
	if err != nil {
		return nil, joined(err, resource.Close())
	}
	return rows, nil
}

type ownedRows struct {
	source   db.Rows
	scope    *Scope
	ctx      context.Context
	resource *Resource
	mu       sync.Mutex
	closed   bool
	readErr  error
	closeErr error
}

func (resource *Resource) begin(ctx context.Context) (*Operation, error) {
	state := resource.scope.owner
	state.mu.Lock()
	defer state.mu.Unlock()
	if resource.closing || resource.closed {
		return nil, invalid("transaction rowset is closed")
	}
	if err := resource.scope.validateLocked(ctx); err != nil {
		return nil, err
	}
	if state.operation != nil {
		return nil, invalid("transaction operations must be serialized by the caller")
	}
	op := resource.scope.operationLocked(ctx)
	op.resource = resource
	op.stop = context.AfterFunc(resource.scope.ctx, func() { op.cancel(context.Cause(resource.scope.ctx)) })
	return op, nil
}

func (rows *ownedRows) Next() bool {
	rows.mu.Lock()
	closed := rows.closed
	rows.mu.Unlock()
	if closed {
		return false
	}
	op, err := rows.resource.begin(rows.ctx)
	if err != nil {
		rows.setReadError(err)
		return false
	}
	defer op.Release()
	next := rows.source.Next()
	err = op.End(nil)
	if err != nil {
		rows.setReadError(err)
		next = false
	}
	if !next {
		_ = rows.Close()
	}
	return next
}

func (rows *ownedRows) Scan(destinations ...any) error {
	op, err := rows.resource.begin(rows.ctx)
	if err != nil {
		return err
	}
	defer op.Release()
	return op.End(rows.source.Scan(destinations...))
}

func (rows *ownedRows) setReadError(err error) {
	rows.mu.Lock()
	rows.readErr = joined(rows.readErr, err)
	rows.mu.Unlock()
}

func (rows *ownedRows) Err() error {
	rows.mu.Lock()
	closed, err := rows.closed, joined(rows.readErr, rows.closeErr)
	rows.mu.Unlock()
	if closed {
		// The transport context is canceled by Close itself; only the scope
		// lifetime remains relevant after a clean close.
		return joined(err, rows.scope.Validate(context.Background()))
	}
	return joined(joined(err, rows.source.Err()), rows.scope.Validate(rows.ctx))
}

func (rows *ownedRows) Close() error { return rows.resource.Close() }

func Query(scope *Scope, ctx context.Context, execute func(context.Context) (db.Rows, error)) (db.Rows, error) {
	op, err := scope.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer op.Release()
	rows, err := execute(op.Context())
	return op.Rows(rows, err)
}

func Do[T any](scope *Scope, ctx context.Context, execute func(context.Context) (T, error)) (T, error) {
	op, err := scope.Begin(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	defer op.Release()
	value, err := execute(op.Context())
	err = op.End(err)
	if err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}
