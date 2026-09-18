package forms_test

import (
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestChoiceOwnershipRawMembershipAndValidators(t *testing.T) {
	choices := []forms.Choice{{Value: forms.Integer(0), Label: "Normal"}, {Value: forms.Integer(1), Label: "High"}}
	option := forms.WithChoices(choices...)
	choices[0].Value = forms.Integer(99)
	called := 0
	field, err := forms.IntegerField("value", option, forms.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		called++
		if value.Equal(forms.Integer(1)) {
			return validation.NewErrors(validation.New("value", "application_policy"))
		}
		return validation.NewErrors()
	})))
	if err != nil {
		t.Fatal(err)
	}
	field.Choices()[0].Value = forms.Integer(99)
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ input, code string }{
		{"0", ""}, {"1", "application_policy"}, {"01", "invalid_choice"}, {" 0 ", "invalid_choice"}, {"+0", "invalid_choice"}, {"0.0", "invalid_choice"}, {"", "required"},
	} {
		bound, err := spec.Bind(forms.NewData(map[string][]string{"value": {test.input}}), map[string]forms.Value{"value": forms.Integer(0)})
		if err != nil {
			t.Fatal(err)
		}
		failures := bound.Errors().All()
		if test.code == "" {
			if !bound.Valid() || len(bound.Changed()) != 0 {
				t.Fatal("unchanged valid choice was lost")
			}
		} else if len(failures) != 1 || string(failures[0].Code()) != test.code {
			t.Fatalf("%q: %v", test.input, failures)
		}
	}
	if called != 2 {
		t.Fatalf("custom validators ran %d times, want only accepted scalar values", called)
	}
	if initial, err := spec.Unbound(map[string]forms.Value{"value": forms.Integer(99)}); err != nil {
		t.Fatal(err)
	} else if got, _ := initial.Initial().Integer("value"); got != 99 {
		t.Fatal("old stored value was lost")
	}
	for _, options := range [][]forms.FieldOption{
		{forms.WithChoices()},
		{forms.WithChoices(forms.Choice{Value: forms.Null()})},
		{forms.WithChoices(forms.Choice{Value: forms.String("0")})},
		{forms.WithChoices(forms.Choice{Value: forms.Integer(0)}, forms.Choice{Value: forms.Integer(0)})},
		{forms.WithChoices(forms.Choice{Value: forms.Integer(0), Label: "bad\x00"})},
		{option, forms.WithWidget(forms.Textarea)},
	} {
		if _, err := forms.IntegerField("value", options...); err == nil {
			t.Fatal("invalid choice configuration accepted")
		}
	}
}

func TestChoiceStringEmptyAndWhitespaceSemantics(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		options := []forms.FieldOption{forms.WithRequired(false), forms.WithChoices(forms.Choice{Value: forms.String(" spaced "), Label: "Keep spaces"})}
		if nullable {
			options = append(options, forms.WithNullable())
		}
		field, err := forms.CharField("value", options...)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{"", " spaced ", "spaced"} {
			bound, err := spec.Bind(forms.NewData(map[string][]string{"value": {raw}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			if raw == "spaced" {
				if bound.Valid() {
					t.Fatal("choice input was trimmed")
				}
				continue
			}
			value, found := bound.Cleaned().Get("value")
			if !bound.Valid() || !found {
				t.Fatal("valid choice missing")
			}
			if raw == "" && nullable {
				if !value.IsNull() {
					t.Fatal("null became blank string")
				}
			} else if got, _ := value.AsString(); got != raw {
				t.Fatal("string changed")
			}
		}
	}
}
