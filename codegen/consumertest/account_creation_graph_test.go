package codegen_test

import (
	_ "embed"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/creation_graph_test.go.tmpl
var accountCreationGraphConsumer string

//go:embed testdata/accountbinding/through_collection_test.go.tmpl
var accountThroughCollectionConsumer string

func TestGeneratedAccountThroughIdentityCreation(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			root := writeAccountCreationGraphConsumer(t, custom)
			required := []string{"TestGeneratedAccountThroughIdentity"}
			for _, child := range []string{"first_account", "created", "duplicate_pair", "denied", "missing_policy", "validator", "unknown", "zero_key", "negative_key", "edit_rejected", "restart"} {
				required = append(required, required[0]+"/"+child)
			}
			required = append(required, "TestGeneratedAccountThroughCollection")
			for _, child := range []string{"create", "unusable", "no_op", "replace", "reuse_deleted_login", "delete_sessions", "denied_add", "denied_effect", "denied_creation_effect", "limit", "endpoint_override", "nested", "snapshot_cleanup", "hash_failure", "revoked_during_hash", "changed_during_hash", "late_policy", "late_payload", "late_key", "rollback", "unknown", "zero_target", "negative_target", "configuration", "restart"} {
				required = append(required, "TestGeneratedAccountThroughCollection/"+child)
			}
			runAccountConsumerDatabases(t, root, required)
		})
	}
}

func writeAccountCreationGraphConsumer(t *testing.T, custom bool) string {
	t.Helper()
	spec := accountBindingConsumerSpec(t, custom)
	if custom {
		*spec.Account.Staff.Constant = true
	}
	var imports []string
	types := make(map[ir.ModelIdentity]string)
	for i := range spec.Apps {
		app := &spec.Apps[i]
		alias := fmt.Sprintf("model%d", i)
		used := false
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			types[identity] = alias + "." + model.GoName
			used = used || identity == spec.Account.Model || identity == spec.Account.Catalog.Group || identity == spec.Account.Catalog.Permission
			if identity == spec.Account.Model {
				for _, field := range []struct {
					name, goName string
					target       ir.ModelIdentity
				}{
					{"catalog_owner", "CatalogOwnerID", spec.Account.Catalog.Group}, {"catalog_permission", "CatalogPermissionID", spec.Account.Catalog.Permission},
					{"link_parent", "LinkParentID", spec.Account.Model},
				} {
					model.Fields = append(model.Fields, ir.Field{Name: field.name, GoName: field.goName, Column: field.name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true,
						Relation: &ir.ForeignKeyRelation{Target: field.target, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}})
				}
				model.ManyToMany = append(model.ManyToMany, ir.ManyToManyField{Name: "delegates", GoName: "Delegates", Target: spec.Account.Catalog.Permission, Blank: true,
					Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: spec.Account.Model, SourceField: "link_parent", TargetField: "catalog_permission"}})
			}
			if identity == spec.Account.Catalog.Group {
				for k := range model.ManyToMany {
					if model.ManyToMany[k].Name == spec.Account.Catalog.GroupPermissions {
						model.ManyToMany[k].Through = &ir.ThroughModel{Model: spec.Account.Model, SourceField: "catalog_owner", TargetField: "catalog_permission"}
					}
				}
			}
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
	profile := "first_name"
	if custom {
		profile = "region"
	}
	prefix, groups, permissions := "IdentityUser", "Groups", "Permissions"
	if custom {
		prefix, groups, permissions = "PeopleMember", "Teams", "Grants"
	}
	replace := strings.NewReplacer("@@IMPORTS@@", strings.Join(imports, "\n"),
		"@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec),
		"@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission], "@@PROFILE@@", profile,
		"@@PREFIX@@", prefix, "@@GROUPS@@", groups, "@@PERMISSIONS@@", permissions)
	source := replace.Replace(accountCreationGraphConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/creation_graph_test.go", formatted)
	through, err := format.Source([]byte(replace.Replace(accountThroughCollectionConsumer)))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/through_collection_test.go", through)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}
