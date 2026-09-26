package modeldef

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/codegen"
	identitydef "github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func Schema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: "identityfixture", Models: []schema.Model{
		{Name: "note", GoName: "Note", Fields: []schema.Field{
			schema.CharField("text", "Text", 100),
			schema.ForeignKey("owner", "OwnerID", schema.Target(identitydef.AppLabel, "user"), schema.RelatedName("notes"), schema.Cascade),
		}},
		{Name: "guard", GoName: "Guard", Fields: []schema.Field{
			schema.ForeignKey("owner", "OwnerID", schema.Target(identitydef.AppLabel, "user"), schema.RelatedName("guards"), schema.Protect),
		}},
	}})
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, fmt.Errorf("identity fixture: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	imported, err := identitydef.AppSpec()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	imported.Alias = "accounts"
	local, err := Schema()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const root = "github.com/progresshans/godj/conformance/identityfixture/"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: root + "project", Directory: "project"},
		Apps: []codegen.AppSpec{imported, {
			Alias: "work", Package: codegen.PackageSpec{PackageName: "models", ImportPath: root + "models", Directory: "models"}, Schema: local,
		}},
	}, nil
}
