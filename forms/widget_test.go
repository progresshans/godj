package forms_test

import (
	"testing"

	"github.com/progresshans/godj/forms"
)

func TestWidgetsAndEmptyValuesAreClosedCompatibleFieldOptions(t *testing.T) {
	text, err := forms.CharField("body", forms.WithNullable(), forms.WithRequired(false), forms.WithWidget(forms.Textarea), forms.WithEmptyValue(forms.String("")))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{text})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(forms.NewData(nil), nil)
	if err != nil || !bound.Valid() {
		t.Fatalf("empty text bind: %v", err)
	}
	if value, ok := bound.Cleaned().String("body"); !ok || value != "" || len(bound.Changed()) != 0 {
		t.Fatal("omitted optional text did not remain the empty initial string")
	}
	for _, widget := range []forms.Widget{0, forms.Checkbox, forms.Widget(255)} {
		if _, err := forms.CharField("body", forms.WithWidget(widget)); err == nil {
			t.Fatal("unsupported string widget accepted")
		}
	}
	if _, err := forms.IntegerField("amount", forms.WithWidget(forms.Textarea)); err == nil {
		t.Fatal("integer textarea accepted")
	}
	if _, err := forms.BooleanField("enabled", forms.WithWidget(forms.TextInput)); err == nil {
		t.Fatal("boolean text input accepted")
	}
	for _, value := range []forms.Value{forms.Null(), forms.String("default"), forms.Integer(0), forms.Boolean(false)} {
		if _, err := forms.CharField("body", forms.WithEmptyValue(value)); err == nil {
			t.Fatal("invalid nonnullable empty value accepted")
		}
	}
	if _, err := forms.IntegerField("amount", forms.WithEmptyValue(forms.Null())); err == nil {
		t.Fatal("integer empty policy silently ignored")
	}
}
