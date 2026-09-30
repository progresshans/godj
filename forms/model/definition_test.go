package model

import (
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func TestDefinitionKeepsIRSelectionAndCommandInputsSeparate(t *testing.T) {
	metadata := ir.Model{Name: "article", GoName: "Article", Fields: []ir.Field{
		{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 40},
		{Name: "private", GoName: "Private", Column: "private", Kind: ir.FieldChar, MaxLength: 40},
	}}
	confirm, err := forms.CharField("confirmation")
	if err != nil {
		t.Fatal(err)
	}
	definition := Definition{Fields: []string{"title"}, ExtraFields: []forms.Field{confirm}, Validators: []forms.CrossValidator{forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		title, _ := values.String("title")
		confirmation, _ := values.String("confirmation")
		if title != confirmation {
			return validation.NewErrors(validation.New("confirmation", "mismatch"))
		}
		return validation.Errors{}
	})}}
	copy := definition.Clone()
	spec, err := copy.Spec(metadata)
	if err != nil {
		t.Fatal(err)
	}
	copy.Fields[0] = "private"
	copy.ExtraFields[0] = forms.Field{}
	copy.Validators[0] = nil
	if definition.Fields[0] != "title" || definition.ExtraFields[0].Name() != "confirmation" || definition.Validators[0] == nil {
		t.Fatal("clone aliases options")
	}
	bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"title": {"Chosen"}, "confirmation": {"Different"}, "private": {"ignored"}}), nil)
	if err != nil || bound.Valid() || bound.Errors().ByField("confirmation").Len() != 1 {
		t.Fatal("command validator omitted", err)
	}
	if _, present := bound.Cleaned().Get("private"); present {
		t.Fatal("omitted model field became input")
	}
	private, err := forms.CharField("private")
	if err != nil {
		t.Fatal(err)
	}
	shadow := Definition{Fields: []string{"title"}, ExtraFields: []forms.Field{private}}
	if _, err := shadow.Spec(metadata); err == nil {
		t.Fatal("extra input shadowed an excluded model field")
	}
	empty := Definition{Fields: []string{}}.Clone()
	if empty.Fields == nil {
		t.Fatal("explicit empty selection became all fields")
	}
	if _, err := empty.Spec(metadata); err == nil {
		t.Fatal("empty selection unexpectedly admitted all model fields")
	}
}
