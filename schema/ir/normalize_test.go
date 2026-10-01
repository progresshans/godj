package ir_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestNormalizeAddsImplicitAutoFieldAndDefaults(t *testing.T) {
	t.Parallel()

	got, err := schema.Build(schema.Definition{
		AppLabel: "news",
		Models: []schema.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []schema.Field{
				schema.CharField("title", "Title", 200),
				schema.BooleanField("published", "Published", schema.Default(false)),
				schema.CharField("summary", "Summary", 200, schema.Nullable()),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	model := got.Models[0]
	if model.DBTable != "news_article" {
		t.Fatalf("DBTable = %q, want news_article", model.DBTable)
	}
	if len(model.Fields) != 4 || model.Fields[0].Name != "id" || !model.Fields[0].PrimaryKey {
		t.Fatalf("implicit primary key = %#v", model.Fields)
	}
	if !model.Fields[3].Nullable {
		t.Fatal("summary should be nullable")
	}
	if got.FormatVersion != ir.CurrentFormatVersion {
		t.Fatalf("FormatVersion = %d, want %d", got.FormatVersion, ir.CurrentFormatVersion)
	}
	publishedDefault := model.Fields[2].Default
	if publishedDefault == nil || publishedDefault.Kind != ir.ScalarBoolean || publishedDefault.Boolean {
		t.Fatalf("published default = %#v, want explicit boolean false", publishedDefault)
	}
}

func TestCanonicalHashIsStableAndInputIsNotMutated(t *testing.T) {
	t.Parallel()

	input := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "news",
		Models: []ir.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
		}},
	}
	first, err := ir.Hash(input)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	second, err := ir.Hash(input)
	if err != nil {
		t.Fatalf("Hash() second error = %v", err)
	}
	if first != second {
		t.Fatalf("hash changed: %s != %s", first, second)
	}
	if input.FormatVersion != ir.CurrentFormatVersion || len(input.Models[0].Fields) != 1 || input.Models[0].DBTable != "" {
		t.Fatalf("Normalize mutated input: %#v", input)
	}
	if len(first) != 64 {
		t.Fatalf("hash length = %d, want 64", len(first))
	}
}

func TestNormalizeNullableBooleanPreservesDefaultAndDistinctIdentity(t *testing.T) {
	t.Parallel()
	hashes := make(map[string]bool)
	for _, defaultValue := range []*ir.Scalar{nil, {Kind: ir.ScalarBoolean}, {Kind: ir.ScalarBoolean, Boolean: true}} {
		input := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "news", Models: []ir.Model{{Name: "article", GoName: "Article", Fields: []ir.Field{
			{Name: "reviewed", GoName: "Reviewed", Kind: ir.FieldBoolean, Nullable: true, Default: defaultValue},
		}}}}
		got, err := ir.Normalize(input)
		if err != nil {
			t.Fatal(err)
		}
		field := got.Models[0].Fields[1]
		if !field.Nullable || field.Kind != ir.FieldBoolean || !reflect.DeepEqual(field.Default, defaultValue) {
			t.Fatalf("nullable Boolean changed default: %+v", field)
		}
		hash, err := ir.Hash(got)
		if err != nil || hashes[hash] {
			t.Fatalf("omitted/false/true defaults share an identity: %v", err)
		}
		hashes[hash] = true
		if defaultValue != nil {
			field.Default.Boolean = !field.Default.Boolean
			if field.Default.Boolean == defaultValue.Boolean {
				t.Fatal("normalized Boolean default aliases caller metadata")
			}
		}
	}
}

func TestNormalizeRejectsDuplicateFields(t *testing.T) {
	t.Parallel()

	_, err := schema.Build(schema.Definition{
		AppLabel: "news",
		Models: []schema.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []schema.Field{
				schema.CharField("title", "Title", 200),
				schema.CharField("title", "OtherTitle", 200),
			},
		}},
	})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) || validation.Code != "duplicate" {
		t.Fatalf("error = %v, want duplicate ValidationError", err)
	}
}

func TestNormalizeRejectsMismatchedTypedDefault(t *testing.T) {
	t.Parallel()

	_, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "news",
		Models: []ir.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []ir.Field{{
				Name:    "published",
				GoName:  "Published",
				Kind:    ir.FieldBoolean,
				Default: &ir.Scalar{Kind: ir.ScalarString, String: "false"},
			}},
		}},
	})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) || validation.Code != "type_mismatch" {
		t.Fatalf("error = %v, want type_mismatch ValidationError", err)
	}
}

func TestSchemaCloneDoesNotShareDefaultState(t *testing.T) {
	t.Parallel()

	input := ir.Schema{Models: []ir.Model{{Fields: []ir.Field{{Default: &ir.Scalar{Kind: ir.ScalarBoolean}}}}}}
	clone := input.Clone()
	clone.Models[0].Fields[0].Default.Boolean = true
	if input.Models[0].Fields[0].Default.Boolean {
		t.Fatal("Clone shared typed default pointer with its input")
	}
}

func TestNormalizeAndHashOwnsSchemaAndReturnsZeroOnFailure(t *testing.T) {
	t.Parallel()

	input := relationSchema()
	input.Models[0].Fields = append(input.Models[0].Fields, ir.Field{
		Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 100,
		Default: &ir.Scalar{Kind: ir.ScalarString, String: "original"},
	})
	normalized, hash, err := ir.NormalizeAndHash(input)
	if err != nil {
		t.Fatalf("NormalizeAndHash() error = %v", err)
	}
	input.Models[0].Fields[1].Relation.Target.AppLabel = "mutated"
	input.Models[0].Fields[2].Default.String = "mutated"
	if normalized.Models[0].Fields[1].Relation.Target.AppLabel != "authors" ||
		normalized.Models[0].Fields[2].Default.String != "original" {
		t.Fatal("NormalizeAndHash() retained mutable caller state")
	}
	if got, err := ir.Hash(normalized); err != nil || got != hash {
		t.Fatalf("prepared hash = %q, Hash(normalized) = %q, error %v", hash, got, err)
	}
	normalized, hash, err = ir.NormalizeAndHash(ir.Schema{})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) || !reflect.DeepEqual(normalized, ir.Schema{}) || hash != "" {
		t.Fatalf("invalid schema returned (%#v, %q, %v), want zero values and ValidationError", normalized, hash, err)
	}
}

func TestNormalizeRejectsNonCurrentSchemaIRVersion(t *testing.T) {
	t.Parallel()

	for _, version := range []int{0, ir.CurrentFormatVersion + 1} {
		version := version
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			t.Parallel()
			_, err := ir.Normalize(ir.Schema{FormatVersion: version, AppLabel: "news"})
			var validation *ir.ValidationError
			if !errors.As(err, &validation) || validation.Code != "unsupported_version" || validation.Path != "format_version" {
				t.Fatalf("error = %#v, want format_version unsupported_version ValidationError", err)
			}
		})
	}
}

func TestNormalizeRejectsInvalidUTF8StringDefault(t *testing.T) {
	t.Parallel()

	_, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "news",
		Models: []ir.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []ir.Field{{
				Name:      "title",
				GoName:    "Title",
				Kind:      ir.FieldChar,
				MaxLength: 20,
				Default:   &ir.Scalar{Kind: ir.ScalarString, String: string([]byte{0xff})},
			}},
		}},
	})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) || validation.Code != "invalid_utf8" {
		t.Fatalf("error = %v, want invalid_utf8 ValidationError", err)
	}
}
