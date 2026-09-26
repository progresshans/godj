package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func TestSQLiteEveryPhysicalConnectionEnforcesForeignKeys(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "connections.sqlite3")
	// Driver-level OFF options cannot weaken the backend's integrity contract.
	backend, err := Open(ctx, "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(0)&_fk=off")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	for _, statement := range []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id))`,
		`INSERT INTO parent VALUES (1)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	backend.database.SetMaxOpenConns(3)
	var held []*sql.Conn
	for index := 0; index < 3; index++ {
		connection, err := backend.database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, connection)
		t.Cleanup(func() { _ = connection.Close() })
		var enabled int
		if err := connection.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatalf("connection %d foreign keys=%d: %v", index, enabled, err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO child(parent_id) VALUES(999)`); err == nil {
			t.Fatalf("connection %d accepted an orphan", index)
		}
	}
	for _, connection := range held {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	backend.database.SetMaxIdleConns(0)
	if _, err := backend.ExecContext(ctx, `INSERT INTO child(parent_id) VALUES(999)`); err == nil {
		t.Fatal("pool replacement accepted an orphan")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.ExecContext(ctx, `INSERT INTO child(parent_id) VALUES(999)`); err == nil {
		t.Fatal("reopen accepted an orphan")
	}
	var count int
	if err := reopened.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM child").Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan rows=%d: %v", count, err)
	}
}

type initializationConnector struct {
	connection *initializationConnection
	connects   int
}

func (c *initializationConnector) Driver() driver.Driver { return nil }
func (c *initializationConnector) Connect(context.Context) (driver.Conn, error) {
	c.connects++
	return c.connection, nil
}

type initializationConnection struct {
	execErr, queryErr, closeErr error
	rows                        *initializationRows
	closed                      int
}

func (c *initializationConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unused prepare")
}
func (c *initializationConnection) Begin() (driver.Tx, error) { return nil, errors.New("unused begin") }
func (c *initializationConnection) Close() error              { c.closed++; return c.closeErr }
func (c *initializationConnection) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), c.execErr
}
func (c *initializationConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return c.rows, c.queryErr
}

type initializationRows struct {
	values            []driver.Value
	nextErr, closeErr error
	closed            int
}

func (r *initializationRows) Columns() []string { return []string{"foreign_keys"} }
func (r *initializationRows) Close() error      { r.closed++; return r.closeErr }
func (r *initializationRows) Next(values []driver.Value) error {
	if r.nextErr != nil {
		return r.nextErr
	}
	if len(r.values) == 0 {
		return io.EOF
	}
	values[0] = r.values[0]
	r.values = r.values[1:]
	return nil
}

func TestSQLiteConnectionInitializationFailsBeforePublicationAndClosesResources(t *testing.T) {
	cause := errors.New("initialization fault")
	closeCause := errors.New("connection close fault")
	for _, test := range []struct {
		name      string
		mutate    func(*initializationConnection)
		wantCause bool
	}{
		{"exec", func(c *initializationConnection) { c.execErr = cause }, true},
		{"query", func(c *initializationConnection) { c.queryErr = cause }, true},
		{"next", func(c *initializationConnection) { c.rows.nextErr = cause }, true},
		{"close rows", func(c *initializationConnection) { c.rows.closeErr = cause }, true},
		{"disabled", func(c *initializationConnection) { c.rows.values = []driver.Value{int64(0)} }, false},
		{"wrong type", func(c *initializationConnection) { c.rows.values = []driver.Value{"1"} }, false},
		{"empty", func(c *initializationConnection) { c.rows.values = nil }, false},
		{"extra row", func(c *initializationConnection) { c.rows.values = []driver.Value{int64(1), int64(1)} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := &initializationConnection{rows: &initializationRows{values: []driver.Value{int64(1)}}, closeErr: closeCause}
			test.mutate(connection)
			base := &initializationConnector{connection: connection}
			got, err := (foreignKeyConnector{Connector: base}).Connect(t.Context())
			if got != nil || err == nil || connection.closed != 1 || !errors.Is(err, closeCause) || test.wantCause && !errors.Is(err, cause) {
				t.Fatalf("publication=%v close=%d err=%v", got, connection.closed, err)
			}
			if connection.execErr == nil && connection.queryErr == nil && connection.rows.closed != 1 {
				t.Fatal("readback rows leaked")
			}
		})
	}
	base := &initializationConnector{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := (foreignKeyConnector{Connector: base}).Connect(ctx); got != nil || !errors.Is(err, context.Canceled) || base.connects != 0 {
		t.Fatalf("canceled initialization opened a connection: %v", err)
	}
}
