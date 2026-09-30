package model_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/models"
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
