package projectwire

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestScalarChoicesWireMeasurementAndClosedScan(t *testing.T) {
	for _, scalar := range []ir.Scalar{
		{Kind: ir.ScalarString}, {Kind: ir.ScalarString, String: "<&>\u2028한글"},
		{Kind: ir.ScalarInteger}, {Kind: ir.ScalarInteger, Integer: math.MinInt64},
		{Kind: ir.ScalarInteger, Integer: math.MaxInt64}, {Kind: ir.ScalarBoolean, Boolean: true},
		{Kind: ir.ScalarDate, Date: "0001-01-01"},
		{Kind: ir.ScalarTime, Time: "00:00:00"},
		{Kind: ir.ScalarFloat, FloatBits: "0000000000000000"},
		{Kind: ir.ScalarFloat, FloatBits: "8000000000000000"},
		{Kind: ir.ScalarFloat, FloatBits: "7ff8000000000000"},
		{Kind: ir.ScalarString, FloatBits: "<&>\u2028mixed", String: "payload"},
		{Kind: ir.ScalarDuration, Duration: "00:00:00"},
		{Kind: ir.ScalarDuration, Duration: "-999999999 00:00:00"},
		{Kind: ir.ScalarDuration, Duration: "999999999 23:59:59.999999"},
		{Kind: ir.ScalarString, Duration: "<&>\u2028mixed", String: "payload"},
		{Kind: ir.ScalarTime, Time: "23:59:59.999999"},
		{Kind: ir.ScalarString, Time: "<&>\u2028mixed", String: "payload"},
		{Kind: ir.ScalarString, Date: "<&>\u2028mixed", String: "payload"},
		{Kind: ir.ScalarDateTime, DateTime: "2026-09-19T00:00:00.000000Z"},
	} {
		field := ir.Field{Name: "value", GoName: "Value", Column: "value", Kind: ir.FieldText, Default: &scalar, Choices: []ir.Choice{{Value: scalar, Label: "<Label> 한글"}}}
		spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "app", Models: []ir.Model{{Name: "entry", GoName: "Entry", DBTable: "entry", Fields: []ir.Field{field}}}}}}}
		wire, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, maximum := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
			sizer := wirejson.NewSizer(maximum)
			accepted := Measure(sizer, spec)
			if accepted != (maximum >= len(wire)) || accepted && sizer.Size() != len(wire) {
				t.Fatalf("wire measurement for %+v: accepted=%v bytes=%d actual=%d", scalar, accepted, sizer.Size(), len(wire))
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(wire))
		decoder.UseNumber()
		if err := Scan(decoder); err != nil {
			t.Fatalf("closed scanner rejected supported scalar metadata: %v", err)
		}
	}
}

func TestChoiceWireResourceAndUnknownMemberRejection(t *testing.T) {
	field := ir.Field{Name: "value", GoName: "Value", Column: "value", Kind: ir.FieldText, Choices: []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "open"}, Label: "Open"}}}
	scan := func(wire []byte, budget *specBudget) error {
		decoder := json.NewDecoder(bytes.NewReader(wire))
		decoder.UseNumber()
		return parseField(decoder, budget)
	}
	wire, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	if err := scan(wire, &specBudget{nodes: projectspec.MaxAggregateNodes - 1}); err == nil {
		t.Fatal("choice scalar nodes bypassed the aggregate budget")
	}
	if err := scan(bytes.Replace(wire, []byte(`"label":"Open"`), []byte(`"label":"Open","extra":true`), 1), &specBudget{}); err == nil {
		t.Fatal("choice unknown member accepted")
	}
	field.Choices[0].Label = strings.Repeat("x", projectspec.MaxSchemaStringBytes+1)
	wire, err = json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	if err := scan(wire, &specBudget{}); err == nil {
		t.Fatal("choice label bypassed string budget")
	}
}
