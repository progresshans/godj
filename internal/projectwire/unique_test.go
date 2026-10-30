package projectwire

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestUniqueWireRetainsBooleanAndExactSize(t *testing.T) {
	for _, unique := range []bool{false, true} {
		field := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldChar, MaxLength: 24, Unique: unique}
		spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "unique", Models: []ir.Model{{Name: "entry", GoName: "Entry", DBTable: "entry", Fields: []ir.Field{field}}}}}}}
		wire, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
			sizer := wirejson.NewSizer(limit)
			ok := Measure(sizer, spec)
			if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
				t.Fatal("unique escaped wire size accounting")
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(wire))
		decoder.UseNumber()
		if err := Scan(decoder); err != nil {
			t.Fatal(err)
		}
		var decoded Spec
		if err := json.Unmarshal(wire, &decoded); err != nil || decoded.Apps[0].Schema.Models[0].Fields[0].Unique != unique {
			t.Fatal("unique changed across wire", err)
		}
		if !unique {
			continue
		}
		for _, value := range []string{`null`, `0`, `"true"`, `[]`, `{}`, `true,"unique":false`} {
			bad := bytes.Replace(wire, []byte(`"unique":true`), []byte(`"unique":`+value), 1)
			if bytes.Equal(bad, wire) {
				t.Fatal("negative control did not change the wire")
			}
			decoder := json.NewDecoder(bytes.NewReader(bad))
			decoder.UseNumber()
			if err := Scan(decoder); err == nil {
				t.Fatalf("invalid unique wire accepted: %s", value)
			}
		}
	}
}
