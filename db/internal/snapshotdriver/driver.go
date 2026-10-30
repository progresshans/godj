// Package snapshotdriver provides deterministic native-connection fault probes
// for the backend read-snapshot owners. Real DB tests own isolation semantics.
package snapshotdriver

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
)

type State struct {
	mu            sync.Mutex
	RollbackError error
	BeginError    error
	connections   int
	closed        int
	rows          int
	statements    []string
}

type Snapshot struct {
	Connections, Closed, OpenRows int
	Statements                    []string
}

func (state *State) Snapshot() Snapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	return Snapshot{state.connections, state.closed, state.rows, append([]string(nil), state.statements...)}
}

type Connector struct{ State *State }

func (connector Connector) Connect(context.Context) (driver.Conn, error) {
	connector.State.mu.Lock()
	connector.State.connections++
	connector.State.mu.Unlock()
	return &connection{state: connector.State}, nil
}
func (connector Connector) Driver() driver.Driver { return factory{connector} }

type factory struct{ Connector }

func (factory factory) Open(string) (driver.Conn, error) {
	return factory.Connect(context.Background())
}

type connection struct{ state *State }

func (*connection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (*connection) Begin() (driver.Tx, error) {
	return nil, errors.New("snapshot owner must own terminal SQL")
}
func (connection *connection) Close() error {
	connection.state.mu.Lock()
	connection.state.closed++
	connection.state.mu.Unlock()
	return nil
}
func (connection *connection) ExecContext(ctx context.Context, statement string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := connection.state
	state.mu.Lock()
	defer state.mu.Unlock()
	state.statements = append(state.statements, statement)
	if strings.HasPrefix(statement, "BEGIN") && state.BeginError != nil {
		return nil, state.BeginError
	}
	if statement == "ROLLBACK" {
		if state.rows != 0 {
			return nil, errors.New("rollback ran with live rows")
		}
		if state.RollbackError != nil {
			return nil, state.RollbackError
		}
	}
	return driver.RowsAffected(0), nil
}
func (connection *connection) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection.state.mu.Lock()
	connection.state.rows++
	connection.state.mu.Unlock()
	return &rows{state: connection.state}, nil
}

type rows struct {
	state           *State
	emitted, closed bool
}

func (*rows) Columns() []string { return []string{"id"} }
func (rows *rows) Close() error {
	if !rows.closed {
		rows.closed = true
		rows.state.mu.Lock()
		rows.state.rows--
		rows.state.mu.Unlock()
	}
	return nil
}
func (rows *rows) Next(values []driver.Value) error {
	if rows.emitted {
		return io.EOF
	}
	rows.emitted = true
	values[0] = int64(1)
	return nil
}
