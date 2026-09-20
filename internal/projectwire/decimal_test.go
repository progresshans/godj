package projectwire

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestDecimalWirePrecisionBudgetClosedShapeAndMeasurement(t *testing.T) {
	field := ir.Field{Name: "cost", GoName: "Cost", Column: "cost", Kind: ir.FieldDecimal, Decimal: &ir.DecimalSpec{MaxDigits: 30, DecimalPlaces: 12}, Default: &ir.Scalar{Kind: ir.ScalarDecimal, Decimal: "123456789012345678.123456789012"}}
	spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "costs", Models: []ir.Model{{Name: "record", GoName: "Record", DBTable: "record", Fields: []ir.Field{field}}}}}}}
	wire, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
		sizer := wirejson.NewSizer(limit)
		ok := Measure(sizer, spec)
		if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
			t.Fatal("decimal wire size budget differs from encoding")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	if err := Scan(decoder); err != nil {
		t.Fatal(err)
	}
	fieldWire, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{`"max_digits":30.0`, `"max_digits":"30"`, `"max_digits":null`, `"max_digits":30,"unknown":1`, `"max_digits":30,"max_digits":30`} {
		bad := bytes.Replace(fieldWire, []byte(`"max_digits":30`), []byte(replacement), 1)
		decoder := json.NewDecoder(bytes.NewReader(bad))
		decoder.UseNumber()
		if err := parseField(decoder, &specBudget{}); err == nil {
			t.Fatalf("invalid decimal wire accepted: %s", replacement)
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(fieldWire))
	decoder.UseNumber()
	if err := parseField(decoder, &specBudget{nodes: projectspec.MaxAggregateNodes - 1}); err == nil {
		t.Fatal("precision bypassed wire node budget")
	}
}
