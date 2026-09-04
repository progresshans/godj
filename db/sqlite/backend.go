// Package sqlite implements GoDj's SQLite compiler and database/sql executor.
// The driver stays private to this backend so public ORM APIs are independent
// of driver names and DSN details.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const DriverModule = "modernc.org/sqlite"

type Backend struct {
	database          *sql.DB
	relationRetention *relationRetentionState
	queryCount        atomic.Uint64
	closed            atomic.Bool
}

var _ db.Queryer = (*Backend)(nil)

func Open(ctx context.Context, dataSourceName string) (*Backend, error) {
	if ctx == nil {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	database, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping SQLite database: %w", err)
	}
	return &Backend{
		database:          database,
		relationRetention: newRelationRetentionState(),
	}, nil
}

func OpenMemory(ctx context.Context, name string) (*Backend, error) {
	dataSourceName := "file:" + url.PathEscape(name) + "?mode=memory&cache=shared"
	backend, err := Open(ctx, dataSourceName)
	if err != nil {
		return nil, err
	}
	// A named shared-memory database still disappears with the last connection.
	// One connection makes that lifetime and transaction behavior deterministic
	// for the M1 backend profile.
	backend.database.SetMaxOpenConns(1)
	backend.database.SetMaxIdleConns(1)
	return backend, nil
}

func (b *Backend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if b == nil || b.database == nil || b.closed.Load() {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite backend is nil or closed"}
	}
	if ctx == nil {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	statement, arguments, err := Compile(plan)
	if err != nil {
		return nil, err
	}
	b.queryCount.Add(1)
	rows, err := b.database.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, classifyQueryError(err, plan.Table())
	}
	return rows, nil
}

func classifyQueryError(err error, table string) error {
	if isSQLiteMissingTableError(err, table) {
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeMissingTable,
			Detail:   fmt.Sprintf("SQLite table %q does not exist", table),
			Cause:    err,
		}
	}
	return fmt.Errorf("execute SQLite query: %w", err)
}

func isSQLiteMissingTableError(err error, table string) bool {
	var sqliteError *modernsqlite.Error
	return errors.As(err, &sqliteError) &&
		sqliteError.Code() == sqlite3.SQLITE_ERROR &&
		isMissingTableMessage(sqliteError.Error(), table)
}

func isMissingTableMessage(message, table string) bool {
	const marker = "no such table: "
	position := strings.LastIndex(message, marker)
	if position < 0 {
		return false
	}
	remainder := message[position+len(marker):]
	return remainder == table || strings.HasPrefix(remainder, table+" (")
}

// ExecContext is intentionally a backend-level primitive. M1 uses it only in
// the conformance schema provisioner; it is not a model write lifecycle API.
func (b *Backend) ExecContext(ctx context.Context, statement string, arguments ...any) (sql.Result, error) {
	if b == nil || b.database == nil || b.closed.Load() {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite backend is nil or closed"}
	}
	if ctx == nil {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	return b.database.ExecContext(ctx, statement, arguments...)
}

func (b *Backend) QueryCount() uint64 {
	if b == nil {
		return 0
	}
	return b.queryCount.Load()
}

func (b *Backend) SQLiteVersion(ctx context.Context) (string, error) {
	if b == nil || b.database == nil || b.closed.Load() {
		return "", &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite backend is nil or closed"}
	}
	if ctx == nil {
		return "", &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	var version string
	if err := b.database.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return "", fmt.Errorf("read SQLite version: %w", err)
	}
	return version, nil
}

func (b *Backend) Close() error {
	if b == nil {
		return nil
	}
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	var databaseErr error
	if b.database != nil {
		if err := b.database.Close(); err != nil {
			databaseErr = fmt.Errorf("close SQLite database: %w", err)
		}
	}
	var retainedErr error
	if b.relationRetention != nil {
		retainedErr = b.relationRetention.sealAndDrain()
	}
	return errors.Join(databaseErr, retainedErr)
}
