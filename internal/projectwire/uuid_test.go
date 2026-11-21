package projectwire

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestUUIDWireSizeAndClosedPayload(t *testing.T) {
	field := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldUUID, Nullable: true, Default: &ir.Scalar{Kind: ir.ScalarUUID, UUID: "00000000-0000-0000-0000-000000000000"}}
	spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "uuidref", Models: []ir.Model{{Name: "record", GoName: "Record", DBTable: "record", Fields: []ir.Field{field}}}}}}}
	wire, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
		sizer := wirejson.NewSizer(limit)
		ok := Measure(sizer, spec)
		if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
			t.Fatal("UUID wire size omitted or mismeasured payload")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	if err := Scan(decoder); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`null`, `0`, `true`, `[]`, `{}`, `"zero","uuid":"duplicate"`} {
		bad := bytes.Replace(wire, []byte(`"uuid":"00000000-0000-0000-0000-000000000000"`), []byte(`"uuid":`+value), 1)
		if bytes.Equal(bad, wire) {
			t.Fatal("UUID negative control did not change the wire")
		}
		decoder := json.NewDecoder(bytes.NewReader(bad))
		decoder.UseNumber()
		if err := Scan(decoder); err == nil {
			t.Fatal("UUID wire accepted an invalid payload shape")
		}
	}
}
