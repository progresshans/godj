package projectwire

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestNamedUniqueConstraintWireOwnsMetadataAndExactEscapedBudget(t *testing.T) {
	for _, name := range []string{"scope_name", "<unique>\u2028한글"} {
		constraint := ir.UniqueConstraint{Name: name, Fields: []string{"category", "label"}}
		model := ir.Model{Name: "label", GoName: "Label", DBTable: "labels", Fields: []ir.Field{}, UniqueConstraints: []ir.UniqueConstraint{constraint}}
		spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "labels", Models: []ir.Model{model}}}}}
		wire, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, maximum := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
			sizer := wirejson.NewSizer(maximum)
			ok := Measure(sizer, spec)
			if ok != (maximum >= len(wire)) || ok && sizer.Size() != len(wire) {
				t.Fatal("constraint bypassed exact wire budget")
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(wire))
		decoder.UseNumber()
		if err := Scan(decoder); err != nil {
			t.Fatal(err)
		}
		var decoded Spec
		if err := json.Unmarshal(wire, &decoded); err != nil || !reflect.DeepEqual(decoded.Apps[0].Schema.Models[0].UniqueConstraints, []ir.UniqueConstraint{constraint}) {
			t.Fatal("wire lost a constraint name, field or order", err)
		}
		copy := Snapshot(Declaration(decoded))
		copy.Apps[0].Schema.Models[0].UniqueConstraints[0].Fields[0] = "mutated"
		if decoded.Apps[0].Schema.Models[0].UniqueConstraints[0].Fields[0] != "category" {
			t.Fatal("wire snapshot aliases constraint members")
		}
	}
}

func TestNamedUniqueConstraintWireRejectsOpenShapesAndBudgetOverflow(t *testing.T) {
	scan := func(raw string, budget *specBudget) error {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		return parseUniqueConstraint(decoder, budget)
	}
	valid := `{"name":"scope_name","fields":["category","label"]}`
	if err := scan(valid, &specBudget{nodes: projectspec.MaxAggregateNodes - 2}); err != nil {
		t.Fatal(err)
	}
	if err := scan(valid, &specBudget{nodes: projectspec.MaxAggregateNodes - 1}); err == nil {
		t.Fatal("member references bypassed aggregate node budget")
	}
	for _, invalid := range []string{
		`null`, `[]`, `{}`, `{"name":"scope_name"}`, `{"fields":["category"]}`,
		`{"name":"scope_name","fields":null}`, `{"name":"scope_name","fields":"label"}`,
		`{"name":"scope_name","fields":[null]}`, `{"name":"scope_name","fields":[{}]}`,
		`{"name":"scope_name","fields":[],"condition":true}`,
		`{"name":"scope_name","fields":[],"name":"other"}`,
		`{"name":"scope_name","fields":[],"fields":["category"]}`,
		`{"name":"` + strings.Repeat("x", projectspec.MaxSchemaStringBytes+1) + `","fields":[]}`,
		`{"name":"scope_name","fields":["` + strings.Repeat("x", projectspec.MaxSchemaStringBytes+1) + `"]}`,
		`{"name":"scope_name","fields":[` + strings.Repeat(`"category",`, projectspec.MaxFieldsPerModel) + `"label"]}`,
	} {
		if err := scan(invalid, &specBudget{}); err == nil {
			t.Fatal("invalid or unbounded constraint wire accepted")
		}
	}
	for _, invalid := range []string{`null`, `{}`, `true`, `"name"`} {
		model := `{"name":"label","go_name":"Label","db_table":"labels","fields":[],"unique_constraints":` + invalid + `}`
		decoder := json.NewDecoder(strings.NewReader(model))
		decoder.UseNumber()
		if err := parseModel(decoder, &specBudget{}); err == nil {
			t.Fatal("non-array model constraints accepted")
		}
	}
}
