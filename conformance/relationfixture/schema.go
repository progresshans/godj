// Package relationfixture owns the shared Author/Post declaration used by the
// relation products. Observers import its generated app and project packages;
// this declaration package never imports generated code.
package relationfixture

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/internal/relationschema"
)

// ProjectSpec returns the declaration-only whole-project generation input for
// the shared relation product. It never imports checked-in generated
// app or project packages.
func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, fmt.Errorf("relation fixture project spec: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	authors, err := relationschema.AuthorsSchema()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	blog, err := relationschema.BlogSchema()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const rootImport = "github.com/progresshans/godj/conformance/relationfixture/"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{
			PackageName: "project",
			ImportPath:  rootImport + "project",
			Directory:   "project",
		},
		Apps: []codegen.AppSpec{
			{
				Alias: "authors",
				Package: codegen.PackageSpec{
					PackageName: "authors",
					ImportPath:  rootImport + "authors",
					Directory:   "authors",
				},
				Schema: authors,
			},
			{
				Alias: "blog",
				Package: codegen.PackageSpec{
					PackageName: "blog",
					ImportPath:  rootImport + "blog",
					Directory:   "blog",
				},
				Schema: blog,
			},
		},
	}, nil
}
