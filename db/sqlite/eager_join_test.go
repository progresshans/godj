package sqlite_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestSQLiteEagerFilterJoinCompositionMatchDjango(t *testing.T) {
	ctx := t.Context()
	backend, err := sqlite.OpenMemory(ctx, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE join_reference_person(id INTEGER PRIMARY KEY, name TEXT NOT NULL, nickname TEXT NULL, active BOOLEAN NOT NULL)`,
		`CREATE TABLE join_reference_post(id INTEGER PRIMARY KEY, title TEXT NOT NULL, author_id INTEGER NOT NULL REFERENCES join_reference_person(id), reviewer_id INTEGER NULL REFERENCES join_reference_person(id))`,
		`CREATE TABLE join_reference_comment(id INTEGER PRIMARY KEY, post_id INTEGER NOT NULL REFERENCES join_reference_post(id), body TEXT NOT NULL)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]any{{int64(1), "Ada", nil, true}, {int64(2), "Bob", "B", false}, {int64(3), "Cleo", "", true}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO join_reference_person VALUES(?,?,?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]any{{1, "keep", 1, nil}, {2, "drop", 2, nil}, {3, "keep", 1, 1}, {4, "drop", 2, 1}, {5, "keep", 1, 2}, {6, "drop", 2, 2}, {7, "keep", 3, 3}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO join_reference_post VALUES(?,?,?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]any{{int64(1), "match"}, {int64(1), "match"}, {int64(2), "match"}, {int64(3), "other"}, {int64(4), "match"}, {int64(4), "match"}, {int64(5), "match"}, {int64(7), "match"}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO join_reference_comment(post_id,body) VALUES(?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile("../../orm/testdata/eager-joins-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference nullableforwardproduct.EagerJoinReference
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 88 {
		t.Fatal("reference roster incomplete")
	}
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			plan, err := nullableforwardproduct.EagerJoinPlan(reference.Leaves, observation.EagerJoinInput)
			if err != nil {
				t.Fatal(err)
			}
			before := backend.QueryCount()
			count, first, rows, err := nullableforwardproduct.EvaluateEagerJoin(ctx, backend, plan)
			if err != nil || !reflect.DeepEqual(rows, observation.Rows) || !reflect.DeepEqual(first, observation.First) || count != observation.Count || backend.QueryCount() != before+uint64(len(observation.AllSQL)+len(observation.FirstSQL)+len(observation.CountSQL)) {
				t.Fatalf("rows=%v first=%v count=%d err=%v", rows, first, count, err)
			}
			statement, _, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatal(err)
			}
			// Combined filter/projection joins use observed semantics. Empty
			// queries are fully compiled before their I/O can be skipped.
			if len(observation.AllSQL) > 0 && strings.Count(statement, "LEFT OUTER JOIN") != strings.Count(observation.AllSQL[0], "LEFT OUTER JOIN") {
				t.Fatalf("JOIN semantics differ: %s", statement)
			}
		})
	}
	t.Run("valid self reference", func(t *testing.T) {
		for _, statement := range []string{`CREATE TABLE tree_node(id INTEGER PRIMARY KEY,name TEXT NOT NULL,parent_id INTEGER NULL REFERENCES tree_node(id))`, `INSERT INTO tree_node VALUES(1,'root',NULL),(2,'child',1),(3,'child',2),(4,'child',2)`} {
			if _, err := backend.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		before := backend.QueryCount()
		querytest.CheckSelfReferenceEagerRows(t, backend)
		if backend.QueryCount() != before+1 {
			t.Fatal("self projection performed extra SQL")
		}
	})

	for name, plan := range querytest.ConflictingEagerJoinPlans(t) {
		t.Run("invalid/"+name, func(t *testing.T) {
			before := backend.QueryCount()
			statement, args, err := sqlite.Compile(plan)
			if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || statement != "" || len(args) != 0 {
				t.Fatalf("invalid compilation=%q %v %v", statement, args, err)
			}
			rows, err := backend.Query(ctx, plan)
			if rows != nil {
				_ = rows.Close()
				t.Fatal("invalid projection returned rows")
			}
			if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || backend.QueryCount() != before {
				t.Fatalf("invalid query reached I/O: %v", err)
			}
		})
	}
}
