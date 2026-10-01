package sqlite

import (
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestTimeSQLUsesCanonicalTextForReadsAndWrites(t *testing.T) {
	field := query.NewFieldRef("at", "at", query.FieldTime, true)
	value := query.Time(clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 123456})
	want := "12:34:56.123456"
	plan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupGreaterThanOrEqual, value))
	if err != nil {
		t.Fatal(err)
	}
	_, readArgs, err := Compile(plan)
	if err != nil || len(readArgs) != 1 || readArgs[0] != want {
		t.Fatalf("time predicate argument: %v %v", readArgs, err)
	}
	_, writeArgs, err := CompileInsert(query.NewInsertPlanReturningKey("events", []query.Assignment{query.NewAssignment(field, value)}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
	if err != nil || len(writeArgs) != 1 || writeArgs[0] != want {
		t.Fatalf("time write argument: %v %v", writeArgs, err)
	}
	in, err := query.NewInCondition(field, []query.Value{value, query.Time(clock.Time{})})
	if err != nil {
		t.Fatal(err)
	}
	inPlan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(in)
	if err != nil {
		t.Fatal(err)
	}
	_, arguments, err := Compile(inPlan)
	if err != nil || len(arguments) != 2 || arguments[0] != want || arguments[1] != "00:00:00" {
		t.Fatalf("time IN encoding: %v %v", arguments, err)
	}
	for _, nullable := range []bool{false, true} {
		field := ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldTime, Nullable: nullable, Default: &ir.Scalar{Kind: ir.ScalarTime, Time: "00:00:00"}}
		ddl, err := compileMigrationColumn(field)
		want := `"at" TIME NOT NULL`
		if nullable {
			want = `"at" TIME NULL`
		}
		if err != nil || ddl != want {
			t.Fatalf("time DDL added a persistent default or lost null: %s %v", ddl, err)
		}
	}
}
