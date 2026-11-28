package sqlite

import (
	"math"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteFloatArgumentsAndNaNRejection(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	field := query.NewFieldRef("amount", "amount", query.FieldFloat, true)
	for _, number := range []float64{0, math.Copysign(0, -1), 0.1, math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64, math.Inf(1), math.Inf(-1)} {
		value := query.Float(number)
		plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, value))
		if err != nil {
			t.Fatal(err)
		}
		_, args, err := Compile(plan)
		if err != nil || len(args) != 1 {
			t.Fatal("float predicate arguments", err)
		}
		if actual, ok := args[0].(float64); !ok || math.Float64bits(actual) != math.Float64bits(number) {
			t.Fatal("float predicate rounded")
		}
		_, args, err = CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, value)}, id))
		if err != nil || len(args) != 1 {
			t.Fatal("float mutation arguments", err)
		}
		if actual, ok := args[0].(float64); !ok || math.Float64bits(actual) != math.Float64bits(number) {
			t.Fatal("float write rounded")
		}
	}
	plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, query.Float(math.NaN())))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Compile(plan); err == nil {
		t.Fatal("NaN predicate can silently become NULL")
	}
	if _, _, err := CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, query.Float(math.NaN()))}, id)); err == nil {
		t.Fatal("NaN mutation can silently become NULL")
	}
	ddl, err := compileMigrationColumn(ir.Field{Name: "amount", GoName: "Amount", Column: "amount", Kind: ir.FieldFloat, Nullable: true})
	if err != nil || ddl != `"amount" REAL NULL` {
		t.Fatalf("Float DDL: %s %v", ddl, err)
	}
}
