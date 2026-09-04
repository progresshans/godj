package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
)

func TestQuarantinedBackendRejectsNewIOBeforeDriverAdmission(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	queryPlan := query.NewPlan("quarantine_probe", []query.FieldRef{id})
	insertPlan := query.NewInsertPlan("quarantine_probe", []query.Assignment{
		query.NewAssignment(title, query.String("value")),
	})
	updatePlan := query.NewUpdatePlan("quarantine_probe", []query.Assignment{
		query.NewAssignment(title, query.String("changed")),
	}, id, query.Integer(1))
	deletePlan := query.NewDeletePlan("quarantine_probe", id, query.Integer(1))
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "probe", Name: "0001_initial"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}}

	tests := []struct {
		name   string
		invoke func(context.Context, *quarantineSurfaceHarness, *atomic.Int32) error
	}{
		{
			name: "query",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				rows, err := harness.backend.Query(ctx, queryPlan)
				if rows != nil {
					_ = rows.Close()
					return errors.New("quarantined Query returned non-nil rows")
				}
				return err
			},
		},
		{
			name: "exec context",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				result, err := harness.backend.ExecContext(ctx, `CREATE TABLE "quarantine_probe" ("id" INTEGER)`)
				if result != nil {
					return errors.New("quarantined ExecContext returned a non-nil result")
				}
				return err
			},
		},
		{
			name: "SQLite version",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				version, err := harness.backend.SQLiteVersion(ctx)
				if version != "" {
					return errors.New("quarantined SQLiteVersion returned a version")
				}
				return err
			},
		},
		{
			name: "insert",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				rows, err := harness.backend.Insert(ctx, insertPlan)
				if rows != 0 {
					return errors.New("quarantined Insert returned affected rows")
				}
				return err
			},
		},
		{
			name: "update",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				rows, err := harness.backend.Update(ctx, updatePlan)
				if rows != 0 {
					return errors.New("quarantined Update returned affected rows")
				}
				return err
			},
		},
		{
			name: "delete",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				rows, err := harness.backend.Delete(ctx, deletePlan)
				if rows != 0 {
					return errors.New("quarantined Delete returned affected rows")
				}
				return err
			},
		},
		{
			name: "atomic",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, callbacks *atomic.Int32) error {
				return harness.backend.Atomic(ctx, func(db.Session) error {
					callbacks.Add(1)
					return nil
				})
			},
		},
		{
			name: "atomic relation",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, callbacks *atomic.Int32) error {
				return harness.backend.AtomicRelation(ctx, func(db.RelationSession) error {
					callbacks.Add(1)
					return nil
				})
			},
		},
		{
			name: "coordinated atomic",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, callbacks *atomic.Int32) error {
				return harness.backend.CoordinatedAtomic(ctx, func(db.Session) error {
					callbacks.Add(1)
					return nil
				})
			},
		},
		{
			name: "begin migration",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				transaction, err := harness.backend.BeginMigration(ctx)
				if transaction != nil {
					return errors.New("quarantined BeginMigration returned a transaction")
				}
				return err
			},
		},
		{
			name: "read applied migrations",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				records, err := harness.backend.ReadAppliedMigrations(ctx)
				if records != nil {
					return errors.New("quarantined ReadAppliedMigrations returned records")
				}
				return err
			},
		},
		{
			name: "open revision-fenced session",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				session, err := harness.backend.OpenRevisionFencedSession(ctx)
				if session != nil {
					return errors.New("quarantined OpenRevisionFencedSession returned a session")
				}
				return err
			},
		},
		{
			name: "pre-created revision session read",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				session := &sqliteRevisionFencedSession{
					backend: harness.backend,
					state:   revisionSessionOpen,
				}
				records, err := session.ReadAppliedMigrations(ctx)
				if records != nil {
					return errors.New("quarantined revision session read returned records")
				}
				return err
			},
		},
		{
			name: "ready revision session begin",
			invoke: func(ctx context.Context, harness *quarantineSurfaceHarness, _ *atomic.Int32) error {
				session := &sqliteRevisionFencedSession{
					backend: harness.backend,
					state:   revisionSessionReady,
				}
				transaction, err := session.BeginMigration(ctx, transition, intent)
				if transaction != nil {
					return errors.New("quarantined revision session begin returned a transaction")
				}
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := newQuarantineSurfaceHarness(t)
			var callbacks atomic.Int32
			err := test.invoke(context.Background(), harness, &callbacks)
			assertQuarantineSurfaceRecoveryRequired(t, err)

			if got := harness.driver.connectCalls.Load(); got != 0 {
				t.Fatalf("driver Connect calls = %d, want 0", got)
			}
			if got := harness.driver.sqlCalls.Load(); got != 0 {
				t.Fatalf("driver SQL calls = %d, want 0", got)
			}
			if got := callbacks.Load(); got != 0 {
				t.Fatalf("callback calls = %d, want 0", got)
			}
			if got := harness.retained.ioCalls.Load(); got != 0 {
				t.Fatalf("retained connection I/O calls = %d, want 0", got)
			}
			if got := harness.backend.QueryCount(); got != 0 {
				t.Fatalf("backend query count = %d, want 0", got)
			}
			harness.assertExactOneQuarantined(t)
		})
	}
}

func TestQuarantinedBackendRecoveryStatePrecedesCanceledContext(t *testing.T) {
	t.Parallel()

	harness := newQuarantineSurfaceHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := harness.backend.SQLiteVersion(ctx); !isSQLiteBackendRecoveryRequired(err) {
		t.Fatalf("SQLiteVersion(canceled) error = %v, want backend recovery required", err)
	}
	openSession := &sqliteRevisionFencedSession{backend: harness.backend, state: revisionSessionOpen}
	if records, err := openSession.ReadAppliedMigrations(ctx); records != nil || !isSQLiteBackendRecoveryRequired(err) {
		t.Fatalf("open session read = (%v, %v), want nil/backend recovery required", records, err)
	}
	if openSession.state != revisionSessionOpen {
		t.Fatalf("open session state = %d, want unchanged open", openSession.state)
	}

	readySession := &sqliteRevisionFencedSession{backend: harness.backend, state: revisionSessionReady}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "probe", Name: "0001_initial"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}}
	if transaction, err := readySession.BeginMigration(ctx, transition, intent); transaction != nil || !isSQLiteBackendRecoveryRequired(err) {
		t.Fatalf("ready session begin = (%v, %v), want nil/backend recovery required", transaction, err)
	}
	if readySession.state != revisionSessionReady {
		t.Fatalf("ready session state = %d, want unchanged ready", readySession.state)
	}

	if harness.driver.connectCalls.Load() != 0 || harness.driver.sqlCalls.Load() != 0 {
		t.Fatalf("driver connect/SQL calls = %d/%d, want 0/0", harness.driver.connectCalls.Load(), harness.driver.sqlCalls.Load())
	}
}

func TestHealthyRevisionSessionCancellationDoesNotPoisonState(t *testing.T) {
	t.Parallel()

	backend, err := OpenMemory(context.Background(), "revision-session-cancellation-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	openSession := &sqliteRevisionFencedSession{backend: backend, state: revisionSessionOpen}
	if records, err := openSession.ReadAppliedMigrations(ctx); records != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("open session canceled read = (%v, %v), want nil/context.Canceled", records, err)
	}
	if openSession.state != revisionSessionOpen {
		t.Fatalf("open session state = %d, want unchanged open", openSession.state)
	}
	readyReadSession := &sqliteRevisionFencedSession{backend: backend, state: revisionSessionReady}
	if records, err := readyReadSession.ReadAppliedMigrations(ctx); records != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ready session canceled read = (%v, %v), want nil/context.Canceled", records, err)
	}
	if readyReadSession.state != revisionSessionReady {
		t.Fatalf("ready read session state = %d, want unchanged ready", readyReadSession.state)
	}

	readySession := &sqliteRevisionFencedSession{backend: backend, state: revisionSessionReady}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "probe", Name: "0001_initial"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}}
	if transaction, err := readySession.BeginMigration(ctx, transition, intent); transaction != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ready session canceled begin = (%v, %v), want nil/context.Canceled", transaction, err)
	}
	if readySession.state != revisionSessionReady {
		t.Fatalf("ready session state = %d, want unchanged ready", readySession.state)
	}
	invalidTransition := transition
	invalidTransition.Migration.App = ""
	if transaction, err := readySession.BeginMigration(ctx, invalidTransition, intent); transaction != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ready session canceled invalid begin = (%v, %v), want nil/context.Canceled", transaction, err)
	}
	if readySession.state != revisionSessionReady {
		t.Fatalf("ready session state after canceled invalid begin = %d, want unchanged ready", readySession.state)
	}
}

type quarantineSurfaceHarness struct {
	backend  *Backend
	driver   *quarantineSurfaceDriverState
	retained *quarantineSurfaceRetainedConnection
}

func newQuarantineSurfaceHarness(t *testing.T) *quarantineSurfaceHarness {
	t.Helper()

	driverState := &quarantineSurfaceDriverState{}
	database := sql.OpenDB(quarantineSurfaceConnector{state: driverState})
	retention := newRelationRetentionState()
	retained := &quarantineSurfaceRetainedConnection{}
	admission, err := retention.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire quarantine setup admission: %v", err)
	}
	if err := admission.retain(retained); !errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeBackendRecoveryRequired,
	}) {
		t.Fatalf("retain quarantine setup connection error = %v", err)
	}
	admission.release()

	harness := &quarantineSurfaceHarness{
		backend: &Backend{
			database:          database,
			relationRetention: retention,
		},
		driver:   driverState,
		retained: retained,
	}
	harness.assertExactOneQuarantined(t)
	t.Cleanup(func() {
		if err := harness.backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if got := retained.closeCalls.Load(); got != 1 {
			t.Errorf("retained connection Close calls = %d, want 1", got)
		}
	})
	return harness
}

func (harness *quarantineSurfaceHarness) assertExactOneQuarantined(t *testing.T) {
	t.Helper()

	state := harness.backend.relationRetention
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.quarantined {
		t.Fatal("relation retention state is not quarantined")
	}
	if state.retained != harness.retained {
		t.Fatalf("retained connection = %T %p, want exact setup connection %p", state.retained, state.retained, harness.retained)
	}
	if state.active != nil {
		t.Fatalf("active transaction admission = %p, want nil", state.active)
	}
}

func assertQuarantineSurfaceRecoveryRequired(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeBackendRecoveryRequired,
	}) {
		t.Fatalf("error = %v, want %s/%s", err, query.CategoryBackend, query.CodeBackendRecoveryRequired)
	}
	var classified *query.Error
	if !errors.As(err, &classified) || classified.Category != query.CategoryBackend || classified.Code != query.CodeBackendRecoveryRequired {
		t.Fatalf("classified error = %#v, want %s/%s", classified, query.CategoryBackend, query.CodeBackendRecoveryRequired)
	}
}

type quarantineSurfaceDriverState struct {
	connectCalls atomic.Int32
	sqlCalls     atomic.Int32
}

type quarantineSurfaceConnector struct {
	state *quarantineSurfaceDriverState
}

func (connector quarantineSurfaceConnector) Connect(context.Context) (driver.Conn, error) {
	connector.state.connectCalls.Add(1)
	return &quarantineSurfaceDriverConnection{state: connector.state}, nil
}

func (connector quarantineSurfaceConnector) Driver() driver.Driver {
	return quarantineSurfaceDriver{state: connector.state}
}

type quarantineSurfaceDriver struct {
	state *quarantineSurfaceDriverState
}

func (testDriver quarantineSurfaceDriver) Open(string) (driver.Conn, error) {
	testDriver.state.connectCalls.Add(1)
	return &quarantineSurfaceDriverConnection{state: testDriver.state}, nil
}

type quarantineSurfaceDriverConnection struct {
	state *quarantineSurfaceDriverState
}

func (connection *quarantineSurfaceDriverConnection) Prepare(string) (driver.Stmt, error) {
	connection.state.sqlCalls.Add(1)
	return nil, errors.New("quarantine surface driver must not prepare SQL")
}

func (*quarantineSurfaceDriverConnection) Close() error { return nil }

func (connection *quarantineSurfaceDriverConnection) Begin() (driver.Tx, error) {
	connection.state.sqlCalls.Add(1)
	return nil, errors.New("quarantine surface driver must not begin a transaction")
}

func (connection *quarantineSurfaceDriverConnection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	connection.state.sqlCalls.Add(1)
	return nil, errors.New("quarantine surface driver must not begin a transaction")
}

func (connection *quarantineSurfaceDriverConnection) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	connection.state.sqlCalls.Add(1)
	return nil, errors.New("quarantine surface driver must not execute SQL")
}

func (connection *quarantineSurfaceDriverConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	connection.state.sqlCalls.Add(1)
	return nil, errors.New("quarantine surface driver must not query SQL")
}

func (connection *quarantineSurfaceDriverConnection) Ping(context.Context) error {
	connection.state.sqlCalls.Add(1)
	return errors.New("quarantine surface driver must not ping")
}

type quarantineSurfaceRetainedConnection struct {
	ioCalls    atomic.Int32
	closeCalls atomic.Int32
}

func (connection *quarantineSurfaceRetainedConnection) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	connection.ioCalls.Add(1)
	return nil, errors.New("quarantine surface retained connection must not execute SQL")
}

func (connection *quarantineSurfaceRetainedConnection) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	connection.ioCalls.Add(1)
	return nil, errors.New("quarantine surface retained connection must not query SQL")
}

func (connection *quarantineSurfaceRetainedConnection) Raw(func(any) error) error {
	connection.ioCalls.Add(1)
	return errors.New("quarantine surface retained connection must not expose raw driver state")
}

func (connection *quarantineSurfaceRetainedConnection) Close() error {
	connection.closeCalls.Add(1)
	return nil
}
