package schema_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestCascadeDeclarationRetainsPolicyNullabilityCardinalityAndHash(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		for _, one := range []bool{false, true} {
			field := schema.ForeignKey("owner", "OwnerID", schema.Target("accounts", "owner"), schema.NoReverse(), schema.Cascade)
			if one {
				field = schema.OneToOne("owner", "OwnerID", schema.Target("accounts", "owner"), schema.NoReverse(), schema.Cascade)
			}
			field.Nullable = nullable
			input := schema.Definition{AppLabel: "links", Models: []schema.Model{{Name: "link", GoName: "Link", Fields: []schema.Field{field}}}}
			built, err := schema.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			got := built.Models[0].Fields[1]
			if got.Relation.OnDelete != ir.DeleteCascade || got.Nullable != nullable || got.Unique != one || !got.Relation.Reverse.Disabled {
				t.Fatal("declaration lost cascade field meaning")
			}
			cascadeHash, err := ir.Hash(built)
			if err != nil {
				t.Fatal(err)
			}
			changed := built.Clone()
			changed.Models[0].Fields[1].Relation.OnDelete = ir.DeleteProtect
			otherHash, err := ir.Hash(changed)
			if err != nil || otherHash == cascadeHash || input.Models[0].Fields[0].Relation.OnDelete != ir.DeleteCascade {
				t.Fatal("schema hash erased the policy or borrowed declaration storage", err)
			}
		}
	}
}
