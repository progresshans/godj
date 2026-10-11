package serializers_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/slugtest"
	"github.com/progresshans/godj/serializers"
)

func TestSlugInputsAgainstPinnedDRF(t *testing.T) {
	reference, inputs := slugtest.Load(t, "sqlite")
	for _, profile := range []string{"required", "unicode", "optional", "model", "model_unicode", "short", "untrimmed", "unicode_untrimmed"} {
		t.Run(profile, func(t *testing.T) {
			options := []serializers.FieldOption{serializers.WithAllowUnicode(profile == "unicode" || profile == "model_unicode" || profile == "unicode_untrimmed")}
			if profile == "untrimmed" || profile == "unicode_untrimmed" {
				options = append(options, serializers.WithTrimWhitespace(false))
			}
			if profile == "optional" {
				options = append(options, serializers.WithAllowEmpty(), serializers.WithNullable())
			}
			if profile == "model" || profile == "model_unicode" {
				options = append(options, serializers.WithMaxLength(50), serializers.WithAllowEmpty())
			}
			if profile == "short" {
				options = append(options, serializers.WithMaxLength(12))
			}
			field, err := serializers.SlugField("address", options...)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := serializers.NewSpec([]serializers.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range inputs {
				t.Run(input.Name, func(t *testing.T) {
					wanted, found := reference.Serializers[profile][input.Name]
					if !found {
						t.Fatal("native serializer input observation is missing")
					}
					value := serializers.Null()
					if input.Value != nil {
						value = serializers.String(*input.Value)
					}
					object, err := serializers.NewObject(serializers.MemberOf("address", value))
					if input.Value != nil && strings.ContainsRune(*input.Value, 0) {
						// Go's closed JSON string domain rejects NUL before field
						// validators. This is a transport difference, not an slug
						// error-order match or a skipped native case.
						var rejected *serializers.Error
						if !errors.As(err, &rejected) || rejected.Code != serializers.CodeInvalidValue || rejected.Field != "object.address" || !slices.Contains(wanted.Codes, "null_characters_not_allowed") {
							t.Fatal("slug bypassed the existing NUL transport boundary")
						}
						if _, published := object.Get("address"); published {
							t.Fatal("invalid JSON object published its slug value")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					result, err := spec.Bind(object, serializers.ModeFull)
					if err != nil {
						t.Fatal(err)
					}
					codes := []string{}
					for _, item := range result.Errors().All() {
						codes = append(codes, string(item.Code()))
					}
					if profile == "untrimmed" && input.Name == "newline_end" {
						// Explicit deviation: DRF's ASCII "$" accepts a final LF.
						// GoDj uses Django's absolute-end slug grammar in JSON too.
						if len(wanted.Codes) != 0 || wanted.Value == nil || *wanted.Value != *input.Value || !reflect.DeepEqual(codes, []string{"invalid"}) || result.Valid() {
							t.Fatal("untrimmed ASCII LF boundary or native witness changed")
						}
						if _, exists := result.Values().Get("address"); exists {
							t.Fatal("rejected LF published a value")
						}
						return
					}
					if !reflect.DeepEqual(codes, wanted.Codes) {
						t.Fatalf("codes %v want %v", codes, wanted.Codes)
					}
					got, exists := result.Values().Get("address")
					if len(codes) > 0 {
						if exists || result.Valid() {
							t.Fatal("invalid slug published cleaned JSON")
						}
						return
					}
					if !exists || !result.Valid() {
						t.Fatal("valid slug lost cleaned JSON")
					}
					if wanted.Value == nil {
						if !got.IsNull() {
							t.Fatal("null slug changed")
						}
						return
					}
					if text, ok := got.AsString(); !ok || text != *wanted.Value {
						t.Fatal("JSON slug normalization changed native value")
					}
				})
			}
		})
	}
}

func TestSlugOmissionDefaultsPreservePinnedDRFValues(t *testing.T) {
	reference, _ := slugtest.Load(t, "sqlite")
	if len(reference.Defaults) != 4 {
		t.Fatal("native slug default observations are incomplete")
	}
	for name, raw := range reference.Defaults {
		t.Run(name, func(t *testing.T) {
			value := serializers.Null()
			if raw != nil {
				value = serializers.String(*raw)
			}
			field, err := serializers.SlugField("address", serializers.WithNullable(), serializers.WithAllowEmpty(), serializers.WithDefault(value))
			if err != nil {
				t.Fatal(err)
			}
			spec, err := serializers.NewSpec([]serializers.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			empty, err := serializers.NewObject()
			if err != nil {
				t.Fatal(err)
			}
			result, err := spec.Bind(empty, serializers.ModeFull)
			if err != nil || !result.Valid() {
				t.Fatal("slug omission default was validated as submitted input", err)
			}
			got, exists := result.Values().Get("address")
			if !exists {
				t.Fatal("slug omission default disappeared")
			}
			if raw == nil {
				if !got.IsNull() {
					t.Fatal("null slug default changed")
				}
			} else if text, ok := got.AsString(); !ok || text != *raw {
				t.Fatal("slug default was implicitly trimmed or validated")
			}
			partial, err := spec.Bind(empty, serializers.ModePartial)
			if err != nil || !partial.Valid() {
				t.Fatal(err)
			}
			if _, exists := partial.Values().Get("address"); exists {
				t.Fatal("partial slug input acquired an omission default")
			}
		})
	}
}

func TestSlugUnicodeSerializerOptionRequiresSlugKind(t *testing.T) {
	for _, allow := range []bool{true, false} {
		if _, err := serializers.StringField("address", serializers.WithAllowUnicode(allow)); err == nil {
			t.Fatal("slug option accepted on generic string")
		}
	}
}
