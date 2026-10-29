package codegen_test

import (
	_ "embed"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/catalog_through_test.go.tmpl
var accountCatalogThroughConsumer string

func TestGeneratedAccountCatalogThroughCreation(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			for _, kind := range []string{"group", "permission"} {
				t.Run(kind, func(t *testing.T) {
					root := writeAccountCatalogThroughConsumer(t, custom, kind)
					required := []string{"TestCatalogThroughCreation"}
					for _, mode := range []string{"create", "no_op", "clear", "replace", "reuse_deleted_identity", "cascade_account", "clear_rollback", "denied_add", "denied_related", "limit", "duplicate", "target_key", "endpoint", "revision", "nested", "invalid_code", "snapshot_cleanup", "late_validator", "late_payload", "late_owned_link", "rollback", "unknown", "zero_target", "negative_target", "configuration", "restart"} {
						required = append(required, "TestCatalogThroughCreation/"+mode)
					}
					runAccountConsumerDatabases(t, root, required)
				})
			}
		})
	}
}

func writeAccountCatalogThroughConsumer(t *testing.T, custom bool, kind string) string {
	root, _ := writeAccountCatalogThroughConsumerWithSpec(t, custom, kind)
	return root
}

func writeAccountCatalogThroughConsumerWithSpec(t *testing.T, custom bool, kind string) (string, codegen.ProjectSpec) {
	t.Helper()
	spec := accountBindingConsumerSpec(t, custom)
	if custom {
		*spec.Account.Staff.Constant = true
	}
	through := spec.Account.Catalog.Group
	factory, otherFactory := "NewGroupThroughCreation", "NewPermissionThroughCreation"
	if kind == "permission" {
		through, factory = spec.Account.Catalog.Permission, "NewPermissionThroughCreation"
		otherFactory = "NewGroupThroughCreation"
	}
	for i := range spec.Apps {
		for j := range spec.Apps[i].Schema.Models {
			model := &spec.Apps[i].Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: spec.Apps[i].Schema.AppLabel, ModelName: model.Name}
			if identity == spec.Account.Model {
				model.ManyToMany = append(model.ManyToMany, ir.ManyToManyField{Name: "catalog_links", GoName: "CatalogLinks", Target: spec.Account.Catalog.Permission, Blank: true,
					Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: through, SourceField: "link_owner", TargetField: "link_target"}})
				model.Fields = append(model.Fields, ir.Field{Name: "anchor", GoName: "AnchorID", Column: "anchor_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true,
					Relation: &ir.ForeignKeyRelation{Target: through, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}})
			}
			if identity == through {
				for _, field := range []struct {
					name, goName string
					target       ir.ModelIdentity
				}{{"link_owner", "LinkOwnerID", spec.Account.Model}, {"link_target", "LinkTargetID", spec.Account.Catalog.Permission}} {
					model.Fields = append(model.Fields, ir.Field{Name: field.name, GoName: field.goName, Column: field.name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true,
						Relation: &ir.ForeignKeyRelation{Target: field.target, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}})
				}
				model.ManyToMany = append(model.ManyToMany, ir.ManyToManyField{Name: "extras", GoName: "Extras", Target: spec.Account.Catalog.Permission, Blank: true,
					Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: schema.Target("links", "extra"), SourceField: "owner", TargetField: "target"}})
			}
		}
	}
	links, err := schema.Build(schema.Definition{AppLabel: "links", Models: []schema.Model{{Name: "extra", GoName: "Extra", Fields: []schema.Field{
		schema.ForeignKey("owner", "OwnerID", through, schema.NoReverse(), schema.Cascade),
		schema.ForeignKey("target", "TargetID", spec.Account.Catalog.Permission, schema.NoReverse(), schema.Cascade, schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "links", Package: codegen.PackageSpec{PackageName: "links", ImportPath: "example.com/godj-project-bundle/links", Directory: "links"}, Schema: links})
	var imports []string
	types := make(map[ir.ModelIdentity]string)
	for i, app := range spec.Apps {
		alias, used := fmt.Sprintf("model%d", i), false
		for _, model := range app.Schema.Models {
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			types[identity] = alias + "." + model.GoName
			used = used || identity == spec.Account.Model || identity == spec.Account.Catalog.Group || identity == spec.Account.Catalog.Permission || identity == schema.Target("links", "extra")
		}
		if used {
			imports = append(imports, fmt.Sprintf("%s %q", alias, app.Package.ImportPath))
		}
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	profile, prefix := "first_name", "IdentityUser"
	if custom {
		profile, prefix = "region", "PeopleMember"
	}
	source := strings.NewReplacer("@@IMPORTS@@", strings.Join(imports, "\n"), "@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec),
		"@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission],
		"@@THROUGH@@", types[through], "@@EXTRA@@", types[schema.Target("links", "extra")], "@@FACTORY@@", factory, "@@OTHERFACTORY@@", otherFactory, "@@PROFILE@@", profile, "@@PREFIX@@", prefix, "@@KIND@@", kind).Replace(accountCatalogThroughConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/catalog_through_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root, spec
}
