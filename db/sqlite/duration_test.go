package sqlite

import (
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"math"
	"testing"
)

func TestSQLiteDurationRangeAndSQLArguments(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	field := query.NewFieldRef("elapsed", "elapsed", query.FieldDuration, true)
	for _, micros := range []int64{math.MinInt64, math.MaxInt64, -1, 0, 1} {
		value := query.Duration(duration.FromMicroseconds(micros))
		plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, value))
		if err != nil {
			t.Fatal(err)
		}
		_, args, err := Compile(plan)
		if err != nil || len(args) != 1 || args[0] != micros {
			t.Fatalf("duration predicate args %v %v", args, err)
		}
		_, args, err = CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, value)}, id))
		if err != nil || len(args) != 1 || args[0] != micros {
			t.Fatalf("duration write args %v %v", args, err)
		}
	}
	for _, raw := range []string{"106751991 04:00:54.775808", "-106751992 19:59:05.224191", "999999999 23:59:59.999999"} {
		elapsed, err := duration.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, query.Duration(elapsed)))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := Compile(plan); err == nil {
			t.Fatal("SQLite predicate narrowed model value")
		}
		if _, _, err := CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, query.Duration(elapsed))}, id)); err == nil {
			t.Fatal("SQLite write narrowed model value")
		}
	}
	ddl, err := compileMigrationColumn(ir.Field{Name: "elapsed", GoName: "Elapsed", Column: "elapsed", Kind: ir.FieldDuration, Nullable: true})
	if err != nil || ddl != `"elapsed" BIGINT NULL` {
		t.Fatalf("duration DDL %q %v", ddl, err)
	}
}
