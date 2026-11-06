package projectwire

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestSlugIndexUnicodeWireRetainsBooleansAndExactSize(t *testing.T) {
	for _, unique := range []bool{false, true} {
		field := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldSlug, MaxLength: 50, DBIndex: unique, AllowUnicode: unique}
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
		if err := json.Unmarshal(wire, &decoded); err != nil || decoded.Apps[0].Schema.Models[0].Fields[0].DBIndex != unique || decoded.Apps[0].Schema.Models[0].Fields[0].AllowUnicode != unique {
			t.Fatal("unique changed across wire", err)
		}
		if !unique {
			continue
		}
		for _, flag := range []string{"db_index", "allow_unicode"} {
			for _, value := range []string{`null`, `0`, `"true"`, `[]`, `{}`, `true,"` + flag + `":false`} {
				bad := bytes.Replace(wire, []byte(`"`+flag+`":true`), []byte(`"`+flag+`":`+value), 1)
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
}
