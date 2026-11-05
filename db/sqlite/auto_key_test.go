package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/autokeytest"
	"github.com/progresshans/godj/query"
)

const sqliteAutoKeyTable = `CREATE TABLE reserved_keys (record_key INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, token BIGINT NOT NULL UNIQUE CHECK(token > 0))`

func TestSQLiteNativeAutoKeyReservationAndScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reservation.sqlite")
	backend := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	second := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	if _, err := backend.database.ExecContext(t.Context(), sqliteAutoKeyTable); err != nil {
		t.Fatal(err)
	}
	autokeytest.Check(t, backend, second, backend.database, "reserved_keys", false)
}

func sqliteAutoKeySequenceSnapshot(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), `SELECT quote(rowid)||':'||quote(name)||':'||typeof(name)||':'||quote(seq)||':'||typeof(seq) FROM main.sqlite_sequence ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSQLiteAutoKeyPhysicalAllocatorValidation(t *testing.T) {
	for _, test := range []struct{ name, ddl string }{
		{"no_autoincrement", `CREATE TABLE reserved_keys(record_key INTEGER NOT NULL PRIMARY KEY, token BIGINT)`},
		{"nullable_declaration", `CREATE TABLE reserved_keys(record_key INTEGER PRIMARY KEY AUTOINCREMENT, token BIGINT)`},
		{"wrong_type", `CREATE TABLE reserved_keys(record_key BIGINT NOT NULL PRIMARY KEY, token BIGINT)`},
		{"composite_key", `CREATE TABLE reserved_keys(record_key INTEGER NOT NULL, token BIGINT NOT NULL, PRIMARY KEY(record_key,token))`},
		{"default_key", `CREATE TABLE reserved_keys(record_key INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT DEFAULT 9, token BIGINT)`},
		{"without_rowid", `CREATE TABLE reserved_keys(record_key INTEGER NOT NULL PRIMARY KEY, token BIGINT) WITHOUT ROWID`},
		{"literal_keyword", `CREATE TABLE reserved_keys(record_key INTEGER NOT NULL PRIMARY KEY, token TEXT DEFAULT 'PRIMARY KEY AUTOINCREMENT')`},
		{"quoted_keyword", `CREATE TABLE reserved_keys(record_key INTEGER NOT NULL PRIMARY KEY, "PRIMARY" TEXT, "KEY" TEXT, "AUTOINCREMENT" TEXT)`},
		{"comment_keyword", `CREATE TABLE reserved_keys(record_key INTEGER NOT NULL PRIMARY KEY /* AUTOINCREMENT */, token TEXT)`},
		{"view", `CREATE VIEW reserved_keys AS SELECT 1 AS record_key, 1 AS token`},
		{"wrong_key", `CREATE TABLE reserved_keys(other_key INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, record_key INTEGER NOT NULL)`},
		{"case_key", `CREATE TABLE reserved_keys(RECORD_KEY INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, token BIGINT)`},
		{"case_table", `CREATE TABLE RESERVED_KEYS(record_key INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, token BIGINT)`},
		{"missing", `CREATE TABLE unrelated(record_key INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "physical.sqlite"), "")
			for _, statement := range []string{`CREATE TABLE carrier(id INTEGER PRIMARY KEY AUTOINCREMENT)`, test.ddl} {
				if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			before := sqliteAutoKeySequenceSnapshot(t, backend.database)
			err := backend.Atomic(t.Context(), func(session db.Session) error {
				keys, err := session.(db.AutoKeyAllocator).ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 1))
				if keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("physical drift accepted", keys, err)
				}
				return err
			})
			if err == nil || !reflect.DeepEqual(before, sqliteAutoKeySequenceSnapshot(t, backend.database)) {
				t.Fatal("rejected metadata changed allocator", err)
			}
		})
	}
	t.Run("temporary_shadow", func(t *testing.T) {
		backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "shadow.sqlite"), "")
		backend.database.SetMaxOpenConns(1)
		for _, statement := range []string{sqliteAutoKeyTable, `CREATE TEMP TABLE reserved_keys(record_key INTEGER PRIMARY KEY)`} {
			if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
		err := backend.Atomic(t.Context(), func(session db.Session) error {
			keys, err := session.(db.AutoKeyAllocator).ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 1))
			if keys != nil {
				t.Fatal("shadowed table returned keys", keys)
			}
			return err
		})
		if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal(err)
		}
	})
}

func TestSQLiteAutoKeyHighWaterAndExhaustion(t *testing.T) {
	for _, test := range []struct {
		name       string
		statements []string
		want       []int64
	}{
		{"new", nil, []int64{1, 2}},
		{"negative_row", []string{`INSERT INTO reserved_keys VALUES(-9,1)`}, []int64{1, 2}},
		{"deleted_high_key", []string{`INSERT INTO reserved_keys VALUES(100,1)`, `DELETE FROM reserved_keys`}, []int64{101, 102}},
		{"updated_primary_key", []string{`INSERT INTO reserved_keys VALUES(1,1)`, `UPDATE reserved_keys SET record_key=250`}, []int64{251, 252}},
		{"reserved_high_water", []string{`INSERT INTO sqlite_sequence(name,seq) VALUES('reserved_keys',9007199254740993)`}, []int64{9007199254740994, 9007199254740995}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "highwater.sqlite")
			backend := openLifecycleFileBackend(t, path, "")
			for _, statement := range append([]string{sqliteAutoKeyTable}, test.statements...) {
				if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			if err := backend.Atomic(t.Context(), func(session db.Session) error {
				keys, err := session.(db.AutoKeyAllocator).ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 2))
				if err != nil || !slices.Equal(keys, test.want) {
					t.Fatal(keys, test.want, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := openLifecycleFileBackend(t, path, "")
			key, err := reopened.Insert(t.Context(), query.NewInsertPlanReturningKey("reserved_keys", []query.Assignment{query.NewAssignment(autokeytest.Token, query.Integer(2))}, autokeytest.Key))
			if err != nil || key != test.want[1]+1 {
				t.Fatal("committed reservation did not survive reopen", key, err)
			}
		})
	}
	t.Run("exhaustion_never_wraps_or_partially_reserves", func(t *testing.T) {
		backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "exhausted.sqlite"), "")
		for _, statement := range []string{sqliteAutoKeyTable, `INSERT INTO sqlite_sequence(name,seq) VALUES('reserved_keys',9223372036854775806)`} {
			if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := backend.Atomic(t.Context(), func(session db.Session) error {
			allocator := session.(db.AutoKeyAllocator)
			if keys, err := allocator.ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 2)); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeAutoKeyExhausted}) {
				t.Fatal(keys, err)
			}
			if keys, err := allocator.ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 1)); err != nil || !slices.Equal(keys, []int64{9223372036854775807}) {
				t.Fatal("failed batch consumed a partial interval", keys, err)
			}
			if keys, err := allocator.ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 1)); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeAutoKeyExhausted}) {
				t.Fatal("exhausted allocator wrapped", keys, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.database.ExecContext(t.Context(), `INSERT INTO reserved_keys(token) VALUES(1)`); err == nil {
			t.Fatal("native INSERT reused exhausted reserved keys")
		}
	})
}

func TestSQLiteAutoKeyRejectsCorruptSequence(t *testing.T) {
	for _, test := range []struct{ name, value string }{
		{"negative", `('reserved_keys',-1)`}, {"null", `('reserved_keys',NULL)`},
		{"text", `('reserved_keys','10')`}, {"real", `('reserved_keys',10.5)`},
		{"blob_name", `(CAST('reserved_keys' AS BLOB),10)`}, {"case_name", `('RESERVED_KEYS',10)`},
		{"duplicate", `('reserved_keys',10),('reserved_keys',11)`}, {"case_duplicate", `('reserved_keys',10),('RESERVED_KEYS',11)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "corrupt.sqlite"), "")
			for _, statement := range []string{sqliteAutoKeyTable, `INSERT INTO sqlite_sequence(name,seq) VALUES` + test.value} {
				if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			before := sqliteAutoKeySequenceSnapshot(t, backend.database)
			err := backend.Atomic(t.Context(), func(session db.Session) error {
				keys, err := session.(db.AutoKeyAllocator).ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 2))
				if keys != nil || err == nil {
					t.Fatal("corrupt sequence produced keys", keys, err)
				}
				return err
			})
			if err == nil || !reflect.DeepEqual(before, sqliteAutoKeySequenceSnapshot(t, backend.database)) {
				t.Fatal("corrupt allocator was changed", err)
			}
		})
	}
}

func TestSQLiteAutoKeyIOFailureCannotPublishKeys(t *testing.T) {
	for _, mode := range []string{"metadata", "allocation", "allocation_nil_rows", "allocation_wrong_endpoint", "late_cancel"} {
		t.Run(mode, func(t *testing.T) {
			backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "fault.sqlite"), "")
			if _, err := backend.database.ExecContext(t.Context(), sqliteAutoKeyTable); err != nil {
				t.Fatal(err)
			}
			transaction, err := backend.database.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Rollback()
			failure := errors.New("injected reservation I/O")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls, mutations := 0, 0
			executor := &relationFaultConnection{query: func(work context.Context, statement string, arguments []any) (*sql.Rows, error) {
				calls++
				if mode == "metadata" {
					return nil, failure
				}
				if strings.HasPrefix(statement, "INSERT INTO main.sqlite_sequence") {
					switch mode {
					case "allocation":
						return nil, failure
					case "allocation_nil_rows":
						return nil, nil
					case "allocation_wrong_endpoint":
						return transaction.QueryContext(work, "SELECT 777")
					case "late_cancel":
						rows, err := transaction.QueryContext(work, statement, arguments...)
						cancel()
						return rows, err
					}
				}
				return transaction.QueryContext(work, statement, arguments...)
			}}
			keys, err := reserveSQLiteAutoKeys(ctx, executor, autokeytest.Plan(t, 2), func() { mutations++ })
			if keys != nil || err == nil || (mode == "metadata" && (calls != 1 || mutations != 0)) || (mode != "metadata" && mutations != 1) {
				t.Fatal("failure published keys or lost mutation ownership", keys, err, calls, mutations)
			}
			if (mode == "metadata" || mode == "allocation") && !errors.Is(err, failure) {
				t.Fatal("native failure cause lost", err)
			}
			if mode == "late_cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("late cancellation lost", err)
			}
			if err := transaction.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Fatal(err)
			}
			if len(sqliteAutoKeySequenceSnapshot(t, backend.database)) != 0 {
				t.Fatal("failed transaction persisted a reservation")
			}
		})
	}
}

func TestSQLiteAutoKeyWriterConflictDoesNotRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.sqlite")
	backend := openLifecycleFileBackend(t, path, "")
	second := openLifecycleFileBackend(t, path, "")
	if _, err := backend.database.ExecContext(t.Context(), sqliteAutoKeyTable); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("release first allocator")
	err := backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
		if _, err := session.(db.AutoKeyAllocator).ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 2)); err != nil {
			return err
		}
		calls := 0
		err := second.Atomic(t.Context(), func(other db.Session) error {
			calls++
			keys, err := other.(db.AutoKeyAllocator).ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 2))
			if keys != nil || err == nil {
				t.Fatal("concurrent writer returned keys", keys, err)
			}
			return err
		})
		var native *modernsqlite.Error
		if calls != 1 || !errors.As(err, &native) || native.Code()&0xff != sqlite3.SQLITE_BUSY {
			t.Fatal("native BUSY or one callback contract lost", calls, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) || len(sqliteAutoKeySequenceSnapshot(t, backend.database)) != 0 {
		t.Fatal("failed allocator owners left a durable reservation", err)
	}
}

func TestSQLiteAutoKeyUnconfirmedTransactionKeepsOwnership(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback"} {
		t.Run(outcome, func(t *testing.T) {
			backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "uncertain.sqlite"), "")
			if _, err := backend.database.ExecContext(t.Context(), sqliteAutoKeyTable); err != nil {
				t.Fatal(err)
			}
			native, err := backend.database.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_, _ = native.ExecContext(context.Background(), "ROLLBACK")
				_ = native.Close()
			}()
			failure, callbackFailure := errors.New("native control failure"), errors.New("owner failed after allocation")
			writes := 0
			connection := &relationFaultConnection{
				exec: func(ctx context.Context, statement string, arguments []any) (sql.Result, error) {
					if statement == "ROLLBACK" || (outcome == "commit" && statement == "COMMIT") {
						return nil, failure
					}
					return native.ExecContext(ctx, statement, arguments...)
				},
				query: func(ctx context.Context, statement string, arguments []any) (*sql.Rows, error) {
					if strings.HasPrefix(statement, "INSERT INTO main.sqlite_sequence") {
						writes++
					}
					return native.QueryContext(ctx, statement, arguments...)
				},
				raw: func(func(any) error) error { return nil }, // No confirmed discard.
			}
			retention := newRelationRetentionState()
			defer retention.sealAndDrain()
			calls := 0
			var retained db.AutoKeyAllocator
			err = executeAtomicRelation(t.Context(), func(session db.RelationSession) error {
				calls++
				retained = session.(db.AutoKeyAllocator)
				keys, err := retained.ReserveAutoKeys(t.Context(), autokeytest.Plan(t, 2))
				if err != nil || !slices.Equal(keys, []int64{1, 2}) || !session.(*relationSession).state.mutationPossible.Load() {
					t.Fatal("reservation did not register native mutation", keys, err)
				}
				if outcome == "rollback" {
					return callbackFailure
				}
				return nil
			}, connection, retention, nil)
			want := query.CodeTransactionOutcomeUnknown
			if outcome == "commit" {
				want = query.CodeCommitOutcomeUnknown
			}
			if calls != 1 || writes != 1 || !errors.Is(err, failure) || !errors.Is(err, &query.Error{Code: want}) || !errors.Is(err, &query.Error{Code: query.CodeBackendRecoveryRequired}) || retainedConnectionCount(retention) != 1 || connection.closeCalls.Load() != 0 {
				t.Fatal("unconfirmed allocator was retried, returned to pool, or reported success", calls, writes, err)
			}
			if outcome == "rollback" && !errors.Is(err, callbackFailure) {
				t.Fatal("owner failure cause lost", err)
			}
			if keys, err := retained.ReserveAutoKeys(context.Background(), autokeytest.Plan(t, 1)); keys != nil || err == nil {
				t.Fatal("uncertain owner leaked a live handle", keys, err)
			}
		})
	}
}

func TestSQLiteAutoKeyReservesTheWholeNativeOperation(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "operation.sqlite"), "")
	if _, err := backend.database.ExecContext(t.Context(), sqliteAutoKeyTable); err != nil {
		t.Fatal(err)
	}
	transaction, err := backend.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	entered, release := make(chan struct{}), make(chan struct{})
	var hold sync.Once
	connection := &relationFaultConnection{
		exec: func(ctx context.Context, statement string, arguments []any) (sql.Result, error) {
			return transaction.ExecContext(ctx, statement, arguments...)
		},
		query: func(ctx context.Context, statement string, arguments []any) (*sql.Rows, error) {
			var interrupted error
			if strings.HasPrefix(statement, "INSERT INTO main.sqlite_sequence") {
				hold.Do(func() {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						interrupted = ctx.Err()
					}
				})
			}
			if interrupted != nil {
				return nil, interrupted
			}
			return transaction.QueryContext(ctx, statement, arguments...)
		},
	}
	session := newRelationSession(t.Context(), connection, nil)
	defer session.scope.Finish()
	autokeytest.CheckInFlightReservation(t, session, entered, sync.OnceFunc(func() { close(release) }))
}
