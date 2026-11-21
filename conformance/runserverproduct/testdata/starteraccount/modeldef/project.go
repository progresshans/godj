package modeldef

import (
	"context"

	"example.com/started-site/modeldef/testschema"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	role, schemas, err := testschema.CustomAccount()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	// Staff is a constant in this host. A privileged first account requires true.
	*role.Staff.Constant = true
	for _, app := range Applications() {
		declaration, err := schema.Build(app)
		if err != nil {
			return codegen.ProjectSpec{}, err
		}
		schemas = append(schemas, declaration)
	}
	spec := codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/started-site/project", Directory: "project"},
		Account: &role,
	}
	for _, declaration := range schemas {
		directory := "apps/" + declaration.AppLabel + "/models"
		spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: declaration.AppLabel, Schema: declaration,
			Package: codegen.PackageSpec{PackageName: "models", ImportPath: "example.com/started-site/" + directory, Directory: directory}})
	}
	return spec, nil
}
