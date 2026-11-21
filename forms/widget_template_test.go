package forms_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestWidgetTemplateSelectionKeepsNativeInputAndValidators(t *testing.T) {
	for _, name := range []string{"", "../control", "_private", "with-space", strings.Repeat("x", forms.MaximumWidgetTemplateNameBytes+1)} {
		if _, err := forms.CharField("text", forms.WithWidgetTemplate(name)); err == nil {
			t.Fatalf("invalid template name accepted: %q", name)
		}
	}
	if _, err := forms.CharField("secret", forms.WithWidget(forms.PasswordInput), forms.WithWidgetTemplate("custom")); err == nil {
		t.Fatal("password accepted a custom renderer")
	}
	if _, err := forms.IntegerField("identity", forms.WithWidget(forms.HiddenInput), forms.WithWidgetTemplate("custom")); err == nil {
		t.Fatal("hidden identity accepted a custom renderer")
	}
	field, err := forms.IntegerField("quantity", forms.WithWidgetTemplate("quantity_control"), forms.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		integer, _ := value.AsInteger()
		if integer == 7 {
			return validation.NewErrors(validation.New("quantity", "reserved"))
		}
		return validation.Errors{}
	})))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input string
		valid bool
		code  validation.Code
	}{{"007", false, "reserved"}, {"text", false, "invalid"}, {"9007199254740993", true, ""}} {
		bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"quantity": {test.input}}), nil)
		if err != nil || bound.Valid() != test.valid {
			t.Fatalf("bind %q: %v", test.input, err)
		}
		if !test.valid && (bound.Errors().Len() != 1 || bound.Errors().All()[0].Code() != test.code) {
			t.Fatal("widget replaced native or custom validation", bound.Errors())
		}
		raw, _ := bound.Submitted().Get("quantity")
		if len(raw) != 1 || raw[0] != test.input || spec.Fields()[0].WidgetTemplate() != "quantity_control" || spec.Fields()[0].Widget() != forms.TextInput {
			t.Fatal("widget selection rewrote raw input or native type")
		}
		if test.valid {
			if value, _ := bound.Cleaned().Integer("quantity"); value != 9007199254740993 {
				t.Fatal("widget converted exact integer through a display representation")
			}
		}
	}
}
