package forms_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	"golang.org/x/text/unicode/norm"
)

func TestStringNormalizerPrecedesValidationAndChangedWithoutRewritingInitial(t *testing.T) {
	field, err := forms.CharField("username", forms.WithMaxLength(4), forms.WithStringNormalizer(norm.NFKC.String))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	initial := map[string]forms.Value{"username": forms.String("Fred")}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"username": {" Ｆｒｅｄ "}}), initial)
	if err != nil || !bound.Valid() {
		t.Fatal(err, bound.Errors())
	}
	if value, ok := bound.Cleaned().String("username"); !ok || value != "Fred" || len(bound.Changed()) != 0 {
		t.Fatal("normalized value/change comparison diverged")
	}
	expanded, err := spec.Bind(forms.NewData(map[string][]string{"username": {"ﬃﬃ"}}), nil)
	if err != nil || expanded.Valid() || expanded.Errors().ByField("username").Empty() {
		t.Fatal("normalization bypassed maximum length", err)
	}
	unbound, err := spec.Unbound(map[string]forms.Value{"username": forms.String("Ｆｒｅｄ")})
	if value, _ := unbound.Initial().String("username"); err != nil || value != "Ｆｒｅｄ" {
		t.Fatal("normalization rewrote model initial", err)
	}
	for _, normalize := range []func(string) string{func(string) string { return "" }, func(string) string { return "x\x00y" }, func(string) string { return string([]byte{255}) }} {
		field, err := forms.CharField("username", forms.WithStringNormalizer(normalize))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		bound, err := spec.Bind(forms.NewData(map[string][]string{"username": {"input"}}), nil)
		if err != nil || bound.Valid() {
			t.Fatal("normalizer bypassed required/valid string checks", err)
		}
	}
}

func TestStringNormalizerRejectsUnsupportedStartupCombinations(t *testing.T) {
	for _, build := range []func() (forms.Field, error){
		func() (forms.Field, error) { return forms.CharField("value", forms.WithStringNormalizer(nil)) },
		func() (forms.Field, error) {
			return forms.IntegerField("value", forms.WithStringNormalizer(strings.ToUpper))
		},
		func() (forms.Field, error) {
			return forms.ModelMultipleChoiceField("value", forms.WithStringNormalizer(strings.ToUpper))
		},
		func() (forms.Field, error) {
			return forms.CharField("value", forms.WithStringNormalizer(strings.ToUpper), forms.WithChoices(forms.Choice{Value: forms.String("a"), Label: "A"}))
		},
	} {
		if _, err := build(); err == nil {
			t.Fatal("unsupported normalizer accepted")
		}
	}
}
