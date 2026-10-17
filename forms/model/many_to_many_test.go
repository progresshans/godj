package model_test

import (
	"slices"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestManyToManyFormProjectionAndDetachedInitials(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "posts", Models: []schema.Model{{Name: "post", GoName: "Post", Fields: []schema.Field{schema.CharField("title", "Title", 40)}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("labels", "Labels", schema.Target("labels", "label"), schema.RelatedName("posts"))}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := definition.Models[0]
	spec, err := formmodel.NewSpecForFields(metadata, []string{"labels", "title"}, formmodel.OverrideField("labels", formmodel.WithRequired(false)))
	if err != nil {
		t.Fatal(err)
	}
	fields := spec.Fields()
	if len(fields) != 2 || fields[0].Name() != "title" || fields[1].Name() != "labels" || fields[1].Kind() != forms.FieldIntegerList || fields[1].Widget() != forms.SelectMultiple || !fields[1].ModelChoice() {
		t.Fatal(fields)
	}
	type row struct {
		title  string
		labels []int64
	}
	value := row{"ticket", []int64{7, 9}}
	read := func(value row, field ir.Field) (query.Value, bool) {
		return query.String(value.title), field.Name == "title"
	}
	many := func(value row, field ir.ManyToManyField) ([]int64, bool) { return value.labels, field.Name == "labels" }
	initial, err := formmodel.InitialValues(metadata, spec, value, read, many)
	if err != nil {
		t.Fatal(err)
	}
	value.labels[0] = 20
	keys, _ := initial["labels"].AsIntegers()
	if !slices.Equal(keys, []int64{7, 9}) {
		t.Fatal(keys)
	}
	if _, err := formmodel.InitialValues(metadata, spec, value, read); err == nil {
		t.Fatal("missing collection reader accepted")
	}
	if _, err := formmodel.InitialValues(metadata, spec, value, read, func(row, ir.ManyToManyField) ([]int64, bool) { return nil, false }); err == nil {
		t.Fatal("missing collection value accepted")
	}
	only, err := formmodel.NewSpecForFields(metadata, []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	if len(only.Fields()) != 1 {
		t.Fatal(only.Fields())
	}
	if _, err := formmodel.NewSpecForFields(metadata, []string{}); err == nil {
		t.Fatal("empty projection accepted")
	}
	metadata.ManyToMany[0].Name = "title"
	if _, err := formmodel.NewSpec(metadata); err == nil {
		t.Fatal("duplicate scalar and collection accepted")
	}
}
