package forms_test

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/unicode16"
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
	bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"username": {" Ｆｒｅｄ "}}), initial)
	if err != nil || !bound.Valid() {
		t.Fatal(err, bound.Errors())
	}
	if value, ok := bound.Cleaned().String("username"); !ok || value != "Fred" || len(bound.Changed()) != 0 {
		t.Fatal("normalized value/change comparison diverged")
	}
	expanded, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"ﬃﬃ"}}), nil)
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
		bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"username": {"input"}}), nil)
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
			return forms.CharField("value", forms.WithMaxInputBytes(-1))
		},
		func() (forms.Field, error) {
			return forms.IntegerField("value", forms.WithMaxInputBytes(0))
		},
	} {
		if _, err := build(); err == nil {
			t.Fatal("unsupported normalizer accepted")
		}
	}
}

func TestStringChoiceNormalizerRetainsRawInitialAndExactCanonicalChoices(t *testing.T) {
	var calls atomic.Int64
	choices := []forms.Choice{{Value: forms.String("Fred"), Label: "Canonical"}}
	field, err := forms.CharField("value", forms.WithMaxLength(4), forms.WithMaxInputBytes(16), forms.WithChoices(choices...), forms.WithStringNormalizer(func(value string) string { calls.Add(1); return unicode16.NFKC(value) }))
	if err != nil {
		t.Fatal(err)
	}
	choices[0].Value = forms.String("changed")
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("construction evaluated callback")
	}
	for _, test := range []struct{ name, raw, code string }{
		{"alias", "Ｆｒｅｄ", ""}, {"canonical", "Fred", ""}, {"whitespace", " Ｆｒｅｄ ", "invalid_choice"}, {"other", "Ｏｔｈｅｒ", "invalid_choice"}, {"empty", "", "required"}, {"raw_bytes", strings.Repeat(" ", 17) + "Fred", "max_bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"value": {test.raw}}), map[string]forms.Value{"value": forms.String("Fred")})
			if err != nil {
				t.Fatal(err)
			}
			if test.code != "" {
				items := bound.Errors().All()
				if bound.Valid() || len(items) != 1 || string(items[0].Code()) != test.code {
					t.Fatal("choice validation", bound.Errors())
				}
				if test.code == "max_bytes" && calls.Load() != before {
					t.Fatal("raw limit ran normalizer during clean/change")
				}
				return
			}
			value, _ := bound.Cleaned().String("value")
			initial, _ := bound.Initial().String("value")
			raw, _ := bound.Submitted().Get("value")
			if !bound.Valid() || value != "Fred" || initial != "Fred" || len(bound.Changed()) != 0 || len(raw) != 1 || raw[0] != test.raw {
				t.Fatal("choice normalization changed raw/initial/change semantics", bound.Errors())
			}
		})
	}
	before := calls.Load()
	unbound, err := spec.Unbound(map[string]forms.Value{"value": forms.String("old unrestricted initial")})
	initial, _ := unbound.Initial().String("value")
	if err != nil || initial != "old unrestricted initial" || calls.Load() != before {
		t.Fatal("input bounds changed stored initial", err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 8 {
				bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"value": {"Ｆｒｅｄ"}}), nil)
				value, _ := bound.Cleaned().String("value")
				if err != nil || !bound.Valid() || value != "Fred" {
					t.Error("concurrent choice normalization", err)
					return
				}
			}
		})
	}
	workers.Wait()
}

func TestStringInputByteLimitPrecedesTrimmingAndCallbacks(t *testing.T) {
	for _, choices := range []bool{false, true} {
		name := "text"
		if choices {
			name = "choices"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			options := []forms.FieldOption{forms.WithRequired(false), forms.WithMaxInputBytes(2), forms.WithStringNormalizer(func(string) string { calls++; return "é" })}
			if choices {
				options = append(options, forms.WithChoices(forms.Choice{Value: forms.String("é")}))
			}
			field, err := forms.CharField("value", options...)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"value": {"é"}}), nil)
			if err != nil || !accepted.Valid() || calls != 1 {
				t.Fatal("exact UTF-8 byte boundary", err, calls)
			}
			for _, raw := range [][]string{{" é "}, {"x", "y"}} {
				before := calls
				bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"value": raw}), nil)
				if err != nil || bound.Valid() || calls != before {
					t.Fatal("pre-normalization rejection called a callback", err, calls)
				}
			}
		})
	}
}
