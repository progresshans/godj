// Package fixture owns the REL-001 declaration schemas used to verify the
// checked-in generated relation product. Product observation does not import
// this package; it imports only the generated project bridge.
package fixture

import (
	"fmt"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func AuthorsSchema() (ir.Schema, error) {
	result, err := schema.Build(schema.Definition{
		AppLabel: "authors",
		Models: []schema.Model{
			{
				Name:    "author",
				GoName:  "Author",
				DBTable: "authors_author",
				Fields: []schema.Field{
					schema.AutoField("id", "ID"),
				},
			},
		},
	})
	if err != nil {
		return ir.Schema{}, fmt.Errorf("build authors relation-product schema: %w", err)
	}
	return result, nil
}

func BlogSchema() (ir.Schema, error) {
	result, err := schema.Build(schema.Definition{
		AppLabel: "blog",
		Models: []schema.Model{
			{
				Name:    "post",
				GoName:  "Post",
				DBTable: "blog_post",
				Fields: []schema.Field{
					schema.AutoField("id", "ID"),
					schema.ForeignKey(
						"author",
						"AuthorID",
						schema.Target("authors", "author"),
						schema.RelatedName("posts"),
						schema.Protect,
					),
					schema.ForeignKey(
						"reviewer",
						"ReviewerID",
						schema.Target("authors", "author"),
						schema.RelatedName("reviewed_posts"),
						schema.SetNull,
						schema.Nullable(),
					),
				},
			},
		},
	})
	if err != nil {
		return ir.Schema{}, fmt.Errorf("build blog relation-product schema: %w", err)
	}
	return result, nil
}
