package codegen_test

import (
	_ "embed"
	"encoding/json"
	"go/format"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/transition_catalog_test.go.tmpl
var accountCatalogTransitionConsumer string

//go:embed testdata/accountbinding/transition_catalog_references_test.go.tmpl
var accountCatalogReferencesConsumer string

func TestGeneratedAccountCatalogTransition(t *testing.T) {
	root := writeAccountCatalogTransitionConsumer(t)
	required := []string{"TestAccountCatalogTransition"}
	for _, strategy := range []string{"staged", "created"} {
		required = append(required, "TestAccountCatalogTransition/"+strategy)
		for _, name := range accountCatalogTransitionCases(strategy) {
			required = append(required, "TestAccountCatalogTransition/"+strategy+"/"+name)
		}
	}
	required = append(required, "TestAccountCatalogReferences")
	for _, strategy := range []string{"staged", "created"} {
		required = append(required, "TestAccountCatalogReferences/"+strategy)
		for _, name := range accountCatalogReferenceCases(strategy) {
			required = append(required, "TestAccountCatalogReferences/"+strategy+"/"+name)
		}
	}
	runAccountConsumerDatabases(t, root, required)
}

func accountCatalogReferenceCases(strategy string) []string {
	result := []string{"lifecycle", "schema_lifecycle", "shared_group", "shared_permission", "shared_catalog", "signed_keys", "null_references", "no_revision", "page_boundary", "row_limit", "revision_limit", "owned_callbacks", "configuration", "missing_grant", "missing_additional_grant", "permission_revoked", "policy_rejected", "row_changed", "row_added", "row_deleted", "second_write_failure", "host_audit_failure", "mapping_failure", "receipt_failure", "late_host_change", "late_policy_change", "host_catalog_change", "lost_update", "unknown_commit", "unknown_rollback", "one_to_one_conflict", "read_cleanup", "canceled", "wrong_catalog_target", "catalog_target_without_source", "concurrent"}
	if strategy == "staged" {
		result = append(result, "already_mapped")
	} else {
		result = append(result, "target_insert_host_change")
	}
	return result
}

func accountCatalogTransitionCases(strategy string) []string {
	result := []string{
		"lifecycle", "round_trip", "signed_keys", "shared_group", "shared_permission", "shared_group_nonidentity", "page_boundary", "explicit_empty", "missing_mapping", "duplicate_mapping", "incomplete_mapping", "mapping_revision", "catalog_limit", "catalog_inventory_limit", "wrong_code", "invalid_unused_code", "invalid_unused_revision", "wrong_unused_group_grants", "same_effective_wrong_direct", "catalog_changed", "source_catalog_changed", "catalog_revision_changed", "catalog_link_changed", "new_catalog_row", "deleted_catalog_row", "permission_revoked", "policy_changed", "owned_mapping", "read_cleanup", "canceled", "rollback", "audit_failure", "mapping_failure", "receipt_failure", "late_catalog_change", "late_link_change", "retirement_catalog_change", "unknown_commit", "unknown_rollback", "concurrent", "reconcile_after_edit", "stored_mapping_rewrite", "host_references", "configuration",
	}
	if strategy == "created" {
		result = append(result, "target_insert_catalog_change")
	}
	return result
}

func accountCatalogTransitionSpec(t *testing.T) (codegen.ProjectSpec, ir.AccountRole) {
	t.Helper()
	spec, role := accountTransitionConsumerSpec(t)
	oldCatalog := role.Catalog
	role.Catalog = ir.AccountCatalog{
		Group: ir.ModelIdentity{AppLabel: "catalog", ModelName: "team"}, GroupName: "title", GroupRevision: "version", GroupPermissions: "grants",
		Permission: ir.ModelIdentity{AppLabel: "catalog", ModelName: "grant"}, PermissionCode: "symbol", PermissionName: "label", PermissionRevision: "version",
	}
	var catalog []ir.Model
	for i := range spec.Apps[0].Schema.Models {
		original := &spec.Apps[0].Schema.Models[i]
		if original.Name != oldCatalog.Group.ModelName && original.Name != oldCatalog.Permission.ModelName {
			continue
		}
		model := original.Clone()
		isGroup := original.Name == oldCatalog.Group.ModelName
		model.Name, model.GoName, model.DBTable = "grant", "Grant", "catalog_grant"
		renames := map[string][2]string{"code": {"symbol", "Symbol"}, "name": {"label", "Label"}, "revision": {"version", "Version"}}
		if isGroup {
			model.Name, model.GoName, model.DBTable = "team", "Team", "catalog_team"
			renames = map[string][2]string{"name": {"title", "Title"}, "revision": {"version", "Version"}}
		}
		for j := range model.Fields {
			field := &model.Fields[j]
			if names, found := renames[field.Name]; found {
				field.Name, field.GoName, field.Column = names[0], names[1], names[0]
			}
		}
		if isGroup {
			model.Fields = append(model.Fields, ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Blank: true, Default: &ir.Scalar{Kind: ir.ScalarString, String: "reviewed"}})
			model.ManyToMany = []ir.ManyToManyField{
				{Name: "grants", GoName: "Grants", Target: role.Catalog.Permission, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}},
				{Name: "legacy_permissions", GoName: "LegacyPermissions", Target: oldCatalog.Permission, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}},
			}
			original.ManyToMany = append(original.ManyToMany, ir.ManyToManyField{Name: "new_permissions", GoName: "NewPermissions", Target: role.Catalog.Permission, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}})
		} else {
			model.Fields = append(model.Fields, ir.Field{Name: "weight", GoName: "Weight", Column: "weight", Kind: ir.FieldInteger, Default: &ir.Scalar{Kind: ir.ScalarInteger, Integer: 5}})
		}
		catalog = append(catalog, model)
	}
	for i := range spec.Apps {
		if spec.Apps[i].Schema.AppLabel != "members" {
			continue
		}
		member := &spec.Apps[i].Schema.Models[0]
		for j := range member.ManyToMany {
			field := &member.ManyToMany[j]
			if field.Name == role.Groups {
				field.Target = role.Catalog.Group
			} else if field.Name == role.Permissions {
				field.Target = role.Catalog.Permission
			}
		}
		member.ManyToMany = append(member.ManyToMany,
			ir.ManyToManyField{Name: "legacy_groups", GoName: "LegacyGroups", Target: oldCatalog.Group, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}},
			ir.ManyToManyField{Name: "legacy_permissions", GoName: "LegacyPermissions", Target: oldCatalog.Permission, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}},
		)
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "catalog", Package: codegen.PackageSpec{PackageName: "catalog", ImportPath: "example.com/godj-project-bundle/catalog", Directory: "catalog"}, Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "catalog", Models: catalog}})
	addAccountCatalogHostReferences(&spec, oldCatalog, role.Catalog)
	return spec, role
}

func addAccountCatalogHostReferences(spec *codegen.ProjectSpec, old, next ir.AccountCatalog) {
	ref := func(name, goName string, target ir.ModelIdentity, one bool) ir.Field {
		cardinality := ir.RelationManyToOne
		if one {
			cardinality = ir.RelationOneToOne
		}
		return ir.Field{Name: name, GoName: goName, Column: name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Unique: one, Relation: &ir.ForeignKeyRelation{Target: target, Cardinality: cardinality, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	for i := range spec.Apps {
		app := &spec.Apps[i]
		if app.Schema.AppLabel != "host" {
			continue
		}
		badge := ir.Model{Name: "catalog_badge", GoName: "CatalogBadge", DBTable: "transition_catalog_badge", Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 64},
		}}
		app.Schema.Models = append(app.Schema.Models, badge)
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			if slices.Contains([]string{"ticket", "assignment", "catalog_badge"}, model.Name) {
				one := model.Name == "catalog_badge"
				model.Fields = append(model.Fields,
					ref("old_group", "OldGroupID", old.Group, false), ref("group", "GroupID", next.Group, one), ref("legacy_group", "LegacyGroupID", old.Group, one),
					ref("old_permission", "OldPermissionID", old.Permission, false), ref("permission", "PermissionID", next.Permission, one), ref("legacy_permission", "LegacyPermissionID", old.Permission, one))
			}
			if model.Name == "workspace" {
				for _, item := range []struct {
					name, goName, field string
					target              ir.ModelIdentity
				}{
					{"old_groups", "OldGroups", "old_group", old.Group}, {"groups", "Groups", "group", next.Group}, {"shared_groups", "SharedGroups", "legacy_group", old.Group},
					{"old_permissions", "OldPermissions", "old_permission", old.Permission}, {"permissions", "Permissions", "permission", next.Permission}, {"shared_permissions", "SharedPermissions", "legacy_permission", old.Permission},
				} {
					model.ManyToMany = append(model.ManyToMany, ir.ManyToManyField{Name: item.name, GoName: item.goName, Target: item.target, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "host", ModelName: "assignment"}, SourceField: "workspace", TargetField: item.field}})
				}
			}
		}
	}
}

func accountCatalogReferenceSchemaVariant(spec codegen.ProjectSpec, phase string) codegen.ProjectSpec {
	result := accountTransitionSchemaVariant(spec, phase)
	for i := range result.Apps {
		app := &result.Apps[i]
		if app.Schema.AppLabel != "host" {
			continue
		}
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			model.Fields = slices.DeleteFunc(model.Fields, func(field ir.Field) bool {
				return field.Name == "legacy_group" || field.Name == "legacy_permission" || phase == "legacy" && field.Relation != nil && field.Relation.Target.AppLabel == "catalog"
			})
			model.ManyToMany = slices.DeleteFunc(model.ManyToMany, func(field ir.ManyToManyField) bool {
				return strings.HasPrefix(field.Name, "shared_") || phase == "legacy" && field.Target.AppLabel == "catalog" || phase == "contracted" && strings.HasPrefix(field.Name, "old_")
			})
		}
	}
	return result
}

func writeAccountCatalogTransitionConsumer(t *testing.T) string {
	t.Helper()
	spec, role := accountCatalogTransitionSpec(t)
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	encoded, err := json.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	caseSource := func(strategy string) string {
		var names []string
		for _, name := range accountCatalogTransitionCases(strategy) {
			names = append(names, strconv.Quote(name))
		}
		return strings.Join(names, ",")
	}
	source := strings.NewReplacer("@@ROLE@@", string(encoded), "@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec), "@@STAGED_CASES@@", caseSource("staged"), "@@CREATED_CASES@@", caseSource("created")).Replace(accountCatalogTransitionConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_catalog_test.go", formatted)
	legacy, contracted := accountCatalogReferenceSchemaVariant(spec, "legacy"), accountCatalogReferenceSchemaVariant(spec, "contracted")
	contracted.Account = &role
	for _, variant := range []codegen.ProjectSpec{legacy, contracted} {
		generated, err := codegen.GenerateProject(variant)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range generated.Files() {
			writeGeneratedTestFile(t, root, file.Path, file.Source())
		}
	}
	stages := accountTransitionMigrationStages(t, legacy, spec, contracted)
	referenceCases := func(strategy string) string {
		var names []string
		for _, name := range accountCatalogReferenceCases(strategy) {
			names = append(names, strconv.Quote(name))
		}
		return strings.Join(names, ",")
	}
	source = strings.NewReplacer("@@LEGACY_MIGRATIONS@@", stages[0], "@@EXPANDED_MIGRATIONS@@", stages[1], "@@CONTRACTED_MIGRATIONS@@", stages[2], "@@STAGED_CASES@@", referenceCases("staged"), "@@CREATED_CASES@@", referenceCases("created")).Replace(accountCatalogReferencesConsumer)
	formatted, err = format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_catalog_references_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}
