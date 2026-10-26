// Package modeldef declares durable sessions independently of any account model
// or legacy operator schema. Namespace ownership belongs to the composing app.
package modeldef

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

const AppLabel = "godj_sessions"

func Schema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: AppLabel, Models: []schema.Model{{
		Name: "session", GoName: "Session", Fields: []schema.Field{
			schema.CharField("namespace", "Namespace", 128, schema.DBIndex(true)),
			schema.CharField("digest", "Digest", 64, schema.Unique()),
			schema.CharField("payload", "Payload", 32768),
		},
	}}})
}

func AppSpec() (codegen.AppSpec, error) {
	definition, err := Schema()
	if err != nil {
		return codegen.AppSpec{}, err
	}
	return codegen.AppSpec{Alias: "sessiondata", External: true, Schema: definition,
		Package: codegen.PackageSpec{PackageName: "models", ImportPath: "github.com/progresshans/godj/sessions/database/models"}}, nil
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, errors.New("session schema: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	app, err := AppSpec()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	app.External = false
	app.Package.Directory = "models"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "github.com/progresshans/godj/sessions/database/project", Directory: "project"},
		Apps:    []codegen.AppSpec{app},
	}, nil
}
