// Package accountdeletefixture declares a selected account and catalog whose
// host graph crosses every lifecycle owner. It imports no generated models.
package accountdeletefixture

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, errors.New("account deletion fixture: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	role, schemas, err := testschema.CustomAccount()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	fk := func(name, goName string, target ir.ModelIdentity, policy ir.DeletePolicy) ir.Field {
		return ir.Field{Name: name, GoName: goName, Column: name + "_id", Kind: ir.FieldForeignKey, Nullable: true,
			Relation: &ir.ForeignKeyRelation{Target: target, OnDelete: policy, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	for i := range schemas {
		for j := range schemas[i].Models {
			model := &schemas[i].Models[j]
			switch (ir.ModelIdentity{AppLabel: schemas[i].AppLabel, ModelName: model.Name}) {
			case role.Model:
				model.Fields = append(model.Fields, fk("leader", "LeaderID", role.Model, ir.DeleteCascade), fk("reviewer", "ReviewerID", role.Model, ir.DeleteSetNull),
					fk("sponsor", "SponsorID", role.Catalog.Group, ir.DeleteCascade), fk("grant_ref", "GrantRefID", role.Catalog.Permission, ir.DeleteSetNull))
				for k := range model.ManyToMany {
					if model.ManyToMany[k].Name == role.Groups {
						model.ManyToMany[k].Through = &ir.ThroughModel{Model: schema.Target("links", "membership"), SourceField: "member", TargetField: "team"}
					}
				}
			case role.Catalog.Group:
				model.Fields = append(model.Fields, fk("creator", "CreatorID", role.Model, ir.DeleteCascade), fk("reviewer", "ReviewerID", role.Model, ir.DeleteSetNull))
				for k := range model.ManyToMany {
					if model.ManyToMany[k].Name == role.Catalog.GroupPermissions {
						model.ManyToMany[k].Through = &ir.ThroughModel{Model: schema.Target("links", "grant_link"), SourceField: "team", TargetField: "grant"}
					}
				}
			case role.Catalog.Permission:
				model.Fields = append(model.Fields, fk("issuer", "IssuerID", role.Model, ir.DeleteCascade), fk("team", "TeamID", role.Catalog.Group, ir.DeleteCascade),
					fk("reviewer", "ReviewerID", role.Model, ir.DeleteSetNull))
			}
		}
	}
	links, err := schema.Build(schema.Definition{AppLabel: "links", Models: []schema.Model{
		{Name: "membership", GoName: "Membership", Fields: []schema.Field{
			schema.ForeignKey("member", "MemberID", role.Model, schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("team", "TeamID", role.Catalog.Group, schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("anchor", "AnchorID", role.Model, schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}},
		{Name: "grant_link", GoName: "GrantLink", Fields: []schema.Field{
			schema.ForeignKey("team", "TeamID", role.Catalog.Group, schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("grant", "GrantID", role.Catalog.Permission, schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("anchor", "AnchorID", role.Model, schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}},
	}})
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	schemas = append(schemas, links)
	const root = "github.com/progresshans/godj/conformance/accountdeletefixture/"
	spec := codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: root + "project", Directory: "project"}, Account: &role}
	for _, app := range schemas {
		spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: app.AppLabel,
			Package: codegen.PackageSpec{PackageName: app.AppLabel, ImportPath: root + app.AppLabel, Directory: app.AppLabel}, Schema: app})
	}
	return spec, nil
}
