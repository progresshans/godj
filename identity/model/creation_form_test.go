package model

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
)

func TestCreationFormPasswordInputsPreserveModelFields(t *testing.T) {
	model := models.UserDescriptor{}.Metadata()
	for _, name := range []string{"password1", "password2", "unusable_password"} {
		field := model.Fields[1].Clone()
		field.Name, field.GoName, field.Column, field.Kind, field.Unique, field.MaxLength = name, "Profile"+name, name, ir.FieldChar, false, 150
		model.Fields = append(model.Fields, field)
	}
	inputs, err := newCreationPasswordInputs(model, formmodel.Definition{Fields: []string{"username", "password1", "password2", "unusable_password"}}, 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if inputs.names.First == "password1" || inputs.names.Confirmation == "password2" || inputs.names.Unusable == "unusable_password" {
		t.Fatal("command shadows real selected profile")
	}
	spec, err := inputs.definition.Spec(model)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, first, second string
		disabled, valid     bool
	}{
		{"usable", "  λ private  ", "  λ private  ", false, true},
		{"required", "", "", false, false}, {"mismatch", "one", "two", false, false},
		{"byte_limit", strings.Repeat("界", 7), strings.Repeat("界", 7), false, false},
		{"unusable_empty", "", "", true, true}, {"unusable_ignores_password", "one", "two", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := map[string][]string{"username": {"candidate"}, "password1": {"public one"}, "password2": {"public two"}, "unusable_password": {"public profile"}, inputs.names.First: {test.first}, inputs.names.Confirmation: {test.second}}
			if test.disabled {
				data[inputs.names.Unusable] = []string{"on"}
			}
			bound, err := spec.Bind(t.Context(), forms.NewData(data), nil)
			if err != nil || bound.Valid() != test.valid {
				t.Fatal("creation password policy differs", err)
			}
			if test.valid {
				if actual, _ := bound.Cleaned().String(inputs.names.First); actual != test.first {
					t.Fatal("private password bytes changed")
				}
				if actual, _ := bound.Cleaned().String("password1"); actual != "public one" {
					t.Fatal("real profile input overwritten by command")
				}
			}
		})
	}
}
