package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Every physical connection must reject an unresolved quoted identifier. The
// driver's DQS option is applied before any DSN PRAGMA or prepared statement.
// Put the backend-owned option first: the driver reads the first value, so a
// caller's duplicate _dqs option cannot weaken this query-integrity boundary.
// Preserve all other query bytes and their order for the driver's own parser.
func strictSQLiteDSN(dsn string) string {
	if position := strings.IndexByte(dsn, '?'); position > 0 {
		return dsn[:position+1] + "_dqs=0&" + dsn[position+1:]
	} else if position == 0 {
		// The driver treats a leading '?' as a literal filename, not options.
		return "file:" + url.PathEscape(dsn) + "?_dqs=0"
	}
	if dsn == "" {
		// SQLite's empty URI filename retains its private temporary DB meaning.
		return "file:?_dqs=0"
	}
	return dsn + "?_dqs=0"
}

// foreignKeyConnector initializes each physical connection, including pool
// growth, replacement and a reopened database. It wraps the registered driver's
// connector so its DSN handling and registrations remain owned by the driver.
// No process-global hook or driver registration is introduced.
type foreignKeyConnector struct{ driver.Connector }

func (connector foreignKeyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection, err := connector.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, errors.New("SQLite connector returned no connection")
	}
	if err := initializeSQLiteForeignKeys(ctx, connection); err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	return connection, nil
}

func initializeSQLiteForeignKeys(ctx context.Context, connection driver.Conn) (resultErr error) {
	executor, supportsExec := connection.(driver.ExecerContext)
	queryer, supportsQuery := connection.(driver.QueryerContext)
	if !supportsExec || !supportsQuery {
		return errors.New("SQLite driver lacks context-aware connection initialization")
	}
	if _, err := executor.ExecContext(ctx, "PRAGMA foreign_keys = ON", nil); err != nil {
		return fmt.Errorf("enable SQLite connection foreign keys: %w", err)
	}
	rows, err := queryer.QueryContext(ctx, "PRAGMA foreign_keys", nil)
	if err != nil {
		return fmt.Errorf("read SQLite connection foreign keys: %w", err)
	}
	if rows == nil {
		return errors.New("SQLite connection foreign key readback returned no rows")
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	if len(rows.Columns()) != 1 {
		return errors.New("SQLite connection foreign key readback has unexpected columns")
	}
	var value [1]driver.Value
	if err := rows.Next(value[:]); err != nil {
		return fmt.Errorf("read SQLite connection foreign key value: %w", err)
	}
	if enabled, ok := value[0].(int64); !ok || enabled != 1 {
		return errors.New("SQLite connection foreign key enforcement is not enabled")
	}
	if err := rows.Next(value[:]); err != io.EOF {
		if err != nil {
			return fmt.Errorf("complete SQLite connection foreign key readback: %w", err)
		}
		return errors.New("SQLite connection foreign key readback returned extra rows")
	}
	return ctx.Err()
}
