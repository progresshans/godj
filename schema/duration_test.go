package schema_test

import (
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDurationFullModelRangeDefaultsAndMixedScalarRejection(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "elapsed", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{field}}}})
	}
	for _, elapsed := range []duration.Duration{{}, {Days: duration.MinDays}, {Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}} {
		model, err := build(schema.DurationField("elapsed", "Elapsed", schema.Default(elapsed)))
		if err != nil {
			t.Fatal(err)
		}
		field := model.Models[0].Fields[1]
		if field.Kind != ir.FieldDuration || field.Default.Kind != ir.ScalarDuration || field.Default.Duration != elapsed.String() {
			t.Fatal("model duration range/default lost")
		}
		for _, other := range []string{"String", "Time", "Date", "DateTime"} {
			bad := *field.Default
			switch other {
			case "String":
				bad.String = "x"
			case "Time":
				bad.Time = "00:00:00"
			case "Date":
				bad.Date = "2000-01-01"
			case "DateTime":
				bad.DateTime = "2000-01-01T00:00:00.000000Z"
			}
			model.Models[0].Fields[1].Default = &bad
			if _, err := ir.Normalize(model); err == nil {
				t.Fatal("inactive scalar payload ignored")
			}
		}
	}
	for _, field := range []schema.Field{schema.DurationField("elapsed", "Elapsed", schema.Default(duration.Duration{Microseconds: -1})), schema.DurationField("elapsed", "Elapsed", schema.Default("00:00:00")), schema.DurationField("elapsed", "Elapsed", schema.Default(clock.Time{}))} {
		if _, err := build(field); err == nil {
			t.Fatal("invalid/coerced duration default")
		}
	}
	model, err := build(schema.TimeField("at", "At", schema.Default(clock.Time{})))
	if err != nil {
		t.Fatal(err)
	}
	model.Models[0].Fields[1].Default.Duration = "00:00:00"
	if _, err := ir.Normalize(model); err == nil {
		t.Fatal("duration hidden in clock scalar")
	}
}
