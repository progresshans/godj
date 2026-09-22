package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/conflicttest"
	"github.com/progresshans/godj/query"
)

func TestSQLiteConflictInsertCompiler(t *testing.T) {
	conflicttest.CheckCompiler(t, CompileConflictInsert,
		`INSERT INTO "conflict_link" ("owner_id", "label_id", "token") VALUES (?, ?, ?) ON CONFLICT ("owner_id", "label_id") DO NOTHING`)
	field := query.NewFieldRef("name", `a"column`, query.FieldString, false)
	statement, arguments, err := CompileConflictInsert(query.NewConflictInsertPlan(`a"table`, []query.Assignment{query.NewAssignment(field, query.String("x'); DROP TABLE links; --"))}, []query.FieldRef{field}))
	if err != nil || statement != `INSERT INTO "a""table" ("a""column") VALUES (?) ON CONFLICT ("a""column") DO NOTHING` ||
		!reflect.DeepEqual(arguments, []any{"x'); DROP TABLE links; --"}) {
		t.Fatal(statement, arguments, err)
	}
	upper := query.NewFieldRef("upper", "KEY", query.FieldInteger, false)
	lower := query.NewFieldRef("lower", "key", query.FieldInteger, false)
	if _, _, err := CompileConflictInsert(query.NewConflictInsertPlan("links", []query.Assignment{query.NewAssignment(upper, query.Integer(1)), query.NewAssignment(lower, query.Integer(2))}, []query.FieldRef{upper, lower})); err == nil {
		t.Fatal("SQLite case-equivalent columns accepted")
	}
}

func TestSQLiteConflictInsertProduct(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.sqlite")
	backend := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	second := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	for _, statement := range []string{
		`CREATE TABLE conflict_parent (id INTEGER PRIMARY KEY)`,
		`INSERT INTO conflict_parent VALUES (1),(2),(3),(4),(5),(6)`,
		`CREATE TABLE conflict_link (id INTEGER PRIMARY KEY AUTOINCREMENT, owner_id INTEGER NOT NULL REFERENCES conflict_parent(id) DEFERRABLE INITIALLY DEFERRED, label_id INTEGER NOT NULL REFERENCES conflict_parent(id), token INTEGER NOT NULL UNIQUE CHECK(token > 0), UNIQUE(owner_id,label_id))`,
	} {
		if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	conflicttest.Check(t, backend, second, backend.database, "conflict_link", "conflict_parent")
	t.Run("zero_rows_is_not_membership_proof", func(t *testing.T) {
		if _, err := backend.database.ExecContext(t.Context(), `CREATE TRIGGER skip_conflict BEFORE INSERT ON conflict_link WHEN NEW.token=90909 BEGIN SELECT RAISE(IGNORE); END`); err != nil {
			t.Fatal(err)
		}
		plan := conflicttest.Plan(4, 4, 90909)
		if inserted, err := backend.InsertOnConflict(t.Context(), plan); inserted || err != nil {
			t.Fatal(inserted, err)
		}
		var count int
		if err := backend.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM conflict_link WHERE token=90909`).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
		if key, err := backend.Insert(t.Context(), query.NewInsertPlanReturningKey("conflict_link", plan.Assignments(), query.NewFieldRef("id", "id", query.FieldInteger, false))); key != 0 || !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) {
			t.Fatal("ordinary insert contract changed", key, err)
		}
	})
	if err := errors.Join(backend.Close(), second.Close()); err != nil {
		t.Fatal(err)
	}
	reopened := openLifecycleFileBackend(t, path, "")
	var count int
	if err := reopened.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM conflict_link`).Scan(&count); err != nil || count != 1 {
		t.Fatal("reopened link state", count, err)
	}
}

func TestSQLiteConflictInsertErrorAndUncertainTransactionOwnership(t *testing.T) {
	for _, outcome := range []string{"driver", "rollback", "rollback_discarded", "commit", "invalid"} {
		t.Run(outcome, func(t *testing.T) {
			failure := errors.New("injected native failure")
			callbackFailure := errors.New("rollback after insert")
			connection := &relationFaultConnection{exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if (outcome == "driver" && strings.HasPrefix(statement, "INSERT")) || (strings.HasPrefix(outcome, "rollback") && statement == "ROLLBACK") || (outcome == "commit" && statement == "COMMIT") {
					return nil, failure
				}
				return relationFaultResult(1), nil
			}}
			if outcome == "rollback" {
				connection.raw = func(func(any) error) error { return nil }
			}
			retention := newRelationRetentionState()
			defer retention.sealAndDrain()
			calls := 0
			err := executeCoordinatedAtomic(context.Background(), func(session db.Session) error {
				calls++
				plan := conflicttest.Plan(1, 1, 10)
				if outcome == "invalid" {
					plan = query.ConflictInsertPlan{}
				}
				inserted, err := session.(db.ConflictInserter).InsertOnConflict(context.Background(), plan)
				if outcome == "driver" || outcome == "invalid" {
					if inserted || err == nil {
						t.Fatal("write failed open", inserted, err)
					}
					return err
				}
				if !inserted || err != nil {
					t.Fatal(inserted, err)
				}
				if strings.HasPrefix(outcome, "rollback") {
					return callbackFailure
				}
				return nil
			}, connection, retention, nil)
			if calls != 1 {
				t.Fatal("callback retried", calls)
			}
			if outcome == "invalid" {
				if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || len(connection.statementSnapshot()) != 2 {
					t.Fatal("invalid plan reached I/O", err, connection.statementSnapshot())
				}
			} else if !errors.Is(err, failure) {
				t.Fatal("native cause lost", err)
			}
			if outcome == "commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("commit uncertainty lost", err)
			}
			if outcome == "rollback" && (!errors.Is(err, callbackFailure) || !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})) {
				t.Fatal("rollback uncertainty lost", err)
			}
			if outcome == "rollback" && (retainedConnectionCount(retention) != 1 || connection.closeCalls.Load() != 0 || !errors.Is(err, &query.Error{Code: query.CodeBackendRecoveryRequired})) {
				t.Fatal("unconfirmed connection returned to pool", err)
			}
			if outcome == "rollback_discarded" && (!errors.Is(err, callbackFailure) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})) {
				t.Fatal("confirmed discard lost error or became unknown", err)
			}
			insertCount := 0
			for _, statement := range connection.statementSnapshot() {
				if strings.HasPrefix(statement, "INSERT") {
					insertCount++
				}
			}
			if outcome != "invalid" && insertCount != 1 {
				t.Fatal("insert retried", insertCount)
			}
		})
	}
}
