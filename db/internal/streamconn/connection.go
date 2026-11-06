// Package streamconn owns a pinned root-read connection and the operations
// borrowing it. A transaction can retain its own lease when cleanup is
// uncertain, without returning the stream's connection to the pool.
package streamconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type Owner struct {
	mu         sync.Mutex
	connection *sql.Conn
	finished   bool
	revoked    error
	references int
	leases     map[*Lease]struct{}
}

func New(connection *sql.Conn) *Owner {
	return &Owner{connection: connection, references: 1, leases: make(map[*Lease]struct{})}
}

// Acquire returns nil after Finish; the caller then uses its original root
// backend. A live but revoked stream never silently switches connections.
func (owner *Owner) Acquire() (*Lease, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.finished {
		return nil, nil
	}
	if owner.revoked != nil {
		return nil, owner.revoked
	}
	lease := &Lease{owner: owner, rows: make(map[*sql.Rows]struct{})}
	owner.references++
	owner.leases[lease] = struct{}{}
	return lease, nil
}

func (owner *Owner) Validate(ctx context.Context) error {
	if ctx == nil {
		return invalid("stream context is nil")
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.revoked != nil {
		return errors.Join(owner.revoked, ctx.Err())
	}
	if owner.finished {
		return invalid("stream read has finished")
	}
	return ctx.Err()
}

func (owner *Owner) activeLeases() []*Lease {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	leases := make([]*Lease, 0, len(owner.leases))
	for lease := range owner.leases {
		leases = append(leases, lease)
	}
	return leases
}

// Revoke closes native rows before physical discard. database/sql cannot
// close a pinned Conn while its rows still own its read lock.
func (owner *Owner) Revoke(cause error) error {
	owner.mu.Lock()
	if owner.revoked == nil {
		owner.revoked = cause
	}
	owner.mu.Unlock()
	var err error
	for _, lease := range owner.activeLeases() {
		err = errors.Join(err, lease.finishRows())
	}
	return err
}

func (owner *Owner) Finish() error {
	owner.mu.Lock()
	if owner.finished {
		owner.mu.Unlock()
		return nil
	}
	owner.finished = true
	revoked := owner.revoked
	owner.mu.Unlock()
	var err error
	for _, lease := range owner.activeLeases() {
		err = errors.Join(err, lease.finishRows())
	}
	return errors.Join(revoked, err, owner.release(nil))
}

// FinishReads closes abandoned direct rowsets before the next FETCH/CLOSE.
// Source rowsets belong to their enclosing iterator and are excluded.
func (owner *Owner) FinishReads() error {
	var err error
	for _, lease := range owner.activeLeases() {
		lease.mu.Lock()
		closeRead := lease.rowsOnly && !lease.sourceRows
		if closeRead && !lease.closed {
			lease.expired = invalid("batch callback has finished")
		}
		lease.mu.Unlock()
		if closeRead {
			err = errors.Join(err, lease.Close())
		}
	}
	return err
}

func (owner *Owner) release(lease *Lease) error {
	owner.mu.Lock()
	if lease != nil {
		delete(owner.leases, lease)
	}
	owner.references--
	last := owner.references == 0
	owner.mu.Unlock()
	if !last {
		return nil
	}
	err := owner.connection.Close()
	if errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	return err
}

type Lease struct {
	owner      *Owner
	mu         sync.Mutex
	closed     bool
	rowsOnly   bool
	sourceRows bool
	expired    error
	rows       map[*sql.Rows]struct{}
}

// Native is used only while the lease remains owned by the operation. Its
// transaction lifetime is separate from the root iterator's lifetime.
func (lease *Lease) Native() *sql.Conn { return lease.owner.connection }

func (lease *Lease) ExecContext(ctx context.Context, statement string, arguments ...any) (sql.Result, error) {
	result, err := lease.owner.connection.ExecContext(ctx, statement, arguments...)
	if errors.Is(err, sql.ErrConnDone) {
		err = errors.Join(err, lease.Close())
	}
	return result, err
}

func (lease *Lease) QueryRowContext(ctx context.Context, statement string, arguments ...any) *sql.Row {
	return lease.owner.connection.QueryRowContext(ctx, statement, arguments...)
}

func (lease *Lease) QueryContext(ctx context.Context, statement string, arguments ...any) (*sql.Rows, error) {
	rows, err := lease.owner.connection.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	lease.owner.mu.Lock()
	lease.mu.Lock()
	if lease.closed || lease.owner.finished || lease.owner.revoked != nil {
		lease.mu.Unlock()
		lease.owner.mu.Unlock()
		return nil, errors.Join(invalid("stream operation has finished"), rows.Close())
	}
	lease.rows[rows] = struct{}{}
	lease.mu.Unlock()
	lease.owner.mu.Unlock()
	return rows, nil
}

// ForgetRows removes an already closed FETCH rowset. A long stream keeps no
// native-row history proportional to the number of batches.
func (lease *Lease) ForgetRows(rows *sql.Rows) {
	lease.mu.Lock()
	delete(lease.rows, rows)
	lease.mu.Unlock()
}

func (lease *Lease) SourceRows() { lease.mu.Lock(); lease.sourceRows = true; lease.mu.Unlock() }

func (lease *Lease) closeRows() error {
	lease.mu.Lock()
	rows := lease.rows
	lease.rows = make(map[*sql.Rows]struct{})
	lease.mu.Unlock()
	var err error
	for rowset := range rows {
		err = errors.Join(err, rowset.Err(), rowset.Close())
	}
	return err
}

// CloseRows finishes a transaction callback's rowsets before terminal SQL.
func (lease *Lease) CloseRows() error { return lease.closeRows() }

// WrapRows releases bookkeeping while the transaction keeps its lease.
func (lease *Lease) WrapRows(rows db.Rows, native *sql.Rows) db.Rows {
	return &queryRows{Rows: rows, lease: lease, native: native}
}

type queryRows struct {
	db.Rows
	lease  *Lease
	native *sql.Rows
}

func (rows *queryRows) Close() error {
	err := rows.Rows.Close()
	rows.lease.ForgetRows(rows.native)
	return err
}

func (lease *Lease) finishRows() error {
	lease.mu.Lock()
	rowsOnly := lease.rowsOnly
	lease.mu.Unlock()
	if rowsOnly {
		return lease.Close()
	}
	return lease.closeRows()
}

func (lease *Lease) Raw(callback func(any) error) error {
	cleanupErr := lease.owner.Revoke(invalid("stream connection was revoked for transaction cleanup"))
	err := lease.owner.connection.Raw(callback)
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		// Confirmed physical discard already ended the transaction's lease.
		cleanupErr = errors.Join(cleanupErr, lease.Close())
	}
	return errors.Join(err, cleanupErr)
}

func (lease *Lease) Close() error {
	lease.mu.Lock()
	if lease.closed {
		lease.mu.Unlock()
		return nil
	}
	lease.closed = true
	lease.mu.Unlock()
	return errors.Join(lease.closeRows(), lease.owner.release(lease))
}

// Rows keeps a direct query's lease until the caller closes its rowset.
func (lease *Lease) Rows(rows db.Rows) db.Rows {
	lease.mu.Lock()
	lease.rowsOnly = true
	lease.mu.Unlock()
	lease.owner.mu.Lock()
	finished := lease.owner.finished
	lease.owner.mu.Unlock()
	if finished {
		_ = lease.Close()
	}
	return &ownedRows{Rows: rows, lease: lease}
}

type ownedRows struct {
	db.Rows
	lease *Lease
	close sync.Once
	err   error
}

func (rows *ownedRows) Next() bool {
	if rows.Err() != nil {
		return false
	}
	return rows.Rows.Next()
}
func (rows *ownedRows) Scan(destinations ...any) error {
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Rows.Scan(destinations...)
}
func (rows *ownedRows) Err() error {
	rows.lease.mu.Lock()
	expired := rows.lease.expired
	rows.lease.mu.Unlock()
	return errors.Join(rows.Rows.Err(), expired, rows.lease.owner.Validate(context.Background()))
}

func (rows *ownedRows) Close() error {
	rows.close.Do(func() { rows.err = errors.Join(rows.Rows.Close(), rows.lease.Close()) })
	return rows.err
}

func invalid(detail string) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: detail}
}
