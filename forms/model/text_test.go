package model_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
)

func TestTextProjectionUsesTextareaAndKeepsEmptyStringDistinctFromNull(t *testing.T) {
	model, err := schema.Build(schema.Definition{AppLabel: "notes", Models: []schema.Model{{Name: "note", GoName: "Note", Fields: []schema.Field{
		schema.TextField("body", "Body", schema.Nullable()),
		schema.CharField("summary", "Summary", 40, schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewSpec(model.Models[0])
	if err != nil {
		t.Fatal(err)
	}
	fields := spec.Fields()
	if fields[0].Kind() != forms.FieldChar || fields[0].Widget() != forms.Textarea || fields[0].MaxLength() != 0 || fields[0].Required() || !fields[0].Nullable() {
		t.Fatal("Text metadata did not select an optional unbounded string textarea")
	}
	if fields[1].Widget() != forms.TextInput {
		t.Fatal("Char presentation changed")
	}
	for _, raw := range []string{"", "  \r\n", "line 1\r\nline 2", strings.Repeat("長い本文 ", 600)} {
		bound, err := spec.Bind(forms.NewData(map[string][]string{"body": {raw}, "summary": {""}}), nil)
		if err != nil || !bound.Valid() {
			t.Fatalf("Text bind: %v", err)
		}
		if value, ok := bound.Cleaned().String("body"); !ok || value != strings.TrimSpace(raw) {
			t.Fatal("Text was truncated or changed into null")
		}
		if value, ok := bound.Cleaned().Get("summary"); !ok || !value.IsNull() {
			t.Fatal("nullable Char empty cleaning changed")
		}
	}
	overridden, err := formmodel.NewSpec(model.Models[0], formmodel.OverrideField("body", formmodel.WithWidget(forms.TextInput)))
	if err != nil || overridden.Fields()[0].Widget() != forms.TextInput {
		t.Fatalf("widget override: %v", err)
	}
	if empty, ok := overridden.Fields()[0].EmptyValue(); !ok || empty.IsNull() {
		t.Fatal("presentation override changed value semantics")
	}
	if _, err := formmodel.NewSpec(model.Models[0], formmodel.OverrideField("body", formmodel.WithWidget(forms.Checkbox))); err == nil {
		t.Fatal("string field accepted a boolean widget")
	}
}
