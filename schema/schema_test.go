package schema_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestBuildUsesOneCurrentFormatForScalarAndForeignKey(t *testing.T) {
	t.Parallel()

	scalar, err := schema.Build(schema.Definition{
		AppLabel: "authors",
		Models: []schema.Model{{
			Name:   "author",
			GoName: "Author",
			Fields: []schema.Field{schema.CharField("name", "Name", 200)},
		}},
	})
	if err != nil {
		t.Fatalf("Build(scalar) error = %v", err)
	}
	if scalar.FormatVersion != ir.CurrentFormatVersion {
		t.Fatalf("scalar format version = %d, want %d", scalar.FormatVersion, ir.CurrentFormatVersion)
	}

	definition := schema.Definition{
		AppLabel: "blog",
		Models: []schema.Model{{
			Name:   "post",
			GoName: "Post",
			Fields: []schema.Field{
				schema.ForeignKey(
					"author", "AuthorID",
					schema.Target("authors", "author"),
					schema.RelatedName("posts"),
					schema.Protect,
				),
				schema.ForeignKey(
					"reviewer", "ReviewerID",
					schema.ModelTarget{AppLabel: "authors", ModelName: "author"},
					schema.RelatedName("reviewed_posts"),
					schema.SetNull,
					schema.Nullable(),
					schema.Column("reviewer_key"),
				),
			},
		}},
	}
	built, err := schema.Build(definition)
	if err != nil {
		t.Fatalf("Build(relation) error = %v", err)
	}
	if built.FormatVersion != ir.CurrentFormatVersion {
		t.Fatalf("relation format version = %d, want %d", built.FormatVersion, ir.CurrentFormatVersion)
	}
	fields := built.Models[0].Fields
	if got := fields[1]; got.Kind != ir.FieldForeignKey || got.Column != "author_id" || got.Nullable ||
		got.Relation == nil || got.Relation.Target != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
		got.Relation.Cardinality != ir.RelationManyToOne || got.Relation.Reverse.Name != "posts" ||
		got.Relation.OnDelete != ir.DeleteProtect {
		t.Fatalf("required ForeignKey = %#v", got)
	}
	if got := fields[2]; got.Column != "reviewer_key" || !got.Nullable || got.Relation == nil ||
		got.Relation.OnDelete != ir.DeleteSetNull {
		t.Fatalf("nullable ForeignKey = %#v", got)
	}

	definition.Models[0].Fields[0].Relation.Target.AppLabel = "mutated"
	definition.Models[0].Fields[0].Relation.Reverse.Name = "mutated"
	if fields[1].Relation.Target.AppLabel != "authors" || fields[1].Relation.Reverse.Name != "posts" {
		t.Fatalf("Build retained declaration relation alias: %#v", fields[1].Relation)
	}
}

func TestNoReverseAndExactDeleteAliases(t *testing.T) {
	t.Parallel()

	if schema.Protect != ir.DeleteProtect || schema.SetNull != ir.DeleteSetNull {
		t.Fatalf("delete aliases = %q/%q", schema.Protect, schema.SetNull)
	}
	if got := schema.NoReverse(); !got.Disabled || got.Name != "" {
		t.Fatalf("NoReverse() = %#v", got)
	}
	if got := schema.RelatedName("posts"); got.Disabled || got.Name != "posts" {
		t.Fatalf("RelatedName() = %#v", got)
	}
}

func TestBuildRejectsInvalidForeignKeyDeclaration(t *testing.T) {
	t.Parallel()

	_, err := schema.Build(schema.Definition{
		AppLabel: "blog",
		Models: []schema.Model{{
			Name:   "post",
			GoName: "Post",
			Fields: []schema.Field{schema.ForeignKey(
				"reviewer", "ReviewerID",
				schema.Target("authors", "author"),
				schema.RelatedName("reviewed_posts"),
				schema.SetNull,
			)},
		}},
	})
	var validation *ir.ValidationError
	if !errors.As(err, &validation) || validation.Path != "models[0].fields[1].relation.on_delete" ||
		validation.Code != "invalid_nullability" {
		t.Fatalf("Build() error = %#v, want set-null nullability ValidationError", err)
	}
}
