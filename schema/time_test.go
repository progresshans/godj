package schema_test

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"testing"
	"time"
)

func TestClockTimeDefaultsPreserveMidnightAndRejectMixedScalars(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "clocks", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{field}}}})
	}
	value := clock.Time{}
	declaration := schema.TimeField("at", "At", schema.Nullable(), schema.Default(value))
	value.Hour = 23
	definition, err := build(declaration)
	if err != nil {
		t.Fatal(err)
	}
	field := definition.Models[0].Fields[1]
	if field.Kind != ir.FieldTime || field.Default == nil || field.Default.Kind != ir.ScalarTime || field.Default.Time != "00:00:00" {
		t.Fatal("midnight default lost presence or snapshot")
	}
	for _, bad := range []schema.Field{
		schema.TimeField("at", "At", schema.Default(clock.Time{Hour: 24})),
		schema.TimeField("at", "At", schema.Default(clock.Time{Microsecond: 1000000})),
		schema.TimeField("at", "At", schema.Default("00:00:00")),
		schema.TimeField("at", "At", schema.Default(time.Time{})),
		schema.TimeField("at", "At", schema.Default(calendar.Date{Year: 2000, Month: 1, Day: 1})),
	} {
		if _, err := build(bad); err == nil {
			t.Fatal("invalid or mixed clock default accepted")
		}
	}
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.PrimaryKey = true }, func(f *ir.Field) { f.MaxLength = 8 },
		func(f *ir.Field) { f.Default.Time = "24:00:00" }, func(f *ir.Field) { f.Default.Time = "00:00" },
		func(f *ir.Field) { f.Default.Time = "00:00:00.000000" },
		func(f *ir.Field) { f.Default.Date = "2000-01-01" }, func(f *ir.Field) { f.Default.DateTime = "2000-01-01T00:00:00.000000Z" },
		func(f *ir.Field) { f.Default.String = "mixed" }, func(f *ir.Field) { f.Default.Boolean = true }, func(f *ir.Field) { f.Default.Integer = 1 },
	} {
		bad := definition.Clone()
		mutate(&bad.Models[0].Fields[1])
		if _, err := ir.Normalize(bad); err == nil {
			t.Fatal("invalid clock metadata normalized")
		}
	}
	for _, field := range []schema.Field{
		schema.CharField("text", "Text", 20, schema.Default("a")), schema.BooleanField("flag", "Flag", schema.Default(false)),
		schema.IntegerField("number", "Number", schema.Default(int64(0))), schema.DateField("date", "Date", schema.Default(calendar.Date{Year: 2000, Month: 1, Day: 1})), schema.DateTimeField("instant", "Instant", schema.Default(time.Time{})),
	} {
		definition, err := build(field)
		if err != nil {
			t.Fatal(err)
		}
		definition.Models[0].Fields[1].Default.Time = "00:00:00"
		if _, err := ir.Normalize(definition); err == nil {
			t.Fatal("inactive clock payload ignored")
		}
	}
}
