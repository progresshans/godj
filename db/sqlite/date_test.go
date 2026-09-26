package sqlite

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDateSQLUsesCanonicalTextForReadsAndWrites(t *testing.T) {
	field := query.NewFieldRef("at", "at", query.FieldDate, true)
	value := query.Date(calendar.Date{Year: 2026, Month: 9, Day: 19})
	want := "2026-09-19"
	plan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupGreaterThanOrEqual, value))
	if err != nil {
		t.Fatal(err)
	}
	_, readArgs, err := Compile(plan)
	if err != nil || len(readArgs) != 1 || readArgs[0] != want {
		t.Fatalf("date predicate argument: %v %v", readArgs, err)
	}
	_, writeArgs, err := CompileInsert(query.NewInsertPlanReturningKey("events", []query.Assignment{query.NewAssignment(field, value)}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
	if err != nil || len(writeArgs) != 1 || writeArgs[0] != want {
		t.Fatalf("date write argument: %v %v", writeArgs, err)
	}
	in, err := query.NewInCondition(field, []query.Value{value, query.Date(calendar.Date{Year: 1, Month: 1, Day: 1})})
	if err != nil {
		t.Fatal(err)
	}
	inPlan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(in)
	if err != nil {
		t.Fatal(err)
	}
	_, arguments, err := Compile(inPlan)
	if err != nil || len(arguments) != 2 || arguments[0] != want || arguments[1] != "0001-01-01" {
		t.Fatalf("date IN encoding: %v %v", arguments, err)
	}
	for _, nullable := range []bool{false, true} {
		field := ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldDate, Nullable: nullable, Default: &ir.Scalar{Kind: ir.ScalarDate, Date: "0001-01-01"}}
		ddl, err := compileMigrationColumn(field)
		want := `"at" DATE NOT NULL`
		if nullable {
			want = `"at" DATE NULL`
		}
		if err != nil || ddl != want {
			t.Fatalf("date DDL added a persistent default or lost null: %s %v", ddl, err)
		}
	}
}
