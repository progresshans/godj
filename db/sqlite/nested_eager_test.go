package sqlite_test

import (
	"errors"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestSQLiteNestedEagerGraphsMatchDjango(t *testing.T) {
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
	querytest.CheckNestedEagerReference(t, backend, sqlite.Compile, func() (uint64, uint64) { count := backend.QueryCount(); return count, count })
	querytest.CheckNestedEagerInvalidPlans(t, backend, sqlite.Compile, func() (uint64, uint64) { count := backend.QueryCount(); return count, count })
}

func TestSQLiteNestedEagerProjectionJoinBudget(t *testing.T) {
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
	fields := []query.FieldRef{id, parent, name}
	direct, err := query.NewForwardRelationProjection(identity, "tree_node", parent, identity, "tree_node", id, fields)
	if err != nil {
		t.Fatal(err)
	}
	projections := make([]query.RelationProjection, 64)
	var hops []query.RelationHop
	for i := range projections {
		hops = append(hops, direct.TerminalHop())
		projections[i], err = query.NewForwardChainProjection(hops, id, fields)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := query.NewPlan("tree_node", fields)
	accepted, err := base.WithRelationProjections(projections[:63]...)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := base.WithRelationProjections(projections...)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := rejected.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	before := backend.QueryCount()
	for _, plan := range []query.Plan{rejected, empty} {
		rows, err := backend.Query(t.Context(), plan)
		if rows != nil {
			_ = rows.Close()
			t.Fatal("over-limit projection returned rows")
		}
		if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || backend.QueryCount() != before {
			t.Fatalf("over-limit graph reached SQL: %v", err)
		}
	}
	for _, statement := range []string{`CREATE TABLE tree_node(id INTEGER PRIMARY KEY,parent_id INTEGER REFERENCES tree_node(id),name TEXT NOT NULL)`, `INSERT INTO tree_node VALUES(1,1,'root')`} {
		if _, err := backend.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := backend.Query(t.Context(), accepted)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("63-target graph lost row")
	}
	destinations := make([]any, 64*3)
	for i := range destinations {
		destinations[i] = new(any)
	}
	if err := rows.Scan(destinations...); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(destinations); i += 3 {
		if *destinations[i].(*any) != int64(1) || *destinations[i+1].(*any) != int64(1) || *destinations[i+2].(*any) != "root" {
			t.Fatalf("route occurrence %d changed", i/3)
		}
	}
	if rows.Next() {
		t.Fatal("graph duplicated source")
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
}
