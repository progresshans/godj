package decimaltest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

// ScanFailures checks the actual database/sql row lifecycle using an injected
// driver. Real SQLite/PostgreSQL storage and transactions are tested separately.
func ScanFailures(t *testing.T, valid driver.Value, validate func(context.Context, *sql.DB) error) {
	t.Helper()
	queryErr, nextErr, closeErr := errors.New("query failure"), errors.New("next failure"), errors.New("close failure")
	for _, test := range []struct {
		name               string
		query, next, close error
		cancel             bool
		want               error
	}{
		{name: "valid"},
		{name: "query", query: queryErr, want: queryErr},
		{name: "iteration", next: nextErr, want: nextErr},
		{name: "close", close: closeErr, want: closeErr},
		{name: "cancel_after_row", cancel: true, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			connector := &scanConnector{value: valid, query: test.query, next: test.next, close: test.close}
			if test.cancel {
				connector.cancel = cancel
			}
			database := sql.OpenDB(connector)
			defer func() {
				if err := database.Close(); err != nil {
					t.Error(err)
				}
			}()
			err := validate(ctx, database)
			if !errors.Is(err, test.want) {
				t.Fatalf("scan error = %v, want %v", err, test.want)
			}
			if connector.queries != 1 || test.query == nil && connector.closed != 1 {
				t.Fatal("scan did not own exactly one query/row close")
			}
			if test.query == nil && connector.read != 1 {
				t.Fatal("failure path did not consume the valid row")
			}
		})
	}
}

type scanConnector struct {
	value                 driver.Value
	query, next, close    error
	cancel                context.CancelFunc
	queries, closed, read int
}

func (c *scanConnector) Connect(context.Context) (driver.Conn, error) { return &scanConnection{c}, nil }
func (c *scanConnector) Driver() driver.Driver                        { return scanDriver{} }

type scanDriver struct{}

func (scanDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type scanConnection struct{ owner *scanConnector }

func (*scanConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*scanConnection) Begin() (driver.Tx, error) { return nil, errors.New("unexpected begin") }
func (*scanConnection) Close() error              { return nil }
func (c *scanConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.owner.queries++
	if c.owner.query != nil {
		return nil, c.owner.query
	}
	return &scanRows{owner: c.owner}, nil
}

type scanRows struct{ owner *scanConnector }

func (*scanRows) Columns() []string { return []string{"value"} }
func (r *scanRows) Close() error    { r.owner.closed++; return r.owner.close }
func (r *scanRows) Next(destination []driver.Value) error {
	if r.owner.read == 0 {
		destination[0] = r.owner.value
		r.owner.read++
		return nil
	}
	if r.owner.cancel != nil {
		r.owner.cancel()
		return context.Canceled
	}
	if r.owner.next != nil {
		return r.owner.next
	}
	return io.EOF
}
