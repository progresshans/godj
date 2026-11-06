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

func TestManyToManyWirePreservesSnapshotAndExactEscapedSize(t *testing.T) {
	for _, through := range []*ir.ThroughModel{nil, {Model: ir.ModelIdentity{AppLabel: "links", ModelName: "pair"}, SourceField: "owner", TargetField: "label"}} {
		for _, reverse := range []ir.ReverseRelation{{}, {Name: "owners"}, {Disabled: true}, {Name: "<逆>\u2028", Disabled: true}} {
			field := ir.ManyToManyField{Name: "<labels>\u2028한글", GoName: "Labels", Target: ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}, Reverse: reverse, Symmetry: ir.ManyToManyDirected, Through: through}
			spec := Spec{Apps: []App{{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "owners", Models: []ir.Model{{Name: "owner", GoName: "Owner", DBTable: "owners", Fields: []ir.Field{}, ManyToMany: []ir.ManyToManyField{field}}}}}}}
			wire, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, max := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
				sizer := wirejson.NewSizer(max)
				ok := Measure(sizer, spec)
				if ok != (max >= len(wire)) || ok && sizer.Size() != len(wire) {
					t.Fatal("many-to-many escaped wire budget drift", max, sizer.Size(), len(wire))
				}
			}
			decoder := json.NewDecoder(bytes.NewReader(wire))
			decoder.UseNumber()
			if err := Scan(decoder); err != nil {
				t.Fatal(err)
			}
			var decoded Spec
			if err := json.Unmarshal(wire, &decoded); err != nil || !reflect.DeepEqual(decoded.Apps[0].Schema.Models[0].ManyToMany, []ir.ManyToManyField{field}) {
				t.Fatal("wire lost relation", err)
			}
			copy := Snapshot(Declaration(decoded))
			copy.Apps[0].Schema.Models[0].ManyToMany[0].Target.ModelName = "changed"
			if through != nil {
				copy.Apps[0].Schema.Models[0].ManyToMany[0].Through.SourceField = "changed"
				if decoded.Apps[0].Schema.Models[0].ManyToMany[0].Through.SourceField != "owner" {
					t.Fatal("through alias")
				}
			}
			if decoded.Apps[0].Schema.Models[0].ManyToMany[0].Target.ModelName != "label" {
				t.Fatal("relation alias")
			}
		}
	}
}

func TestManyToManyWireRejectsOpenShapesAndBoundsBeforeDecode(t *testing.T) {
	valid := `{"name":"labels","go_name":"Labels","target":{"app_label":"labels","model_name":"label"},"reverse":{},"symmetry":"directed","through":{"model":{"app_label":"links","model_name":"pair"},"source_field":"owner","target_field":"label"}}`
	scan := func(value string, budget *specBudget) error {
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.UseNumber()
		return parseManyToMany(decoder, budget)
	}
	if err := scan(valid, &specBudget{nodes: projectspec.MaxAggregateNodes - 2}); err != nil {
		t.Fatal(err)
	}
	if err := scan(valid, &specBudget{nodes: projectspec.MaxAggregateNodes - 1}); err == nil {
		t.Fatal("through nodes not charged")
	}
	for _, invalid := range []string{`null`, `{}`, strings.Replace(valid, `"symmetry":"directed",`, "", 1), strings.Replace(valid, `"source_field":"owner"`, `"source_field":null`, 1), strings.Replace(valid, `"reverse":{}`, `"reverse":{"column":"fake"}`, 1), strings.Replace(valid, `"symmetry":"directed"`, `"symmetry":"directed","column":"fake"`, 1), strings.Replace(valid, `"source_field":"owner"`, `"source_field":"owner","source_field":"other"`, 1), strings.Replace(valid, `"label"`, `"`+strings.Repeat("x", projectspec.MaxSchemaStringBytes+1)+`"`, 1)} {
		if err := scan(invalid, &specBudget{}); err == nil {
			t.Fatal("malformed many-to-many accepted", invalid[:min(len(invalid), 80)])
		}
	}
	model := `{"name":"owner","go_name":"Owner","db_table":"owners","fields":[],"many_to_many":[` + strings.Repeat(valid+",", projectspec.MaxFieldsPerModel) + valid + `]}`
	decoder := json.NewDecoder(strings.NewReader(model))
	decoder.UseNumber()
	if err := parseModel(decoder, &specBudget{}); err == nil {
		t.Fatal("per-model field limit bypass")
	}
}
