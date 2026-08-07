package ir_test

import (
	"errors"
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
	if got.FormatVersion != 2 {
		t.Fatalf("FormatVersion = %d, want 2", got.FormatVersion)
	}
	publishedDefault := model.Fields[2].Default
	if publishedDefault == nil || publishedDefault.Kind != ir.ScalarBoolean || publishedDefault.Boolean {
		t.Fatalf("published default = %#v, want explicit boolean false", publishedDefault)
	}
}

func TestCanonicalHashIsStableAndInputIsNotMutated(t *testing.T) {
	t.Parallel()

	input := ir.Schema{
		AppLabel: "news",
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
	if input.FormatVersion != 0 || len(input.Models[0].Fields) != 1 || input.Models[0].DBTable != "" {
		t.Fatalf("Normalize mutated input: %#v", input)
	}
	if len(first) != 64 {
		t.Fatalf("hash length = %d, want 64", len(first))
	}
}

func TestNormalizeRejectsUnsupportedNullableBoolean(t *testing.T) {
	t.Parallel()

	_, err := schema.Build(schema.Definition{
		AppLabel: "news",
		Models: []schema.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []schema.Field{schema.BooleanField("published", "Published", schema.Nullable())},
		}},
	})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	if validation.Code != "unsupported" {
		t.Fatalf("code = %q, want unsupported", validation.Code)
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
		AppLabel: "news",
		Models: []ir.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []ir.Field{{
				Name:    "published",
				GoName:  "Published",
				Kind:    ir.FieldBoolean,
				Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "false"},
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

	input := ir.Schema{Models: []ir.Model{{Fields: []ir.Field{{Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean}}}}}}
	clone := input.Clone()
	clone.Models[0].Fields[0].Default.Boolean = true
	if input.Models[0].Fields[0].Default.Boolean {
		t.Fatal("Clone shared typed default pointer with its input")
	}
}

func TestNormalizeRejectsSchemaIRV1(t *testing.T) {
	t.Parallel()

	_, err := ir.Normalize(ir.Schema{FormatVersion: 1, AppLabel: "news"})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) || validation.Code != "unsupported_version" {
		t.Fatalf("error = %v, want unsupported_version ValidationError", err)
	}
}

func TestNormalizeRejectsInvalidUTF8StringDefault(t *testing.T) {
	t.Parallel()

	_, err := ir.Normalize(ir.Schema{
		AppLabel: "news",
		Models: []ir.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []ir.Field{{
				Name:      "title",
				GoName:    "Title",
				Kind:      ir.FieldChar,
				MaxLength: 20,
				Default:   &ir.ScalarDefault{Kind: ir.ScalarString, String: string([]byte{0xff})},
			}},
		}},
	})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) || validation.Code != "invalid_utf8" {
		t.Fatalf("error = %v, want invalid_utf8 ValidationError", err)
	}
}
