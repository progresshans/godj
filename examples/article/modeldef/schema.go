// Package modeldef contains the declaration-side schema. It deliberately does
// not import examples/article/models, preserving the codegen bootstrap graph.
package modeldef

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/codegen"
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

// ProjectSpec returns the declaration-only whole-project generation input.
// This package deliberately does not import the generated models or project
// packages, so generation can bootstrap missing or stale output.
func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, fmt.Errorf("article project spec: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	appSchema, err := Schema()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const rootImport = "github.com/progresshans/godj/examples/article/"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{
			PackageName: "project",
			ImportPath:  rootImport + "project",
			Directory:   "project",
		},
		Apps: []codegen.AppSpec{{
			Alias: "models",
			Package: codegen.PackageSpec{
				PackageName: "models",
				ImportPath:  rootImport + "models",
				Directory:   "models",
			},
			Schema: appSchema,
		}},
	}, nil
}
