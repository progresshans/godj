package schema_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestUniqueDeclarationPreservesScalarAndRelationMeaning(t *testing.T) {
	fields := []schema.Field{
		schema.CharField("value", "Value", 24), schema.TextField("value", "Value"),
		schema.IntegerField("value", "Value"), schema.BooleanField("value", "Value"),
		schema.FloatField("value", "Value"), schema.DecimalField("value", "Value", 8, 2),
		schema.UUIDField("value", "Value"), schema.JSONField("value", "Value"),
		schema.DateField("value", "Value"), schema.DateTimeField("value", "Value"),
		schema.TimeField("value", "Value"), schema.DurationField("value", "Value"),
		schema.ForeignKey("value", "Value", schema.Target("unique", "entry"), schema.RelatedName("children"), schema.Protect),
	}
	for _, field := range fields {
		t.Run(string(field.Kind), func(t *testing.T) {
			for _, nullable := range []bool{false, true} {
				field.Nullable = nullable
				declaration := schema.Definition{AppLabel: "unique", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{field}}}}
				plain, err := schema.Build(declaration)
				if err != nil {
					t.Fatal(err)
				}
				schema.Unique()(&declaration.Models[0].Fields[0])
				unique, err := schema.Build(declaration)
				if err != nil {
					t.Fatal(err)
				}
				got := unique.Models[0].Fields[1]
				want := plain.Models[0].Fields[1].Clone()
				want.Unique = true
				if !got.Equal(want) || !reflect.DeepEqual(got, want) {
					t.Fatal("unique changed another field facet")
				}
				if got.Equal(plain.Models[0].Fields[1]) {
					t.Fatal("field equality omitted uniqueness")
				}
				beforeHash, err := ir.Hash(plain)
				if err != nil {
					t.Fatal(err)
				}
				afterHash, err := ir.Hash(unique)
				if err != nil || beforeHash == afterHash {
					t.Fatal("schema identity omitted uniqueness", err)
				}
				clone := unique.Clone()
				clone.Models[0].Fields[1].Unique = false
				if !unique.Models[0].Fields[1].Unique {
					t.Fatal("cloned field aliases its source")
				}
				if got.Relation != nil && (got.Relation.Cardinality != ir.RelationManyToOne || got.Relation.Reverse.Name != "children") {
					t.Fatal("unique ForeignKey changed its relation API")
				}
			}
		})
	}
}

func TestPrimaryKeyOwnsUniquenessWithoutDuplicateConstraint(t *testing.T) {
	declaration := schema.Definition{AppLabel: "unique", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.AutoField("id", "ID", schema.Unique())}}}}
	built, err := schema.Build(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if !built.Models[0].Fields[0].PrimaryKey || built.Models[0].Fields[0].Unique {
		t.Fatal("primary key requested a second constraint")
	}
	if !declaration.Models[0].Fields[0].Unique {
		t.Fatal("normalization mutated the caller")
	}
	redundant := built.Clone()
	redundant.Models[0].Fields[0].Unique = true
	normalized, err := ir.Normalize(redundant)
	if err != nil || !reflect.DeepEqual(normalized, built) || !redundant.Models[0].Fields[0].Unique {
		t.Fatal("IR normalization lost PK ownership", err)
	}
}
