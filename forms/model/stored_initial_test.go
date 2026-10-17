package model_test

import (
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
)

func TestServerOwnedFileInitialNeedsNoInputInspector(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "stored", Models: []schema.Model{{Name: "asset", GoName: "Asset", Fields: []schema.Field{
		schema.CharField("title", "Title", 30),
		schema.FileField("document", "Document", schema.Editable(false), schema.Nullable()),
		schema.ImageField("cover", "Cover", schema.Editable(false), schema.Choices(schema.Choice("current.png", "Current"))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := definition.Models[0]
	stored, err := forms.ExistingFile("legacy.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []forms.Value{forms.String("legacy.png"), stored} {
		initial := map[string]forms.Value{"document": forms.Null(), "cover": value}
		if err := formmodel.ValidateInitialValues(model, initial); err != nil {
			t.Fatal("stored file reference required an input field or image I/O capability", err)
		}
		if !initial["cover"].Equal(value) {
			t.Fatal("initial reference changed")
		}
	}
	for _, initial := range []map[string]forms.Value{
		{"cover": forms.Integer(1)}, {"cover": forms.Null()}, {"cover": forms.String("bad\x00name")},
	} {
		if err := formmodel.ValidateInitialValues(model, initial); err == nil {
			t.Fatal("malformed or nonnullable stored file value accepted")
		}
	}
	if _, err := formmodel.NewSpecForFields(model, []string{"cover"}); err == nil {
		t.Fatal("stored reference validation granted input permission")
	}
}
