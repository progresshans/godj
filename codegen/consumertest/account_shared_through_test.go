package codegen_test

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/transition_shared_through_test.go.tmpl
var accountSharedThroughConsumer string

func accountSharedThroughCases(strategy string) []string {
	result := []string{"lifecycle", "signed_keys", "shared_group", "shared_permission", "late_payload", "late_catalog_payload", "late_endpoint", "late_extra_null", "late_remove", "audit_failure", "rollback", "unknown_commit", "unknown_rollback"}
	if strategy == "created" {
		result = append(result, "account_only")
	}
	return result
}

func TestGeneratedAccountSharedThroughTransition(t *testing.T) {
	spec, role := accountSharedThroughSpec(t)
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	encoded, err := json.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	cases := func(strategy string) string {
		var quoted []string
		for _, name := range accountSharedThroughCases(strategy) {
			quoted = append(quoted, strconv.Quote(name))
		}
		return strings.Join(quoted, ",")
	}
	source := strings.NewReplacer("@@ROLE@@", string(encoded), "@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec), "@@STAGED_CASES@@", cases("staged"), "@@CREATED_CASES@@", cases("created")).Replace(accountSharedThroughConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_shared_through_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	required := []string{"TestAccountSharedThroughTransition"}
	for _, storage := range []string{"plain", "unique"} {
		parent := "TestAccountSharedThroughTransition/" + storage
		required = append(required, parent)
		for _, strategy := range []string{"staged", "created"} {
			group := parent + "/" + strategy
			required = append(required, group)
			for _, name := range accountSharedThroughCases(strategy) {
				required = append(required, group+"/"+name)
			}
		}
	}
	runAccountConsumerDatabases(t, root, required)
}

// Both storage variants belong to one generated module. Each physical table
// has old/new account and catalog endpoints plus an independent profile target.
// This exercises nullable projections without recompiling duplicate app types.
func accountSharedThroughSpec(t *testing.T) (codegen.ProjectSpec, ir.AccountRole) {
	t.Helper()
	spec, role := accountCatalogTransitionSpec(t)
	spec.Apps = slices.DeleteFunc(spec.Apps, func(app codegen.AppSpec) bool { return app.Schema.AppLabel == "host" })
	old := spec.Account.Clone()
	bridge := ir.Model{Name: "bridge", GoName: "Bridge", DBTable: "shared_bridge", Fields: []ir.Field{
		{Name: "id", GoName: "ID", Column: "link_key", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldText, Default: &ir.Scalar{Kind: ir.ScalarString, String: "bridge-default"}},
	}}
	for _, field := range []struct {
		name, goName string
		target       ir.ModelIdentity
	}{
		{"old_user", "OldUserID", old.Model}, {"member", "MemberID", role.Model},
		{"old_group", "OldGroupID", old.Catalog.Group}, {"new_group", "NewGroupID", role.Catalog.Group},
		{"old_permission", "OldPermissionID", old.Catalog.Permission}, {"new_permission", "NewPermissionID", role.Catalog.Permission},
		{"profile_permission", "ProfilePermissionID", old.Catalog.Permission},
	} {
		bridge.Fields = append(bridge.Fields, ir.Field{Name: field.name, GoName: field.goName, Column: field.name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true,
			Relation: &ir.ForeignKeyRelation{Target: field.target, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}})
	}
	unique := bridge.Clone()
	unique.Name, unique.GoName, unique.DBTable = "unique_bridge", "UniqueBridge", "shared_unique_bridge"
	pairs := make(map[string]bool)
	for i := range spec.Apps {
		app := &spec.Apps[i]
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			var fields []ir.ManyToManyField
			for _, field := range model.ManyToMany {
				var source, target string
				switch identity {
				case old.Model:
					source = "old_user"
					target = map[string]string{old.Groups: "old_group", old.Permissions: "old_permission", "topics": "profile_permission"}[field.Name]
				case role.Model:
					source = "member"
					target = map[string]string{role.Groups: "new_group", role.Permissions: "new_permission", "legacy_groups": "old_group", "legacy_permissions": "old_permission", "interests": "profile_permission"}[field.Name]
				case old.Catalog.Group:
					source = "old_group"
					target = map[string]string{old.Catalog.GroupPermissions: "old_permission", "new_permissions": "new_permission"}[field.Name]
				case role.Catalog.Group:
					source = "new_group"
					target = map[string]string{role.Catalog.GroupPermissions: "new_permission", "legacy_permissions": "old_permission"}[field.Name]
				}
				if source == "" || target == "" {
					t.Fatal("shared-through fixture omitted a declared relation", identity, field.Name)
				}
				field.Reverse = ir.ReverseRelation{Disabled: true}
				field.Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "bridges", ModelName: "bridge"}, SourceField: source, TargetField: target}
				fields = append(fields, field)
				other := field.Clone()
				other.Name, other.GoName = "unique_"+field.Name, "Unique"+field.GoName
				other.Through.Model.ModelName = "unique_bridge"
				fields = append(fields, other)
				if pair := source + "/" + target; !pairs[pair] {
					pairs[pair] = true
					unique.UniqueConstraints = append(unique.UniqueConstraints, ir.UniqueConstraint{Name: fmt.Sprintf("pair_%d", len(pairs)), Fields: []string{source, target}})
				}
			}
			model.ManyToMany = fields
		}
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "bridges", Package: codegen.PackageSpec{PackageName: "bridges", ImportPath: "example.com/godj-project-bundle/bridges", Directory: "bridges"},
		Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "bridges", Models: []ir.Model{bridge, unique}}})
	return spec, role
}
