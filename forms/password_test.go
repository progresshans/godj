package forms_test

import (
	"fmt"
	"github.com/progresshans/godj/forms"
	"strings"
	"testing"
)

func TestUnstrippedPasswordPreservesWhitespaceAndNeverFormatsPrivateValues(t *testing.T) {
	field, err := forms.CharField("password", forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithMaxLength(32))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"  private-λ<&>  ", " \t\n", "　秘密　"} {
		data := forms.NewData(map[string][]string{"password": {password}})
		bound, err := spec.Bind(data, nil)
		if err != nil || !bound.Valid() {
			t.Fatal("password rejected", err)
		}
		if actual, ok := bound.Cleaned().String("password"); !ok || actual != password {
			t.Fatal("password was stripped")
		}
		for _, value := range []any{data, bound, bound.Cleaned(), bound.Cleaned().All()[0], forms.String(password)} {
			for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%p", "%w"} {
				if text := fmt.Sprintf(format, value); strings.Contains(text, password) {
					t.Fatal("private form value exposed by formatting")
				}
			}
		}
		unchanged, err := spec.Bind(data, map[string]forms.Value{"password": forms.String(password)})
		if err != nil || len(unchanged.Changed()) != 0 {
			t.Fatal("unstripped initial value changed")
		}
	}
	for _, raw := range [][]string{nil, {""}, {"x\x00y"}, {"\xff"}, {"one", "two"}, {strings.Repeat("λ", 33)}} {
		bound, err := spec.Bind(forms.NewData(map[string][]string{"password": raw}), nil)
		if err != nil || bound.Valid() {
			t.Fatal("invalid password shape accepted", err)
		}
	}
	plain, _ := forms.CharField("plain")
	plainSpec, _ := forms.NewSpec([]forms.Field{plain})
	bound, err := plainSpec.Bind(forms.NewData(map[string][]string{"plain": {"  text  "}}), nil)
	text, _ := bound.Cleaned().String("plain")
	if err != nil || !bound.Valid() || text != "text" {
		t.Fatal("default string cleaning changed")
	}
}

func TestPasswordOptionsRejectIncompatibleFieldsAndSecretDefaults(t *testing.T) {
	if _, err := forms.IntegerField("number", forms.WithTrimWhitespace(false)); err == nil {
		t.Fatal("integer accepted string cleaning policy")
	}
	if _, err := forms.IntegerField("number", forms.WithWidget(forms.PasswordInput)); err == nil {
		t.Fatal("integer accepted password widget")
	}
	if _, err := forms.CharField("password", forms.WithWidget(forms.PasswordInput), forms.WithDefault(forms.String("private"))); err == nil {
		t.Fatal("password default accepted")
	}
	field, _ := forms.CharField("password", forms.WithTrimWhitespace(false), forms.WithWidget(forms.PasswordInput), forms.WithMaxLength(1))
	spec, _ := forms.NewSpec([]forms.Field{field})
	bound, err := spec.Bind(forms.NewData(map[string][]string{"password": {" x "}}), map[string]forms.Value{"password": forms.String("x")})
	if err != nil || bound.Valid() || len(bound.Changed()) != 1 {
		t.Fatal("failed unstripped validation lost changed state")
	}
}
