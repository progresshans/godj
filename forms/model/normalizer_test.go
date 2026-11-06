package model_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/schema/ir"
	"golang.org/x/text/unicode/norm"
)

func TestModelStringPolicyNarrowsInputWithoutChangingIR(t *testing.T) {
	metadata := models.UserDescriptor{}.Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"username"}, formmodel.OverrideField("username", formmodel.WithMaxLength(4), formmodel.WithStringNormalizer(norm.NFKC.String)))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"Ｆｒｅｄ"}}), nil)
	if value, _ := bound.Cleaned().String("username"); err != nil || !bound.Valid() || value != "Fred" {
		t.Fatal("projected policy not applied", err)
	}
	if spec.Fields()[0].MaxLength() != 4 {
		t.Fatal("input length override lost")
	}
	for _, field := range metadata.Fields {
		if field.Name == "username" && field.MaxLength != 256 {
			t.Fatal("input policy mutated storage IR")
		}
	}
	for _, override := range []formmodel.Override{
		formmodel.OverrideField("username", formmodel.WithMaxLength(257)), formmodel.OverrideField("username", formmodel.WithMaxLength(0)), formmodel.OverrideField("username", formmodel.WithStringNormalizer(nil)),
		formmodel.OverrideField("active", formmodel.WithStringNormalizer(strings.ToUpper)), formmodel.OverrideField("groups", formmodel.WithMaxLength(1)),
	} {
		if _, err := formmodel.NewSpecForFields(metadata, []string{"username", "active", "groups"}, override); err == nil {
			t.Fatal("invalid/broader form policy accepted")
		}
	}
}

func TestModelChoiceNormalizerValidatesCanonicalInputWithoutRewritingMetadata(t *testing.T) {
	metadata := (models.UserDescriptor{}).Metadata()
	for index := range metadata.Fields {
		if metadata.Fields[index].Name == "username" {
			metadata.Fields[index].MaxLength = 4
			metadata.Fields[index].Choices = []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "Fred"}, Label: "Canonical"}}
		}
	}
	original := metadata.Clone()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"username"}, formmodel.OverrideField("username", formmodel.WithStringNormalizer(unicode16.NFKC), formmodel.WithMaxInputBytes(12)))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"Ｆｒｅｄ"}}), map[string]forms.Value{"username": forms.String("Fred")})
	value, _ := bound.Cleaned().String("username")
	if err != nil || !bound.Valid() || value != "Fred" || len(bound.Changed()) != 0 || !metadata.Equal(original) {
		t.Fatal("model policy lost choice/source", err, bound.Errors())
	}
	if spec.Fields()[0].MaxInputBytes() != 12 || spec.Fields()[0].Choices()[0].InputValue() != "Fred" {
		t.Fatal("model projection changed constraints/choice")
	}
	for _, name := range []string{"active", "groups"} {
		if _, err := formmodel.NewSpecForFields(metadata, []string{name}, formmodel.OverrideField(name, formmodel.WithMaxInputBytes(1))); err == nil {
			t.Fatal("byte policy accepted non-string model field")
		}
	}
}
