package schema_test

import (
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"testing"
	"time"
)

func TestDateTimeDefaultIdentityUsesUTCPrecisionAndKeepsZeroPresent(t *testing.T) {
	first := time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("application zone", 9*60*60))
	build := func(value time.Time) ir.Schema {
		result, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
			schema.DateTimeField("occurred", "Occurred", schema.Default(value)), schema.DateTimeField("optional", "Optional", schema.Nullable(), schema.Default(time.Time{})),
		}}}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	one, two := build(first), build(first.UTC().Truncate(time.Microsecond))
	hash1, err := ir.Hash(one)
	if err != nil {
		t.Fatal(err)
	}
	hash2, err := ir.Hash(two)
	if err != nil || hash1 != hash2 {
		t.Fatal("equivalent datetime defaults changed schema identity")
	}
	if one.Models[0].Fields[2].Default.DateTime != "0001-01-01T00:00:00.000000Z" {
		t.Fatal("zero time became an absent default")
	}
	for _, field := range []schema.Field{schema.DateTimeField("invalid", "Invalid", schema.Default("now")), schema.DateTimeField("invalid", "Invalid", schema.Default(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)))} {
		if _, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{field}}}}); err == nil {
			t.Fatal("invalid datetime default accepted")
		}
	}
}
