// Package modeldef contains the declaration-side schema. It deliberately does
// not import examples/article/models, preserving the codegen bootstrap graph.
package modeldef

import (
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

var Definition = schema.Definition{
	AppLabel: "godj_conformance",
	Models: []schema.Model{{
		Name:   "article",
		GoName: "Article",
		Fields: []schema.Field{
			schema.CharField("title", "Title", 200),
			schema.BooleanField("published", "Published", schema.Default(false)),
			schema.CharField("summary", "Summary", 200, schema.Nullable()),
		},
	}},
}

func Schema() (ir.Schema, error) {
	return schema.Build(Definition)
}
