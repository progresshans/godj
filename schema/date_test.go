package schema_test

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"testing"
	"time"
)

func TestCalendarDateDefaultIdentityAndShape(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "dates", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{field}}}})
	}
	date := calendar.Date{Year: 1, Month: 1, Day: 1}
	declaration := schema.DateField("day", "Day", schema.Nullable(), schema.Default(date))
	date.Year = 9999
	definition, err := build(declaration)
	if err != nil {
		t.Fatal(err)
	}
	field := definition.Models[0].Fields[1]
	if field.Kind != ir.FieldDate || field.Default == nil || field.Default.Kind != ir.ScalarDate || field.Default.Date != "0001-01-01" {
		t.Fatal("date declaration default lost identity")
	}
	if _, err := ir.Hash(definition); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []schema.Field{
		schema.DateField("day", "Day", schema.Default(calendar.Date{})), schema.DateField("day", "Day", schema.Default(calendar.Date{Year: 1900, Month: 2, Day: 29})), schema.DateField("day", "Day", schema.Default("2000-02-29")), schema.DateField("day", "Day", schema.Default(time.Time{})),
	} {
		if _, err := build(bad); err == nil {
			t.Fatal("invalid or mixed temporal default accepted")
		}
	}
	for _, change := range []func(*ir.Field){func(f *ir.Field) { f.PrimaryKey = true }, func(f *ir.Field) { f.MaxLength = 10 }, func(f *ir.Field) { f.Default.Date = "2000-2-29" }, func(f *ir.Field) { f.Default.DateTime = "2000-02-29T00:00:00.000000Z" }, func(f *ir.Field) { f.Default.String = "mixed" }, func(f *ir.Field) { f.Default.Boolean = true }, func(f *ir.Field) { f.Default.Integer = 1 }} {
		bad := definition.Clone()
		change(&bad.Models[0].Fields[1])
		if _, err := ir.Normalize(bad); err == nil {
			t.Fatal("invalid date IR normalized")
		}
	}
}
