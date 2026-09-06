// Package modeldef declares a small two-model consumer independent of Article.
package modeldef

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func Schema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: "helpdesk", Models: []schema.Model{
		{Name: "category", GoName: "Category", Fields: []schema.Field{schema.CharField("name", "Name", 60)}},
		{Name: "ticket", GoName: "Ticket", Fields: []schema.Field{
			schema.CharField("subject", "Subject", 120),
			schema.CharField("details", "Details", 400, schema.Nullable()),
			schema.BooleanField("closed", "Closed", schema.Default(false)),
			schema.ForeignKey("category", "CategoryID", schema.Target("helpdesk", "category"), schema.RelatedName("tickets"), schema.Protect),
		}},
	}})
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, fmt.Errorf("helpdesk schema: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	definition, err := Schema()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const root = "github.com/progresshans/godj/examples/helpdesk/"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: root + "project", Directory: "project"},
		Apps:    []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: root + "models", Directory: "models"}, Schema: definition}},
	}, nil
}
