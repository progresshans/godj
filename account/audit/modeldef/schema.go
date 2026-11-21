// Package modeldef declares account-domain audit independently of the selected
// account model. Historical events survive account, catalog and domain changes.
package modeldef

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

const AppLabel = "godj_account_audit"

func Schema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: AppLabel, Models: []schema.Model{{
		Name: "event", GoName: "Event", Fields: []schema.Field{
			schema.CharField("domain", "Domain", 64, schema.DBIndex(true)),
			schema.CharField("actor_id", "ActorID", 128),
			schema.CharField("model", "Model", 128),
			schema.IntegerField("object_id", "ObjectID"),
			schema.CharField("action", "Action", 16),
			schema.CharField("changed_fields", "ChangedFields", 32768),
			schema.DateTimeField("recorded_at", "RecordedAt"),
		},
	}}})
}

func AppSpec() (codegen.AppSpec, error) {
	definition, err := Schema()
	if err != nil {
		return codegen.AppSpec{}, err
	}
	return codegen.AppSpec{Alias: "accountaudit", External: true, Schema: definition,
		Package: codegen.PackageSpec{PackageName: "models", ImportPath: "github.com/progresshans/godj/account/audit/models"}}, nil
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, errors.New("account audit schema: nil context")
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
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "github.com/progresshans/godj/account/audit/project", Directory: "project"},
		Apps:    []codegen.AppSpec{app},
	}, nil
}
