package serializers

import (
	"strings"

	"github.com/progresshans/godj/internal/emailinput"
	"github.com/progresshans/godj/internal/unicode16"
)

// StringNormalization is a closed, pure input transform. The zero value
// preserves text after the field's whitespace policy. Transforms never rewrite
// omission defaults, choice declarations or model output. Unicode operations
// use the pinned version, independently of the host Go Unicode tables.
type StringNormalization string

const (
	StringExact                    StringNormalization = ""
	StringUnicode16NFKC            StringNormalization = "unicode16_nfkc"
	StringUnicode16EmailDomain     StringNormalization = "unicode16_email_domain"
	StringUnicode16NFKCEmailDomain StringNormalization = "unicode16_nfkc_email_domain"
)

// WithStringNormalization runs after whitespace handling and before blank,
// length, grammar and choice validation. EmailDomain trims Python Unicode
// whitespace and lowercases only the domain after the final @; without an @
// it preserves its input. The combined policy applies NFKC, then EmailDomain.
// Only string-like fields support this option. No arbitrary callback is kept.
func WithStringNormalization(policy StringNormalization) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.normalization, config.normalizationSet = policy, true
	})
}

// WithMaxInputBytes bounds a submitted UTF-8 string before trimming or any
// normalization. Zero leaves that field limit unset; request parser budgets
// still apply. Omitted defaults and model output are not submitted input.
func WithMaxInputBytes(maximum int) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.maxInputBytes, config.maxInputBytesSet = maximum, true
	})
}

func (field Field) StringNormalization() StringNormalization { return field.normalization }
func (field Field) MaxInputBytes() int                       { return field.maxInputBytes }

func validStringNormalization(policy StringNormalization) bool {
	switch policy {
	case StringExact, StringUnicode16NFKC, StringUnicode16EmailDomain, StringUnicode16NFKCEmailDomain:
		return true
	}
	return false
}

func stringFieldKind(kind FieldKind) bool {
	return kind == FieldString || kind == FieldEmail || kind == FieldURL || kind == FieldSlug
}

func normalizeStringInput(field Field, text string) string {
	if field.trimWhitespace {
		if field.kind == FieldString {
			text = strings.TrimSpace(text)
		} else {
			text = unicode16.TrimSpace(text)
		}
	}
	switch field.normalization {
	case StringUnicode16NFKC:
		text = unicode16.NFKC(text)
	case StringUnicode16EmailDomain:
		text = emailinput.NormalizeDomain(text)
	case StringUnicode16NFKCEmailDomain:
		text = emailinput.NormalizeDomain(unicode16.NFKC(text))
	}
	return text
}
