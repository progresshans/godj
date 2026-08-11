// Package fixture owns the REL-007/008 declaration schemas used to regenerate
// the checked-in relation-delete product. Runtime observation imports only the
// generated app and project packages.
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
					schema.CharField("name", "Name", 200),
				},
			},
		},
	})
	if err != nil {
		return ir.Schema{}, fmt.Errorf("build authors relation-delete schema: %w", err)
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
					schema.CharField("title", "Title", 200),
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
		return ir.Schema{}, fmt.Errorf("build blog relation-delete schema: %w", err)
	}
	return result, nil
}
