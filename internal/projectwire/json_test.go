package projectwire

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestJSONDefaultWireSizeAndClosedPayload(t *testing.T) {
	field := ir.Field{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldJSON, Default: &ir.Scalar{Kind: ir.ScalarJSON, JSON: `{"nested":[true,340282366920938463463374607431768211455]}`}}
	spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "jsonref", Models: []ir.Model{{Name: "record", GoName: "Record", DBTable: "record", Fields: []ir.Field{field}}}}}}}
	wire, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
		sizer := wirejson.NewSizer(limit)
		ok := Measure(sizer, spec)
		if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
			t.Fatal("JSON document escaping escaped the wire size budget")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	if err := Scan(decoder); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(field.Default.JSON)
	payload := append([]byte(`"json":`), encoded...)
	for _, value := range []string{`null`, `0`, `true`, `[]`, `{}`, `"null","json":"duplicate"`} {
		bad := bytes.Replace(wire, payload, []byte(`"json":`+value), 1)
		if bytes.Equal(bad, wire) {
			t.Fatal("JSON negative control did not change wire")
		}
		decoder := json.NewDecoder(bytes.NewReader(bad))
		decoder.UseNumber()
		if err := Scan(decoder); err == nil {
			t.Fatal("JSON default accepted an invalid wire shape")
		}
	}
}
