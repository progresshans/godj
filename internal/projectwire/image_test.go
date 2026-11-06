package projectwire

import (
	"bytes"
	"encoding/json"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestImageWireIncludesDimensionReferencesAndExactLimit(t *testing.T) {
	for _, pair := range [][2]string{{}, {"width", ""}, {"", "height"}, {"width", "height"}} {
		field := ir.Field{Name: "image", GoName: "Image", Column: "image", Kind: ir.FieldImage, MaxLength: 100, WidthField: pair[0], HeightField: pair[1]}
		spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "images", Models: []ir.Model{{Name: "photo", GoName: "Photo", DBTable: "photo", Fields: []ir.Field{field}}}}}}}
		wire, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
			sizer := wirejson.NewSizer(limit)
			ok := Measure(sizer, spec)
			if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
				t.Fatal("image reference escaped exact wire limit")
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
		if !decoded.Apps[0].Schema.Models[0].Fields[0].Equal(field) {
			t.Fatal("project wire lost image references")
		}
		for _, dimension := range []string{"width_field", "height_field"} {
			if !bytes.Contains(wire, []byte(`"`+dimension+`":`)) {
				continue
			}
			for _, invalid := range []string{`null`, `1`, `true`, `[]`, `{}`, `"width","` + dimension + `":"height"`} {
				var document map[string]any
				if err := json.Unmarshal(wire, &document); err != nil {
					t.Fatal(err)
				}
				needle := `"` + dimension + `":"` + map[string]string{"width_field": pair[0], "height_field": pair[1]}[dimension] + `"`
				bad := bytes.Replace(wire, []byte(needle), []byte(`"`+dimension+`":`+invalid), 1)
				if bytes.Equal(bad, wire) {
					t.Fatal("negative control did not alter wire")
				}
				decoder := json.NewDecoder(bytes.NewReader(bad))
				decoder.UseNumber()
				if err := Scan(decoder); err == nil {
					t.Fatal("malformed dimension wire admitted")
				}
			}
		}
	}
}
