package serializers_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/serializers"
)

func normalizationSpec(t *testing.T, build func(string, ...serializers.FieldOption) (serializers.Field, error), options ...serializers.FieldOption) serializers.Spec {
	t.Helper()
	field, err := build("value", options...)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestStringNormalizationPrecedesLengthGrammarAndChoices(t *testing.T) {
	for _, test := range []struct {
		name, raw, want, code string
		build                 func(string, ...serializers.FieldOption) (serializers.Field, error)
		policy                serializers.StringNormalization
		maximum               int
		choices               []serializers.Choice
	}{
		{name: "contracting", raw: "e\u0301e\u0301", want: "éé", build: serializers.StringField, policy: serializers.StringUnicode16NFKC, maximum: 2},
		{name: "expanding", raw: "ﬃ", code: "max_length", build: serializers.StringField, policy: serializers.StringUnicode16NFKC, maximum: 2},
		{name: "email_grammar", raw: "Ａ＠ＥＸＡＭＰＬＥ．ＴＥＳＴ", want: "A@EXAMPLE.TEST", build: serializers.EmailField, policy: serializers.StringUnicode16NFKC},
		{name: "email_domain", raw: " Local@EXAMPLE.TEST ", want: "Local@example.test", build: serializers.EmailField, policy: serializers.StringUnicode16EmailDomain},
		{name: "combined", raw: " Ｌｏｃａｌ＠ＥＸＡＭＰＬＥ．ＴＥＳＴ ", want: "Local@example.test", build: serializers.EmailField, policy: serializers.StringUnicode16NFKCEmailDomain},
		{name: "domain_without_at", raw: " unchanged ", want: " unchanged ", build: serializers.StringField, policy: serializers.StringUnicode16EmailDomain},
		{name: "domain_full_lowercase", raw: "X@İ.ΟΣ", want: "X@i\u0307.ος", build: serializers.StringField, policy: serializers.StringUnicode16EmailDomain},
		{name: "url_grammar", raw: "ｈｔｔｐｓ：／／ｅｘａｍｐｌｅ．ｔｅｓｔ／", want: "https://example.test/", build: serializers.URLField, policy: serializers.StringUnicode16NFKC},
		{name: "slug_grammar", raw: "Ｆｒｅｄ", want: "Fred", build: serializers.SlugField, policy: serializers.StringUnicode16NFKC},
		{name: "choice_alias", raw: "Ｆｒｅｄ", want: "Fred", build: serializers.StringField, policy: serializers.StringUnicode16NFKC, choices: []serializers.Choice{{Value: serializers.String("Fred"), Label: "Canonical"}}},
		{name: "choice_outside", raw: "Ｏｔｈｅｒ", code: "invalid_choice", build: serializers.StringField, policy: serializers.StringUnicode16NFKC, choices: []serializers.Choice{{Value: serializers.String("Fred")}}},
		{name: "choice_whitespace", raw: " Ｆｒｅｄ ", code: "invalid_choice", build: serializers.StringField, policy: serializers.StringUnicode16NFKC, choices: []serializers.Choice{{Value: serializers.String("Fred")}}},
		{name: "exact", raw: "Ｆｒｅｄ", want: "Ｆｒｅｄ", build: serializers.StringField, policy: serializers.StringExact},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := []serializers.FieldOption{serializers.WithStringNormalization(test.policy), serializers.WithMaxInputBytes(128), serializers.WithTrimWhitespace(false), serializers.WithMaxLength(test.maximum)}
			if test.choices != nil {
				options = append(options, serializers.WithChoices(test.choices...))
			}
			spec := normalizationSpec(t, test.build, options...)
			object, err := serializers.NewObject(serializers.MemberOf("value", serializers.String(test.raw)))
			if err != nil {
				t.Fatal(err)
			}
			bound, err := spec.Bind(object, serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if test.code != "" {
				violations := bound.Errors().All()
				if bound.Valid() || len(violations) != 1 || string(violations[0].Code()) != test.code {
					t.Fatal("input validation order", bound.Errors())
				}
				return
			}
			value, present := bound.Values().Get("value")
			text, _ := value.AsString()
			if !bound.Valid() || !present || text != test.want {
				t.Fatal("normalization differed", bound.Errors(), text)
			}
			if len(test.choices) > 0 {
				test.choices[0].Value = serializers.String("changed")
				if text, _ := spec.Fields()[0].Choices()[0].Value.AsString(); text != "Fred" {
					t.Fatal("choice aliasing")
				}
			}
		})
	}
}

func TestStringInputBytesPrecedeTransformsAndPreserveOmissionDefaults(t *testing.T) {
	spec := normalizationSpec(t, serializers.StringField, serializers.WithMaxInputBytes(3), serializers.WithStringNormalization(serializers.StringUnicode16NFKC), serializers.WithMaxLength(1), serializers.WithDefault(serializers.String("ﬃ")), serializers.WithNullable())
	for _, test := range []struct{ name, raw, want, code string }{
		{"exact_bytes", `{"value":"e\u0301"}`, "é", ""},
		{"raw_overflow", `{"value":" e\u0301 "}`, "", "max_bytes"},
		{"expanded_length", `{"value":"ﬃ"}`, "", "max_length"},
		{"type", `{"value":3}`, "", "type"},
		{"default", `{}`, "ﬃ", ""},
		{"null", `{"value":null}`, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, err := spec.Bind(decodeObject(t, test.raw), serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if test.code != "" {
				failures := bound.Errors().All()
				if bound.Valid() || len(failures) != 1 || string(failures[0].Code()) != test.code {
					t.Fatal("wrong boundary", bound.Errors())
				}
				return
			}
			value, present := bound.Values().Get("value")
			actual, _ := value.AsString()
			if !bound.Valid() || !present || test.name == "null" && !value.IsNull() || test.name != "null" && actual != test.want {
				t.Fatal("default/null normalization", bound.Errors())
			}
		})
	}
	partial, err := spec.Bind(decodeObject(t, `{}`), serializers.ModePartial)
	if err != nil || !partial.Valid() || len(partial.Values().All()) != 0 {
		t.Fatal("partial supplied default", err)
	}
	legacy := normalizationSpec(t, serializers.StringField, serializers.WithMaxInputBytes(1), serializers.WithStringNormalization(serializers.StringUnicode16NFKC), serializers.WithDefault(serializers.String("legacy")), serializers.WithChoices(serializers.Choice{Value: serializers.String("current")}))
	bound, err := legacy.Bind(decodeObject(t, `{}`), serializers.ModeFull)
	value, _ := bound.Values().Get("value")
	text, _ := value.AsString()
	if err != nil || !bound.Valid() || text != "legacy" {
		t.Fatal("omission went through raw-byte or choice checks", err)
	}
}

func TestStringNormalizationRejectsInvalidDeclarationsAndOwnsConcurrentUse(t *testing.T) {
	for name, build := range map[string]func() (serializers.Field, error){
		"unknown": func() (serializers.Field, error) {
			return serializers.StringField("value", serializers.WithStringNormalization("private-invalid-policy"))
		},
		"negative_bytes": func() (serializers.Field, error) {
			return serializers.StringField("value", serializers.WithMaxInputBytes(-1))
		},
		"integer_policy": func() (serializers.Field, error) {
			return serializers.IntegerField("value", serializers.WithStringNormalization(serializers.StringExact))
		},
		"integer_bytes": func() (serializers.Field, error) {
			return serializers.IntegerField("value", serializers.WithMaxInputBytes(0))
		},
		"binary_bytes": func() (serializers.Field, error) {
			return serializers.BinaryField("value", serializers.WithMaxInputBytes(1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := build()
			if !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) || strings.Contains(err.Error(), "private") {
				t.Fatal("invalid policy published/leaked", err)
			}
		})
	}
	spec := normalizationSpec(t, serializers.EmailField, serializers.WithStringNormalization(serializers.StringUnicode16NFKCEmailDomain), serializers.WithMaxInputBytes(128))
	object := decodeObject(t, `{"value":"Ｌｏｃａｌ＠ＥＸＡＭＰＬＥ．ＴＥＳＴ"}`)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 16 {
				bound, err := spec.Bind(object, serializers.ModeFull)
				value, _ := bound.Values().Get("value")
				text, _ := value.AsString()
				if err != nil || !bound.Valid() || text != "Local@example.test" {
					t.Error("shared transform changed", err)
					return
				}
			}
		})
	}
	workers.Wait()
}
