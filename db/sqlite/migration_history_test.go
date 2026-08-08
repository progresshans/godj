package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
)

func TestSQLiteReadAppliedMigrationsAbsentIsEmptyWithoutMutation(t *testing.T) {
	backend := openMigrationTestBackend(t)
	if sqliteTableExists(t, backend, migrationRecorderTable) {
		t.Fatal("migration recorder unexpectedly exists before read")
	}
	queryCount := backend.QueryCount()

	for range 2 {
		records, err := backend.ReadAppliedMigrations(context.Background())
		if err != nil {
			t.Fatalf("ReadAppliedMigrations() error = %v", err)
		}
		if len(records) != 0 {
			t.Fatalf("ReadAppliedMigrations() = %v, want empty", records)
		}
		if sqliteTableExists(t, backend, migrationRecorderTable) {
			t.Fatal("missing-recorder read created the recorder table")
		}
	}
	if got := backend.QueryCount(); got != queryCount {
		t.Fatalf("ORM QueryCount after history reads = %d, want %d", got, queryCount)
	}
}

func TestSQLiteReadAppliedMigrationsExistingEmptyRowsAndUnrecord(t *testing.T) {
	t.Run("existing empty", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
			t.Fatalf("create recorder: %v", err)
		}
		assertAppliedMigrations(t, backend)
		if !sqliteTableExists(t, backend, migrationRecorderTable) {
			t.Fatal("read removed the existing recorder table")
		}
	})

	t.Run("rows are sorted and fresh", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		zeta2 := migrationbackend.AppliedMigration{App: "zeta", Name: "0002"}
		alpha2 := migrationbackend.AppliedMigration{App: "alpha", Name: "0002"}
		alpha1 := migrationbackend.AppliedMigration{App: "alpha", Name: "0001"}
		recordAppliedMigrations(t, backend, zeta2, alpha2, alpha1)

		first := assertAppliedMigrations(t, backend, alpha1, alpha2, zeta2)
		first[0] = migrationbackend.AppliedMigration{App: "mutated", Name: "mutated"}
		assertAppliedMigrations(t, backend, alpha1, alpha2, zeta2)

		recordUnappliedMigration(t, backend, alpha2)
		assertAppliedMigrations(t, backend, alpha1, zeta2)
	})
}

func TestSQLiteReadAppliedMigrationsFreshBackendAndDatabaseIsolation(t *testing.T) {
	directory := t.TempDir()
	defaultPath := filepath.Join(directory, "default.sqlite3")
	analyticsPath := filepath.Join(directory, "analytics.sqlite3")
	defaultRecord := migrationbackend.AppliedMigration{App: "alpha", Name: "0001"}
	analyticsRecord := migrationbackend.AppliedMigration{App: "metrics", Name: "0009"}

	defaultWriter := openMigrationHistoryFileBackend(t, defaultPath)
	analyticsWriter := openMigrationHistoryFileBackend(t, analyticsPath)
	recordAppliedMigrations(t, defaultWriter, defaultRecord)
	recordAppliedMigrations(t, analyticsWriter, analyticsRecord)
	if err := defaultWriter.Close(); err != nil {
		t.Fatalf("close default writer: %v", err)
	}
	if err := analyticsWriter.Close(); err != nil {
		t.Fatalf("close analytics writer: %v", err)
	}

	defaultReader := openMigrationHistoryFileBackend(t, defaultPath)
	analyticsReader := openMigrationHistoryFileBackend(t, analyticsPath)
	assertAppliedMigrations(t, defaultReader, defaultRecord)
	assertAppliedMigrations(t, analyticsReader, analyticsRecord)
}

func TestSQLiteReadAppliedMigrationsFreshBackendAfterUnrecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unrecord.sqlite3")
	record := migrationbackend.AppliedMigration{App: "alpha", Name: "0001"}

	writer := openMigrationHistoryFileBackend(t, path)
	recordAppliedMigrations(t, writer, record)
	if err := writer.Close(); err != nil {
		t.Fatalf("close record writer: %v", err)
	}

	unwriter := openMigrationHistoryFileBackend(t, path)
	assertAppliedMigrations(t, unwriter, record)
	recordUnappliedMigration(t, unwriter, record)
	if err := unwriter.Close(); err != nil {
		t.Fatalf("close unrecord writer: %v", err)
	}

	fresh := openMigrationHistoryFileBackend(t, path)
	assertAppliedMigrations(t, fresh)
}

func TestSQLiteReadAppliedMigrationsRejectsMalformedAndUnavailableSources(t *testing.T) {
	t.Run("missing column is not missing recorder", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" ("app" TEXT NOT NULL)`); err != nil {
			t.Fatalf("create malformed recorder: %v", err)
		}
		records, err := backend.ReadAppliedMigrations(context.Background())
		if err == nil || records != nil {
			t.Fatalf("ReadAppliedMigrations() = (%v, %v), want nil/error", records, err)
		}
		if !strings.Contains(err.Error(), "no such column") {
			t.Fatalf("error = %v, want missing-column detail", err)
		}
	})

	t.Run("missing table behind view is not missing recorder", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		if _, err := backend.ExecContext(
			context.Background(),
			`CREATE VIEW "godj_migrations" AS SELECT "app", "name" FROM "missing_history_source"`,
		); err != nil {
			t.Fatalf("create malformed recorder view: %v", err)
		}
		records, err := backend.ReadAppliedMigrations(context.Background())
		if err == nil || records != nil {
			t.Fatalf("ReadAppliedMigrations() = (%v, %v), want nil/error", records, err)
		}
		if !strings.Contains(err.Error(), "missing_history_source") {
			t.Fatalf("error = %v, want underlying missing table", err)
		}
	})

	t.Run("null identity scan error is preserved", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		if _, err := backend.ExecContext(
			context.Background(),
			`CREATE TABLE "godj_migrations" ("app" TEXT, "name" TEXT)`,
		); err != nil {
			t.Fatalf("create nullable recorder: %v", err)
		}
		if _, err := backend.ExecContext(
			context.Background(),
			`INSERT INTO "godj_migrations" ("app", "name") VALUES (NULL, '0001')`,
		); err != nil {
			t.Fatalf("insert malformed record: %v", err)
		}
		records, err := backend.ReadAppliedMigrations(context.Background())
		if err == nil || records != nil {
			t.Fatalf("ReadAppliedMigrations() = (%v, %v), want nil/error", records, err)
		}
		if !strings.Contains(err.Error(), "scan SQLite applied migration") {
			t.Fatalf("error = %v, want scan classification", err)
		}
	})

	t.Run("closed backend", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		if err := backend.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		records, err := backend.ReadAppliedMigrations(context.Background())
		if err == nil || records != nil {
			t.Fatalf("ReadAppliedMigrations() = (%v, %v), want nil/error", records, err)
		}
	})

	t.Run("nil and canceled context", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		if records, err := backend.ReadAppliedMigrations(nil); err == nil || records != nil {
			t.Fatalf("ReadAppliedMigrations(nil) = (%v, %v), want nil/error", records, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		queryCount := backend.QueryCount()
		records, err := backend.ReadAppliedMigrations(ctx)
		if !errors.Is(err, context.Canceled) || records != nil {
			t.Fatalf("ReadAppliedMigrations(canceled) = (%v, %v), want nil/context.Canceled", records, err)
		}
		if got := backend.QueryCount(); got != queryCount {
			t.Fatalf("ORM QueryCount after canceled read = %d, want %d", got, queryCount)
		}
		if sqliteTableExists(t, backend, migrationRecorderTable) {
			t.Fatal("canceled read created recorder table")
		}
	})
}

func TestSQLiteReadAppliedMigrationsPreservesQueryIterationAndCloseErrors(t *testing.T) {
	queryFailure := errors.New("history query failure")
	backend := openMigrationHistoryFaultBackend(t, historyFault{queryErr: queryFailure})
	if records, err := backend.ReadAppliedMigrations(context.Background()); records != nil || !errors.Is(err, queryFailure) {
		t.Fatalf("query failure = (%v, %v), want nil/sentinel", records, err)
	}
	lookalike := errors.New("SQL logic error: no such table: godj_migrations (1)")
	backend = openMigrationHistoryFaultBackend(t, historyFault{queryErr: lookalike})
	if records, err := backend.ReadAppliedMigrations(context.Background()); records != nil || !errors.Is(err, lookalike) {
		t.Fatalf("unstructured missing-table lookalike = (%v, %v), want nil/sentinel", records, err)
	}
	foreignTaxonomy := &query.Error{Category: query.CategoryBackend, Code: query.CodeMissingTable}
	backend = openMigrationHistoryFaultBackend(t, historyFault{queryErr: foreignTaxonomy})
	if records, err := backend.ReadAppliedMigrations(context.Background()); records != nil || !errors.Is(err, foreignTaxonomy) {
		t.Fatalf("non-driver missing-table taxonomy = (%v, %v), want nil/sentinel", records, err)
	}

	iterationFailure := errors.New("history iteration failure")
	backend = openMigrationHistoryFaultBackend(t, historyFault{
		rows:    [][]driver.Value{{"alpha", "0001"}},
		nextErr: iterationFailure,
	})
	if records, err := backend.ReadAppliedMigrations(context.Background()); records != nil || !errors.Is(err, iterationFailure) {
		t.Fatalf("iteration failure = (%v, %v), want nil/sentinel", records, err)
	}

	closeFailure := errors.New("history close failure")
	backend = openMigrationHistoryFaultBackend(t, historyFault{closeErr: closeFailure})
	if records, err := backend.ReadAppliedMigrations(context.Background()); records != nil || !errors.Is(err, closeFailure) {
		t.Fatalf("close failure = (%v, %v), want nil/sentinel", records, err)
	}

	queryStarted := make(chan struct{})
	backend = openMigrationHistoryFaultBackend(t, historyFault{
		queryStarted:       queryStarted,
		waitForQueryCancel: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := backend.ReadAppliedMigrations(ctx)
		result <- err
	}()
	<-queryStarted
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight cancellation error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight canceled history read hung")
	}
}

func TestSQLiteReadAppliedMigrationsUsesOneReadOnlyDriverQuery(t *testing.T) {
	calls := &migrationHistoryDriverCalls{}
	backend := openMigrationHistoryFaultBackend(t, historyFault{
		rows:  [][]driver.Value{{"alpha", "0001"}},
		calls: calls,
	})

	records, err := backend.ReadAppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReadAppliedMigrations() error = %v", err)
	}
	want := []migrationbackend.AppliedMigration{{App: "alpha", Name: "0001"}}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("ReadAppliedMigrations() = %v, want %v", records, want)
	}

	got := calls.snapshot()
	if !reflect.DeepEqual(got.queries, []string{readAppliedMigrationsSQL}) {
		t.Fatalf("driver queries = %q, want exactly %q", got.queries, readAppliedMigrationsSQL)
	}
	if len(got.execs) != 0 {
		t.Fatalf("driver execs = %q, want none", got.execs)
	}
	if got.begins != 0 || got.commits != 0 || got.rollbacks != 0 {
		t.Fatalf(
			"driver transaction calls = begin:%d commit:%d rollback:%d, want all zero",
			got.begins,
			got.commits,
			got.rollbacks,
		)
	}
}

func TestSQLiteReadAppliedMigrationsRepeatedConcurrentAndCloseRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.sqlite3")
	backend := openMigrationHistoryFileBackend(t, path)
	want := []migrationbackend.AppliedMigration{
		{App: "alpha", Name: "0001"},
		{App: "alpha", Name: "0002"},
		{App: "beta", Name: "0001"},
	}
	recordAppliedMigrations(t, backend, want...)

	const readers = 32
	start := make(chan struct{})
	errorsByReader := make(chan error, readers)
	for range readers {
		go func() {
			<-start
			for range 20 {
				records, err := backend.ReadAppliedMigrations(context.Background())
				if err != nil {
					errorsByReader <- err
					return
				}
				if !reflect.DeepEqual(records, want) {
					errorsByReader <- fmt.Errorf("records = %v, want %v", records, want)
					return
				}
				records[0] = migrationbackend.AppliedMigration{App: "mutated", Name: "mutated"}
			}
			errorsByReader <- nil
		}()
	}
	close(start)
	for range readers {
		if err := <-errorsByReader; err != nil {
			t.Fatal(err)
		}
	}
	assertAppliedMigrations(t, backend, want...)

	start = make(chan struct{})
	type closeRaceResult struct {
		operation string
		records   []migrationbackend.AppliedMigration
		err       error
	}
	results := make(chan closeRaceResult, readers+1)
	for range readers {
		go func() {
			<-start
			records, err := backend.ReadAppliedMigrations(context.Background())
			results <- closeRaceResult{operation: "read", records: records, err: err}
		}()
	}
	go func() {
		<-start
		results <- closeRaceResult{operation: "close", err: backend.Close()}
	}()
	close(start)
	for range readers + 1 {
		select {
		case result := <-results:
			if result.operation == "close" && result.err != nil {
				t.Fatalf("concurrent Backend.Close() error = %v", result.err)
			}
			if result.operation == "read" {
				if result.err == nil && !reflect.DeepEqual(result.records, want) {
					t.Fatalf("successful concurrent read = %v, want %v", result.records, want)
				}
				if result.err != nil && result.records != nil {
					t.Fatalf("failed concurrent read = (%v, %v), want nil/error", result.records, result.err)
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent history read and Backend.Close hung")
		}
	}
	if records, err := backend.ReadAppliedMigrations(context.Background()); err == nil || records != nil {
		t.Fatalf("read after Close = (%v, %v), want nil/error", records, err)
	}
}

func recordAppliedMigrations(t *testing.T, backend *Backend, records ...migrationbackend.AppliedMigration) {
	t.Helper()
	transaction, err := backend.BeginMigration(context.Background())
	if err != nil {
		t.Fatalf("BeginMigration(): %v", err)
	}
	for _, record := range records {
		if err := transaction.RecordApplied(context.Background(), record.App, record.Name); err != nil {
			_ = transaction.Rollback(context.Background())
			t.Fatalf("RecordApplied(%s.%s): %v", record.App, record.Name, err)
		}
	}
	if err := transaction.Commit(context.Background()); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
}

func recordUnappliedMigration(t *testing.T, backend *Backend, record migrationbackend.AppliedMigration) {
	t.Helper()
	transaction, err := backend.BeginMigration(context.Background())
	if err != nil {
		t.Fatalf("BeginMigration(): %v", err)
	}
	if err := transaction.RecordUnapplied(context.Background(), record.App, record.Name); err != nil {
		_ = transaction.Rollback(context.Background())
		t.Fatalf("RecordUnapplied(%s.%s): %v", record.App, record.Name, err)
	}
	if err := transaction.Commit(context.Background()); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
}

func assertAppliedMigrations(t *testing.T, backend *Backend, want ...migrationbackend.AppliedMigration) []migrationbackend.AppliedMigration {
	t.Helper()
	queryCount := backend.QueryCount()
	records, err := backend.ReadAppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReadAppliedMigrations(): %v", err)
	}
	if !reflect.DeepEqual(records, want) && !(len(records) == 0 && len(want) == 0) {
		t.Fatalf("ReadAppliedMigrations() = %v, want %v", records, want)
	}
	if got := backend.QueryCount(); got != queryCount {
		t.Fatalf("ORM QueryCount after history read = %d, want %d", got, queryCount)
	}
	return records
}

func openMigrationHistoryFileBackend(t *testing.T, path string) *Backend {
	t.Helper()
	backend, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	return backend
}

const migrationHistoryFaultDriverName = "godj-migration-history-fault"

var (
	registerMigrationHistoryFaultDriver sync.Once
	migrationHistoryFaultSequence       atomic.Uint64
	migrationHistoryFaults              sync.Map
)

type historyFault struct {
	queryErr           error
	rows               [][]driver.Value
	nextErr            error
	closeErr           error
	queryStarted       chan struct{}
	waitForQueryCancel bool
	calls              *migrationHistoryDriverCalls
}

type migrationHistoryDriverCalls struct {
	mu        sync.Mutex
	queries   []string
	execs     []string
	begins    int
	commits   int
	rollbacks int
}

func (calls *migrationHistoryDriverCalls) recordQuery(statement string) {
	if calls == nil {
		return
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	calls.queries = append(calls.queries, statement)
}

func (calls *migrationHistoryDriverCalls) recordExec(statement string) {
	if calls == nil {
		return
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	calls.execs = append(calls.execs, statement)
}

func (calls *migrationHistoryDriverCalls) recordBegin() {
	if calls == nil {
		return
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	calls.begins++
}

func (calls *migrationHistoryDriverCalls) recordCommit() {
	if calls == nil {
		return
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	calls.commits++
}

func (calls *migrationHistoryDriverCalls) recordRollback() {
	if calls == nil {
		return
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	calls.rollbacks++
}

func (calls *migrationHistoryDriverCalls) snapshot() migrationHistoryDriverCalls {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	return migrationHistoryDriverCalls{
		queries:   append([]string(nil), calls.queries...),
		execs:     append([]string(nil), calls.execs...),
		begins:    calls.begins,
		commits:   calls.commits,
		rollbacks: calls.rollbacks,
	}
}

type migrationHistoryFaultDriver struct{}

func (migrationHistoryFaultDriver) Open(name string) (driver.Conn, error) {
	value, ok := migrationHistoryFaults.Load(name)
	if !ok {
		return nil, fmt.Errorf("unknown history fault DSN %q", name)
	}
	return &migrationHistoryFaultConnection{fault: value.(historyFault)}, nil
}

type migrationHistoryFaultConnection struct {
	fault historyFault
}

func (*migrationHistoryFaultConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("history fault driver does not support Prepare")
}

func (*migrationHistoryFaultConnection) Close() error { return nil }

func (connection *migrationHistoryFaultConnection) Begin() (driver.Tx, error) {
	connection.fault.calls.recordBegin()
	return &migrationHistoryFaultTransaction{calls: connection.fault.calls}, nil
}

func (connection *migrationHistoryFaultConnection) QueryContext(ctx context.Context, statement string, _ []driver.NamedValue) (driver.Rows, error) {
	connection.fault.calls.recordQuery(statement)
	if connection.fault.queryStarted != nil {
		close(connection.fault.queryStarted)
	}
	if connection.fault.waitForQueryCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if connection.fault.queryErr != nil {
		return nil, connection.fault.queryErr
	}
	return &migrationHistoryFaultRows{fault: connection.fault}, nil
}

func (connection *migrationHistoryFaultConnection) ExecContext(_ context.Context, statement string, _ []driver.NamedValue) (driver.Result, error) {
	connection.fault.calls.recordExec(statement)
	return driver.RowsAffected(0), nil
}

type migrationHistoryFaultTransaction struct {
	calls *migrationHistoryDriverCalls
}

func (transaction *migrationHistoryFaultTransaction) Commit() error {
	transaction.calls.recordCommit()
	return nil
}

func (transaction *migrationHistoryFaultTransaction) Rollback() error {
	transaction.calls.recordRollback()
	return nil
}

type migrationHistoryFaultRows struct {
	fault historyFault
	index int
}

func (*migrationHistoryFaultRows) Columns() []string { return []string{"app", "name"} }

func (rows *migrationHistoryFaultRows) Close() error { return rows.fault.closeErr }

func (rows *migrationHistoryFaultRows) Next(destination []driver.Value) error {
	if rows.index < len(rows.fault.rows) {
		copy(destination, rows.fault.rows[rows.index])
		rows.index++
		return nil
	}
	if rows.fault.nextErr != nil {
		err := rows.fault.nextErr
		rows.fault.nextErr = nil
		return err
	}
	return io.EOF
}

func openMigrationHistoryFaultBackend(t *testing.T, fault historyFault) *Backend {
	t.Helper()
	registerMigrationHistoryFaultDriver.Do(func() {
		sql.Register(migrationHistoryFaultDriverName, migrationHistoryFaultDriver{})
	})
	dsn := fmt.Sprintf("fault-%d", migrationHistoryFaultSequence.Add(1))
	migrationHistoryFaults.Store(dsn, fault)
	database, err := sql.Open(migrationHistoryFaultDriverName, dsn)
	if err != nil {
		t.Fatalf("open history fault database: %v", err)
	}
	backend := &Backend{database: database}
	t.Cleanup(func() {
		migrationHistoryFaults.Delete(dsn)
		if err := backend.Close(); err != nil {
			t.Errorf("close history fault backend: %v", err)
		}
	})
	return backend
}
