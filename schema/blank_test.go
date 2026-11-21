package schema_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestBlankInputPolicyIsIndependentOfStorageAndRetainedInMetadata(t *testing.T) {
	fields := []schema.Field{
		schema.CharField("name", "Name", 40, schema.Blank()),
		schema.EmailField("email", "Email", schema.Nullable()),
		schema.TextField("note", "Note", schema.Nullable(), schema.Blank()),
		schema.IntegerField("number", "Number", schema.Blank()),
		schema.BooleanField("enabled", "Enabled", schema.Blank()),
		schema.DateField("day", "Day", schema.Blank()),
		schema.DateTimeField("instant", "Instant", schema.Blank()),
		schema.TimeField("time", "Time", schema.Blank()),
		schema.DurationField("duration", "Duration", schema.Blank()),
		schema.FloatField("float", "Float", schema.Blank()),
		schema.DecimalField("decimal", "Decimal", 8, 2, schema.Blank()),
		schema.UUIDField("uuid", "UUID", schema.Blank()),
		schema.JSONField("json", "JSON", schema.Blank()),
		schema.ForeignKey("parent", "ParentID", schema.Target("policy", "entry"), schema.RelatedName("children"), schema.Protect, schema.Nullable(), schema.Blank()),
	}
	value, err := schema.Build(schema.Definition{AppLabel: "policy", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: fields,
		ManyToMany: []schema.ManyToManyField{schema.ManyToMany("peers", "Peers", schema.Target("policy", "entry"), schema.ReverseRelation{Disabled: true}, schema.ManyToManyBlank())},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	model := value.Models[0]
	for _, field := range model.Fields {
		if field.PrimaryKey {
			continue
		}
		if field.Blank != (field.Name != "email") {
			t.Fatalf("lost blank policy: %+v", field)
		}
		if field.Nullable != (field.Name == "email" || field.Name == "note" || field.Name == "parent") {
			t.Fatalf("blank changed storage nullability: %+v", field)
		}
		before := field.Clone()
		before.Blank = !field.Blank
		kind, err := ir.ClassifyFieldChange(before, field)
		if err != nil || kind != ir.ChangeBlank {
			t.Fatalf("blank-only delta: %v, %v", kind, err)
		}
		mixed := field.Clone()
		mixed.Nullable = !mixed.Nullable
		if _, err := ir.ClassifyFieldChange(before, mixed); err == nil {
			t.Fatal("blank change hid a storage change")
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ir.Schema
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, decoded) {
		t.Fatal("IR wire lost model input policy")
	}
	before := model.ManyToMany[0]
	after := before.Clone()
	after.Blank = false
	if !ir.ManyToManyBlankChange(before, after) || ir.ManyToManyRename(before, after) {
		t.Fatal("blank was confused with rename")
	}
	left, err := ir.AutomaticThroughModel(value.AppLabel, model, before)
	if err != nil {
		t.Fatal(err)
	}
	changedModel := model.Clone()
	changedModel.ManyToMany[0] = after
	right, err := ir.AutomaticThroughModel(value.AppLabel, changedModel, after)
	if err != nil || !reflect.DeepEqual(left, right) {
		t.Fatal("blank policy changed owned storage", err)
	}
	for _, mutate := range []func(*ir.ManyToManyField){
		func(f *ir.ManyToManyField) { f.Name = "other" },
		func(f *ir.ManyToManyField) { f.Target.ModelName = "other" },
		func(f *ir.ManyToManyField) { f.Symmetry = ir.ManyToManyDirected },
	} {
		mixed := after.Clone()
		mutate(&mixed)
		if ir.ManyToManyBlankChange(before, mixed) {
			t.Fatal("blank change hid collection storage/binding change")
		}
	}
}
