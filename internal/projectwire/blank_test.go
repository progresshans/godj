package projectwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestBlankWireOwnsScalarAndCollectionPolicyAndExactLimit(t *testing.T) {
	for _, scalar := range []bool{false, true} {
		for _, collection := range []bool{false, true} {
			t.Run(fmt.Sprintf("scalar_%t/collection_%t", scalar, collection), func(t *testing.T) {
				model := ir.Model{Name: "entry", GoName: "Entry", DBTable: "entry", Fields: []ir.Field{{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldChar, MaxLength: 24, Blank: scalar}}, ManyToMany: []ir.ManyToManyField{{Name: "related", GoName: "Related", Target: ir.ModelIdentity{AppLabel: "policy", ModelName: "entry"}, Symmetry: ir.ManyToManyDirected, Blank: collection}}}
				spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "policy", Models: []ir.Model{model}}}}}
				wire, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
					sizer := wirejson.NewSizer(limit)
					ok := Measure(sizer, spec)
					if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
						t.Fatal("blank policy escaped exact wire size accounting")
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
				got := decoded.Apps[0].Schema.Models[0]
				if got.Fields[0].Blank != scalar || got.ManyToMany[0].Blank != collection {
					t.Fatal("model policy was lost in project runner transport")
				}
				if scalar == collection {
					return
				}
				for _, invalid := range []string{`null`, `0`, `"true"`, `[]`, `{}`, `true,"blank":false`} {
					bad := bytes.Replace(wire, []byte(`"blank":true`), []byte(`"blank":`+invalid), 1)
					if bytes.Equal(bad, wire) {
						t.Fatal("wire control did not change input")
					}
					decoder := json.NewDecoder(bytes.NewReader(bad))
					decoder.UseNumber()
					if err := Scan(decoder); err == nil {
						t.Fatal("malformed or repeated blank policy was accepted")
					}
				}
			})
		}
	}
}
