// Package modeldef owns the collection browser's schema without importing its
// generated inventory. Helpdesk is an independently generated external app.
package modeldef

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
	helpdesk "github.com/progresshans/godj/examples/helpdesk/modeldef"
	"github.com/progresshans/godj/schema"
)

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, errors.New("collection browser schema: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	targets, err := helpdesk.Schema()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	inventory, err := schema.Build(schema.Definition{AppLabel: "browser_inventory", Models: []schema.Model{
		{Name: "asset", GoName: "Asset", Fields: []schema.Field{
			schema.CharField("code", "Code", 80, schema.Unique()),
			schema.BooleanField("enabled", "Enabled", schema.Default(true)),
			schema.FileField("attachment", "Attachment", schema.MaxLength(120), schema.Blank(), schema.Default("")),
			schema.TextField("note", "Note", schema.Nullable(), schema.Blank()),
			schema.IntegerField("revision", "Revision", schema.Default(int64(1))),
			schema.ForeignKey("category", "CategoryID", schema.Target("helpdesk", "category"), schema.RelatedName("browser_assets"), schema.Protect),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("labels", "Labels", schema.Target("helpdesk", "label"), schema.RelatedName("browser_assets"), schema.Through(schema.Target("browser_inventory", "asset_label"), "asset", "label"), schema.ManyToManyBlank()),
		}},
		{Name: "asset_label", GoName: "AssetLabel", Fields: []schema.Field{
			schema.ForeignKey("asset", "AssetID", schema.Target("browser_inventory", "asset"), schema.RelatedName("label_links"), schema.Cascade),
			schema.ForeignKey("label", "LabelID", schema.Target("helpdesk", "label"), schema.RelatedName("browser_asset_links"), schema.Cascade),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "asset_label", Fields: []string{"asset", "label"}}}},
		{Name: "audit", GoName: "Audit", Fields: []schema.Field{
			schema.CharField("action", "Action", 8), schema.IntegerField("asset_key", "AssetKey"), schema.CharField("actor", "Actor", 32), schema.IntegerField("revision", "Revision"),
		}},
	}})
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	const root = "github.com/progresshans/godj/admin/testdata/browser/"
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "collectionproject", ImportPath: root + "collectionproject", Directory: "collectionproject"}, Apps: []codegen.AppSpec{
		{Alias: "helpdesk", Package: codegen.PackageSpec{PackageName: "models", ImportPath: "github.com/progresshans/godj/examples/helpdesk/models"}, Schema: targets, External: true},
		{Alias: "inventory", Package: codegen.PackageSpec{PackageName: "inventory", ImportPath: root + "inventory", Directory: "inventory"}, Schema: inventory},
	}}, nil
}
