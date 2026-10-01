package sqlite_test

import (
	"errors"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"strings"
	"sync"
	"testing"
)

func TestSQLiteNestedForwardPathsMatchDjango(t *testing.T) {
	backend, err := sqlite.OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	querytest.CreateNestedFixture(t, func(name string) string { return name }, "DATETIME", func(statement string) error { _, err := backend.ExecContext(t.Context(), statement); return err })
	querytest.CheckNestedReference(t, backend, sqlite.Compile, func() (uint64, uint64) { count := backend.QueryCount(); return count, count })
}

func TestSQLiteNestedForwardJoinBudgetAndConcurrentCompilation(t *testing.T) {
	backend, err := sqlite.OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	identity := ir.ModelIdentity{AppLabel: "tree", ModelName: "node"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	parent := query.NewFieldRef("parent", "parent_id", query.FieldInteger, true)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	direct, err := query.NewForwardRelationPath(identity, "tree_node", "parent", "parent_id", identity, "tree_node", "id", true, name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	hops := make([]query.RelationHop, 64)
	for i := range hops {
		hops[i] = direct.Hops()[0]
	}
	build := func(length int, scope query.RelationTerminalScope) query.Plan {
		terminal, lookup, value := name, query.LookupExact, query.String("root")
		if scope == query.RelationTerminalSourceKey {
			terminal, lookup, value = parent, query.LookupIsNull, query.Boolean(false)
		}
		path, err := query.NewForwardRelationChain(hops[:length], terminal, scope)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("tree_node", []query.FieldRef{id, parent, name}).WithConditions(query.NewRelatedCondition(path, lookup, value))
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	valid := build(63, query.RelationTerminalRelatedField)
	wantSQL, wantArgs, err := sqlite.Compile(valid)
	if err != nil || strings.Count(wantSQL, " JOIN ") != 63 {
		t.Fatalf("63 joins: %v", err)
	}
	trimmed := build(64, query.RelationTerminalSourceKey)
	statement, _, err := sqlite.Compile(trimmed)
	if err != nil || strings.Count(statement, " JOIN ") != 63 {
		t.Fatalf("64 hops trimmed to 63 joins: %v", err)
	}
	invalid := build(64, query.RelationTerminalRelatedField)
	zero, err := invalid.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	before := backend.QueryCount()
	for _, plan := range []query.Plan{invalid, zero} {
		statement, args, err := sqlite.Compile(plan)
		if statement != "" || len(args) != 0 || !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatalf("over budget compile=%q,%v,%v", statement, args, err)
		}
		rows, err := backend.Query(t.Context(), plan)
		if rows != nil {
			_ = rows.Close()
			t.Fatal("over budget returned rows")
		}
		if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || backend.QueryCount() != before {
			t.Fatalf("over budget I/O: %v", err)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			statement, args, err := sqlite.Compile(valid)
			if err != nil || statement != wantSQL || len(args) != len(wantArgs) || args[0] != wantArgs[0] {
				t.Error("shared route compilation changed", err)
			}
			if len(args) > 0 {
				args[0] = "private mutation"
			}
		})
	}
	wg.Wait()
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE tree_node(id INTEGER PRIMARY KEY,parent_id INTEGER REFERENCES tree_node(id),name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(t.Context(), `INSERT INTO tree_node VALUES(1,1,'root')`); err != nil {
		t.Fatal(err)
	}
	for _, plan := range []query.Plan{valid, trimmed} {
		rows, err := backend.Query(t.Context(), plan)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("bounded cyclic query lost row")
		}
		var gotID, gotParent int64
		var gotName string
		if err := rows.Scan(&gotID, &gotParent, &gotName); err != nil || gotID != 1 || gotParent != 1 || gotName != "root" {
			t.Fatal("bounded route scan", err)
		}
		if rows.Next() {
			t.Fatal("bounded route duplicated row")
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
	}
}
