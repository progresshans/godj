package uniquetest

import (
	"math"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

// CompositeModel keeps one independent named key containing the primary key
// while tests add/remove the scoped key. Logical bucket differs from storage.
func CompositeModel(t testing.TB, profile string, scoped bool) (ir.Model, query.FieldRef, query.FieldRef) {
	t.Helper()
	field, value := Field(t, profile)
	field.Unique = false
	constraints := []schema.UniqueConstraint{{Name: "identity_value", Fields: []string{"id", "value"}}}
	if scoped {
		constraints = append(constraints, schema.UniqueConstraint{Name: "within_bucket", Fields: []string{"bucket", "value"}})
	}
	built, err := schema.Build(schema.Definition{AppLabel: "composite", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.IntegerField("bucket", "Bucket", schema.Nullable(), schema.Column("scope_key")), field,
	}, UniqueConstraints: constraints}}})
	if err != nil {
		t.Fatal(err)
	}
	return built.Models[0], query.NewFieldRef("bucket", "scope_key", query.FieldInteger, true), value
}

func CompositeSample(t testing.TB, profile Profile) query.Value {
	t.Helper()
	for _, attempt := range profile.Attempts {
		if !attempt.Saved {
			continue
		}
		value := Value(t, profile.Name, attempt.Input)
		if value.Kind() == query.ValueNull {
			continue
		}
		if number, ok := value.Float(); ok && math.IsNaN(number) {
			continue
		}
		return value
	}
	t.Fatal("scalar profile has no native composite sample")
	return query.Value{}
}

// CompositeTargets supplies both FK members and a transitive named owner.
func CompositeTargets(t testing.TB) []ir.Model {
	t.Helper()
	root, _, _ := CompositeModel(t, "char", true)
	built, err := schema.Build(schema.Definition{AppLabel: "composite", Models: []schema.Model{
		{Name: "middle", GoName: "Middle", Fields: []schema.Field{schema.ForeignKey("entry", "Entry", schema.Target("composite", "entry"), schema.NoReverse(), schema.Protect, schema.Nullable())}, UniqueConstraints: []schema.UniqueConstraint{{Name: "middle_entry", Fields: []string{"entry", "id"}}}},
		{Name: "leaf", GoName: "Leaf", Fields: []schema.Field{schema.ForeignKey("middle", "Middle", schema.Target("composite", "middle"), schema.NoReverse(), schema.Protect, schema.Nullable()), schema.TextField("label", "Label", schema.Nullable())}, UniqueConstraints: []schema.UniqueConstraint{{Name: "leaf_label", Fields: []string{"middle", "label"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return append([]ir.Model{root}, built.Models...)
}
