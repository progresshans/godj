package serializers_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/urltest"
	"github.com/progresshans/godj/serializers"
)

func TestURLInputsAgainstPinnedDRF(t *testing.T) {
	reference, inputs := urltest.Load(t, "sqlite")
	for _, profile := range []string{"required", "optional", "model", "short"} {
		t.Run(profile, func(t *testing.T) {
			options := []serializers.FieldOption{}
			if profile == "optional" {
				options = append(options, serializers.WithAllowEmpty(), serializers.WithNullable())
			}
			if profile == "model" {
				options = append(options, serializers.WithMaxLength(200), serializers.WithAllowEmpty())
			}
			if profile == "short" {
				options = append(options, serializers.WithMaxLength(24))
			}
			field, err := serializers.URLField("address", options...)
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
						// validators. This is a transport difference, not an url
						// error-order match or a skipped native case.
						var rejected *serializers.Error
						if !errors.As(err, &rejected) || rejected.Code != serializers.CodeInvalidValue || rejected.Field != "object.address" || !slices.Contains(wanted.Codes, "null_characters_not_allowed") {
							t.Fatal("url bypassed the existing NUL transport boundary")
						}
						if _, published := object.Get("address"); published {
							t.Fatal("invalid JSON object published its url value")
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
					if !reflect.DeepEqual(codes, wanted.Codes) {
						t.Fatalf("codes %v want %v", codes, wanted.Codes)
					}
					got, exists := result.Values().Get("address")
					if len(codes) > 0 {
						if exists || result.Valid() {
							t.Fatal("invalid url published cleaned JSON")
						}
						return
					}
					if !exists || !result.Valid() {
						t.Fatal("valid url lost cleaned JSON")
					}
					if wanted.Value == nil {
						if !got.IsNull() {
							t.Fatal("null url changed")
						}
						return
					}
					if text, ok := got.AsString(); !ok || text != *wanted.Value {
						t.Fatal("JSON url normalization changed native value")
					}
				})
			}
		})
	}
}

func TestURLOmissionDefaultsPreservePinnedDRFValues(t *testing.T) {
	reference, _ := urltest.Load(t, "sqlite")
	if len(reference.Defaults) != 4 {
		t.Fatal("native url default observations are incomplete")
	}
	for name, raw := range reference.Defaults {
		t.Run(name, func(t *testing.T) {
			value := serializers.Null()
			if raw != nil {
				value = serializers.String(*raw)
			}
			field, err := serializers.URLField("address", serializers.WithNullable(), serializers.WithAllowEmpty(), serializers.WithDefault(value))
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
				t.Fatal("url omission default was validated as submitted input", err)
			}
			got, exists := result.Values().Get("address")
			if !exists {
				t.Fatal("url omission default disappeared")
			}
			if raw == nil {
				if !got.IsNull() {
					t.Fatal("null url default changed")
				}
			} else if text, ok := got.AsString(); !ok || text != *raw {
				t.Fatal("url default was implicitly trimmed or validated")
			}
			partial, err := spec.Bind(empty, serializers.ModePartial)
			if err != nil || !partial.Valid() {
				t.Fatal(err)
			}
			if _, exists := partial.Values().Get("address"); exists {
				t.Fatal("partial url input acquired an omission default")
			}
		})
	}
}
