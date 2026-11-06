package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/insertgraphtest"
	"github.com/progresshans/godj/query"
)

func sqliteInsertGraphFixture(t *testing.T) (*Backend, *Backend) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.sqlite")
	backend := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	observer := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	for _, names := range [][2]string{{"graph_left", "graph_right"}, {"graph_right", "graph_left"}, {"graph_self", "graph_self"}} {
		statement := "CREATE TABLE " + names[0] + " (record_key INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, " +
			"peer_key INTEGER NOT NULL REFERENCES " + names[1] + "(record_key), token TEXT NOT NULL UNIQUE CHECK(token <> ''), optional_value INTEGER)"
		if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return backend, observer
}

func TestSQLiteNativeInsertGraphAndScope(t *testing.T) {
	insertgraphtest.Check(t, func(t *testing.T) insertgraphtest.Fixture {
		backend, observer := sqliteInsertGraphFixture(t)
		return insertgraphtest.Fixture{Backend: backend, Observer: observer}
	})
}

func sqliteRequireNoGraphObjects(t *testing.T, executor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) {
	t.Helper()
	rows, err := executor.QueryContext(t.Context(), `SELECT name FROM temp.sqlite_schema WHERE name LIKE 'godj_graph_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var name string
		_ = rows.Scan(&name)
		t.Fatal("graph leaked a temporary object", name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteInsertGraphNativeGuards(t *testing.T) {
	for _, condition := range []string{"foreign_keys_disabled", "connection_deferral", "temporary_shadow", "non_auto_key", "value_limit", "wrong_scalar"} {
		t.Run(condition, func(t *testing.T) {
			backend, observer := sqliteInsertGraphFixture(t)
			backend.database.SetMaxOpenConns(1)
			switch condition {
			case "foreign_keys_disabled":
				if _, err := backend.database.ExecContext(t.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
					t.Fatal(err)
				}
			case "temporary_shadow":
				if _, err := backend.database.ExecContext(t.Context(), "CREATE TEMP TABLE graph_self (record_key INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, peer_key INTEGER, token TEXT, optional_value INTEGER)"); err != nil {
					t.Fatal(err)
				}
			case "non_auto_key":
				for _, statement := range []string{"DROP TABLE graph_self", "CREATE TABLE graph_self (record_key INTEGER NOT NULL PRIMARY KEY, peer_key INTEGER, token TEXT, optional_value INTEGER)"} {
					if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := backend.Atomic(t.Context(), func(session db.Session) error {
				native := session.(*transactionSession)
				if condition == "connection_deferral" {
					if _, err := native.transaction.ExecContext(t.Context(), "PRAGMA defer_foreign_keys=ON"); err != nil {
						t.Fatal(err)
					}
				}
				insert := insertgraphtest.Insert("graph_self", insertgraphtest.Row{Key: 3, Peer: 3, Token: "kept_out"})
				if condition == "value_limit" {
					assignments := []query.Assignment{query.NewAssignment(insertgraphtest.Key, query.Integer(3))}
					for index := 1; index <= sqliteInsertGraphParameters; index++ {
						field := query.NewFieldRef(fmt.Sprintf("value_%d", index), fmt.Sprintf("value_%d", index), query.FieldInteger, false)
						assignments = append(assignments, query.NewAssignment(field, query.Integer(1)))
					}
					insert = query.NewInsertPlanReturningKey("graph_self", assignments, insertgraphtest.Key)
				} else if condition == "wrong_scalar" {
					assignments := insert.Assignments()
					assignments[0] = query.NewAssignment(insertgraphtest.Token, query.Integer(33))
					insert = query.NewInsertPlanReturningKey("graph_self", assignments, insertgraphtest.Key)
				}
				keys, err := session.(db.GraphInserter).InsertGraph(t.Context(), insertgraphtest.Plan(t, insert))
				if err == nil || keys != nil {
					t.Fatal("invalid graph reached storage", keys, err)
				}
				sqliteRequireNoGraphObjects(t, native.transaction)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			insertgraphtest.RequireRows(t, observer, "graph_self", nil)
		})
	}
}

func TestSQLiteInsertGraphTriggerResultsAndCleanup(t *testing.T) {
	for _, behavior := range []string{"ignore", "replace_key", "delete_after_insert", "fail_after_previous_row", "ordinary_side_effect"} {
		t.Run(behavior, func(t *testing.T) {
			backend, observer := sqliteInsertGraphFixture(t)
			backend.database.SetMaxOpenConns(1)
			var trigger string
			switch behavior {
			case "ignore":
				trigger = "CREATE TRIGGER graph_trigger BEFORE INSERT ON graph_self BEGIN SELECT RAISE(IGNORE); END"
			case "replace_key":
				trigger = "CREATE TRIGGER graph_trigger AFTER INSERT ON graph_self BEGIN UPDATE graph_self SET record_key=record_key+100,peer_key=peer_key+100 WHERE record_key=NEW.record_key; END"
			case "delete_after_insert":
				trigger = "CREATE TRIGGER graph_trigger AFTER INSERT ON graph_self BEGIN DELETE FROM graph_self WHERE record_key=NEW.record_key; END"
			case "fail_after_previous_row":
				trigger = "CREATE TRIGGER graph_trigger BEFORE INSERT ON graph_self WHEN NEW.token='second' BEGIN SELECT RAISE(FAIL, 'injected graph failure'); END"
			case "ordinary_side_effect":
				for _, statement := range []string{"CREATE TABLE graph_log (id INTEGER PRIMARY KEY AUTOINCREMENT, token TEXT)", "INSERT INTO graph_log(id,token) VALUES(100,'unrelated')"} {
					if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
				trigger = "CREATE TRIGGER graph_trigger AFTER INSERT ON graph_self BEGIN INSERT INTO graph_log(token) VALUES(NEW.token); END"
			}
			if _, err := backend.database.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			var expected []insertgraphtest.Row
			if err := backend.Atomic(t.Context(), func(session db.Session) error {
				requested := insertgraphtest.Reserve(t, session, "graph_self", 2)
				first := insertgraphtest.Row{Key: requested[0], Peer: requested[0], Token: "first"}
				second := insertgraphtest.Row{Key: requested[1], Peer: requested[1], Token: "second"}
				keys, err := session.(db.GraphInserter).InsertGraph(t.Context(), insertgraphtest.Plan(t,
					insertgraphtest.Insert("graph_self", first), insertgraphtest.Insert("graph_self", second)))
				if behavior == "ordinary_side_effect" {
					if err != nil || len(keys) != 2 || keys[0] != first.Key || keys[1] != second.Key {
						t.Fatal("trigger's rowid leaked into graph result", keys, err)
					}
					expected = []insertgraphtest.Row{first, second}
					var count int
					if err := session.(*transactionSession).transaction.QueryRowContext(t.Context(), "SELECT count(*) FROM graph_log WHERE (id=100 AND token='unrelated') OR (id=101 AND token='first') OR (id=102 AND token='second')").Scan(&count); err != nil || count != 3 {
						t.Fatal("native trigger side effects did not run", count, err)
					}
				} else if err == nil || keys != nil {
					t.Fatal("changed or suppressed INSERT was accepted", keys, err)
				}
				insertgraphtest.RequireRows(t, session, "graph_self", expected)
				sqliteRequireNoGraphObjects(t, session.(*transactionSession).transaction)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			insertgraphtest.RequireRows(t, observer, "graph_self", expected)
			sqliteRequireNoGraphObjects(t, backend.database)
		})
	}
}

func TestSQLiteInsertGraphCompilationKeepsValuesOutOfDDL(t *testing.T) {
	const secret = "secret'\"; DROP TABLE graph_self;--"
	plan := insertgraphtest.Plan(t, insertgraphtest.Insert("graph_self", insertgraphtest.Row{Key: -3, Peer: -3, Token: secret}))
	compiled, err := compileInsertGraph(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{compiled.createInput, compiled.createReceipts, compiled.createTrigger, compiled.statement, compiled.readback} {
		if strings.Contains(statement, secret) || strings.Contains(statement, "defer_foreign_keys") || strings.Contains(statement, "foreign_keys=OFF") {
			t.Fatal("graph embedded values or changed FK enforcement", statement)
		}
	}
	if len(compiled.arguments) != 4 || compiled.arguments[0] != secret || len(compiled.readKeys) != 1 || compiled.readKeys[0] != int64(-3) {
		t.Fatal("graph lost parameter ownership", compiled.arguments, compiled.readKeys)
	}
}
