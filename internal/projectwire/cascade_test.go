package projectwire

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestCascadeProjectWireRetainsCanonicalSchemaAndExactSize(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "links", Models: []schema.Model{{Name: "link", GoName: "Link", Fields: []schema.Field{
		schema.ForeignKey("owner", "OwnerID", schema.Target("accounts", "owner"), schema.NoReverse(), schema.Cascade),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Apps: []App{{Schema: built}}}
	wire, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
		sizer := wirejson.NewSizer(limit)
		ok := Measure(sizer, spec)
		if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
			t.Fatal("CASCADE project wire size is not exact")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	if err := Scan(decoder); err != nil {
		t.Fatal(err)
	}
	var decoded Spec
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	normalized, err := ir.Normalize(decoded.Apps[0].Schema)
	wantHash, hashErr := ir.Hash(built)
	gotHash, gotErr := ir.Hash(normalized)
	if err != nil || hashErr != nil || gotErr != nil || wantHash != gotHash || normalized.Models[0].Fields[1].Relation.OnDelete != ir.DeleteCascade {
		t.Fatal("project wire changed the normalized CASCADE declaration", err, hashErr, gotErr)
	}
}
