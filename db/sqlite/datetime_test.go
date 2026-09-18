package sqlite

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
	"time"
)

func TestDateTimeSQLUsesCanonicalTextForReadsAndWrites(t *testing.T) {
	field := query.NewFieldRef("at", "at", query.FieldDateTime, true)
	value := query.DateTime(time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("input", 9*3600)))
	want := "2026-09-19 03:34:56.123456"
	plan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupGreaterThanOrEqual, value))
	if err != nil {
		t.Fatal(err)
	}
	_, readArgs, err := Compile(plan)
	if err != nil || len(readArgs) != 1 || readArgs[0] != want {
		t.Fatalf("datetime predicate argument: %v %v", readArgs, err)
	}
	_, writeArgs, err := CompileInsert(query.NewInsertPlanReturningKey("events", []query.Assignment{query.NewAssignment(field, value)}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
	if err != nil || len(writeArgs) != 1 || writeArgs[0] != want {
		t.Fatalf("datetime write argument: %v %v", writeArgs, err)
	}
	in, err := query.NewInCondition(field, []query.Value{value, query.DateTime(time.Time{})})
	if err != nil {
		t.Fatal(err)
	}
	inPlan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(in)
	if err != nil {
		t.Fatal(err)
	}
	_, arguments, err := Compile(inPlan)
	if err != nil || len(arguments) != 2 || arguments[0] != want || arguments[1] != "0001-01-01 00:00:00.000000" {
		t.Fatalf("datetime IN encoding: %v %v", arguments, err)
	}
	for _, nullable := range []bool{false, true} {
		field := ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldDateTime, Nullable: nullable, Default: &ir.ScalarDefault{Kind: ir.ScalarDateTime, DateTime: "0001-01-01T00:00:00.000000Z"}}
		ddl, err := compileMigrationColumn(field)
		want := `"at" DATETIME NOT NULL`
		if nullable {
			want = `"at" DATETIME NULL`
		}
		if err != nil || ddl != want {
			t.Fatalf("datetime DDL added a persistent default or lost null: %s %v", ddl, err)
		}
	}
}
