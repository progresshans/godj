// Package modeldef declares account ownership history independently of any
// account or permission model. Installing it never installs a default User.
package modeldef

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

const AppLabel = "godj_account_domain"

func Schema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: AppLabel, Models: []schema.Model{{
		Name: "transition", GoName: "Transition", Fields: []schema.Field{
			schema.CharField("operation_id", "OperationID", 64, schema.Unique()),
			schema.IntegerField("sequence", "Sequence", schema.Unique()),
			schema.ForeignKey("previous", "PreviousID", schema.Target(AppLabel, "transition"), schema.RelatedName("successors"), schema.Protect, schema.Nullable(), schema.Blank()),
			schema.CharField("role_fingerprint", "RoleFingerprint", 64),
			schema.CharField("generation", "Generation", 64, schema.Unique()),
			schema.CharField("evidence_sha256", "EvidenceSHA256", 64),
			schema.IntegerField("mapping_count", "MappingCount", schema.Default(int64(0))),
			schema.CharField("mapping_sha256", "MappingSHA256", 64, schema.Default(""), schema.Blank()),
			schema.DateTimeField("transitioned_at", "TransitionedAt"),
		},
	}, {
		Name: "mapping", GoName: "Mapping", Fields: []schema.Field{
			schema.OneToOne("transition", "TransitionID", schema.Target(AppLabel, "transition"), schema.RelatedName("mapping"), schema.Protect),
			schema.TextField("payload", "Payload"),
		},
	}}})
}

func AppSpec() (codegen.AppSpec, error) {
	definition, err := Schema()
	if err != nil {
		return codegen.AppSpec{}, err
	}
	return codegen.AppSpec{
		Alias: "accountdomain", External: true, Schema: definition,
		Package: codegen.PackageSpec{PackageName: "models", ImportPath: "github.com/progresshans/godj/account/domain/models"},
	}, nil
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, errors.New("account domain schema: nil context")
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
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "github.com/progresshans/godj/account/domain/project", Directory: "project"},
		Apps:    []codegen.AppSpec{app},
	}, nil
}
