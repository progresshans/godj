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

//go:embed testdata/accountbinding/transition_target_references_test.go.tmpl
var accountTransitionTargetReferenceConsumer string

//go:embed testdata/accountbinding/transition_catalog_fields_test.go.tmpl
var accountTransitionCatalogFieldsConsumer string

func accountSharedThroughCases(strategy string) []string {
	result := []string{"lifecycle", "signed_keys", "shared_group", "shared_permission", "late_payload", "late_catalog_payload", "late_endpoint", "late_extra_null", "late_remove", "audit_failure", "rollback", "unknown_commit", "unknown_rollback"}
	for _, kind := range []string{"group", "permission"} {
		for _, field := range []string{"payload", "endpoint"} {
			result = append(result, "late_identity_"+kind+"_"+field)
		}
	}
	if strategy == "created" {
		result = append(result, "account_only", "identity_signed_keys", "identity_endpoints", "identity_source", "identity_catalog", "identity_catalog_escalation", "late_identity_account_payload", "late_identity_account_endpoint")
	}
	return result
}

func writeAccountSharedThroughConsumer(t *testing.T, spec codegen.ProjectSpec, role ir.AccountRole) string {
	t.Helper()
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
	var referenceCases []string
	for _, name := range accountTransitionTargetReferenceCases() {
		referenceCases = append(referenceCases, strconv.Quote(name))
	}
	source := strings.NewReplacer("@@ROLE@@", string(encoded), "@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec), "@@STAGED_CASES@@", cases("staged"), "@@CREATED_CASES@@", cases("created"), "@@REFERENCE_CASES@@", strings.Join(referenceCases, ",")).Replace(accountSharedThroughConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_shared_through_test.go", formatted)
	formatted, err = format.Source([]byte(strings.ReplaceAll(accountTransitionTargetReferenceConsumer, "@@REFERENCE_CASES@@", strings.Join(referenceCases, ","))))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_target_references_test.go", formatted)
	requiredFields := false
	for _, app := range spec.Apps {
		for _, model := range app.Schema.Models {
			if (ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}) != role.Catalog.Permission {
				continue
			}
			for _, field := range model.Fields {
				if field.Name == "parent_group" {
					requiredFields = !field.Nullable
				}
			}
		}
	}
	var catalogCases []string
	for _, name := range accountTransitionCatalogFieldCases(requiredFields) {
		catalogCases = append(catalogCases, strconv.Quote(name))
	}
	formatted, err = format.Source([]byte(strings.ReplaceAll(accountTransitionCatalogFieldsConsumer, "@@CATALOG_FIELD_CASES@@", strings.Join(catalogCases, ","))))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_catalog_fields_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}

func TestGeneratedAccountSharedThroughTransition(t *testing.T) {
	spec, role := accountSharedThroughSpec(t)
	root := writeAccountSharedThroughConsumer(t, spec, role)
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
	runAccountConsumerDatabases(t, root, required, "TestAccountSharedThroughTransition")
}

func accountTransitionTargetReferenceCases() []string {
	return []string{"lifecycle", "signed_keys", "owned_input", "reordered_input", "unknown_commit", "unknown_rollback", "rollback", "cycle", "self_cycle", "unknown_source", "wrong_revision", "wrong_kind", "unknown_field", "scalar_overlap", "missing_reference", "denied_choice", "late_choice", "policy_rejected", "duplicate_login", "unique_conflict", "first_insert_failure", "late_reference", "source_changed", "permission_revoked"}
}

func TestGeneratedAccountTransitionTargetReferences(t *testing.T) {
	for _, required := range []bool{false, true} {
		name := "nullable"
		if required {
			name = "required"
		}
		t.Run(name, func(t *testing.T) {
			spec, role := accountSharedThroughSpec(t)
			for i := range spec.Apps {
				if spec.Apps[i].Schema.AppLabel == role.Model.AppLabel {
					for j := range spec.Apps[i].Schema.Models[0].Fields {
						field := &spec.Apps[i].Schema.Models[0].Fields[j]
						if field.Name == "preferred_group" {
							field.Nullable, field.Blank = !required, !required
						}
					}
				}
			}
			root := writeAccountSharedThroughConsumer(t, spec, role)
			cases := []string{"TestAccountTransitionTargetReferences"}
			for _, name := range accountTransitionTargetReferenceCases() {
				cases = append(cases, "TestAccountTransitionTargetReferences/"+name)
			}
			runAccountConsumerDatabases(t, root, cases, "TestAccountTransitionTargetReferences")
		})
	}
}

func accountTransitionCatalogFieldCases(required bool) []string {
	cases := []string{"lifecycle", "signed_keys", "owned_input", "reordered_input", "staged_reference", "pending_account_reference", "unknown_commit", "unknown_rollback", "rollback", "cycle", "self_cycle", "unknown_source", "wrong_revision", "wrong_kind", "unknown_field", "scalar_overlap", "missing_reference", "denied_choice", "denied_static", "late_choice", "policy_rejected", "unique_conflict", "first_catalog_insert_failure", "late_reference", "late_host_reference", "source_changed", "permission_revoked", "missing_policy", "duplicate_policy", "wrong_policy_model"}
	cases = append(cases, "joint_chain", "joint_signed_keys", "joint_complete_policy", "joint_policy_rejected", "joint_cycle", "joint_late_reference", "joint_unknown_commit", "joint_unknown_rollback", "joint_rollback", "joint_first_account_failure", "joint_mid_extra_account", "joint_mid_link")
	if !required {
		cases = append(cases, "reverse_namespaces", "shared_group", "shared_permission")
	}
	return cases
}

func TestGeneratedAccountTransitionCatalogFields(t *testing.T) {
	for _, required := range []bool{false, true} {
		name := "nullable"
		if required {
			name = "required"
		}
		t.Run(name, func(t *testing.T) {
			spec, role := accountSharedThroughSpec(t)
			for i := range spec.Apps {
				app := &spec.Apps[i]
				for j := range app.Schema.Models {
					model := &app.Schema.Models[j]
					if (ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}) != role.Catalog.Permission {
						continue
					}
					for k := range model.Fields {
						field := &model.Fields[k]
						if field.Name == "parent_group" {
							field.Nullable, field.Blank = !required, !required
						}
					}
				}
			}
			root := writeAccountSharedThroughConsumer(t, spec, role)
			cases := []string{"TestAccountTransitionCatalogFields"}
			for _, name := range accountTransitionCatalogFieldCases(required) {
				cases = append(cases, "TestAccountTransitionCatalogFields/"+name)
			}
			runAccountConsumerDatabases(t, root, cases, "TestAccountTransitionCatalogFields")
		})
	}
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
	addTransitionIdentityThroughModels(&spec, old, role)
	addTransitionCatalogReferenceFields(&spec, old, role)
	return spec, role
}

// The same three native identity INSERTs also own physical through rows for
// source profiles, source catalog grants and an unrelated host collection.
// Nullable endpoints exercise an empty logical set with a nonempty inventory.
func addTransitionIdentityThroughModels(spec *codegen.ProjectSpec, old, next ir.AccountRole) {
	hubID := ir.ModelIdentity{AppLabel: "bridges", ModelName: "hub"}
	hub := ir.Model{Name: "hub", GoName: "Hub", DBTable: "shared_hub", Fields: []ir.Field{
		{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 64},
	}}
	identities := []struct {
		kind, goName string
		identity     ir.ModelIdentity
	}{{"member", "Member", next.Model}, {"team", "Team", next.Catalog.Group}, {"grant", "Grant", next.Catalog.Permission}}
	for _, item := range identities {
		relation := func(name, goName, source string) ir.ManyToManyField {
			return ir.ManyToManyField{Name: name, GoName: goName, Target: old.Catalog.Permission, Blank: true, Reverse: ir.ReverseRelation{Disabled: true},
				Through: &ir.ThroughModel{Model: item.identity, SourceField: source, TargetField: "identity_permission"}}
		}
		hub.ManyToMany = append(hub.ManyToMany, relation(item.kind+"_links", item.goName+"Links", "host_hub"))
		for i := range spec.Apps {
			app := &spec.Apps[i]
			for j := range app.Schema.Models {
				model := &app.Schema.Models[j]
				identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
				switch identity {
				case old.Model:
					model.ManyToMany = append(model.ManyToMany, relation(item.kind+"_rows", item.goName+"Rows", "identity_source"))
				case old.Catalog.Group:
					model.ManyToMany = append(model.ManyToMany, relation(item.kind+"_grants", item.goName+"Grants", "identity_group"))
				case item.identity:
					for _, field := range []struct {
						name, goName string
						target       ir.ModelIdentity
					}{{"identity_source", "IdentitySourceID", old.Model}, {"identity_group", "IdentityGroupID", old.Catalog.Group}, {"identity_permission", "IdentityPermissionID", old.Catalog.Permission}, {"host_hub", "HostHubID", hubID}} {
						model.Fields = append(model.Fields, ir.Field{Name: field.name, GoName: field.goName, Column: field.name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true,
							Relation: &ir.ForeignKeyRelation{Target: field.target, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}})
					}
				}
			}
		}
	}
	for i := range spec.Apps {
		if spec.Apps[i].Schema.AppLabel == "bridges" {
			spec.Apps[i].Schema.Models = append(spec.Apps[i].Schema.Models, hub)
		} else if spec.Apps[i].Schema.AppLabel == next.Model.AppLabel {
			model := &spec.Apps[i].Schema.Models[0]
			for _, field := range []struct {
				name, goName string
				target       ir.ModelIdentity
			}{
				{"mentor", "MentorID", next.Model}, {"preferred_group", "PreferredGroupID", next.Catalog.Group}, {"preferred_permission", "PreferredPermissionID", next.Catalog.Permission},
			} {
				model.Fields = append(model.Fields, ir.Field{Name: field.name, GoName: field.goName, Column: field.name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Relation: &ir.ForeignKeyRelation{Target: field.target, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}})
			}
			model.Fields = append(model.Fields, ir.Field{Name: "preferred_unique", GoName: "PreferredUniqueID", Column: "preferred_unique_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Unique: true, Relation: &ir.ForeignKeyRelation{Target: next.Catalog.Group, Cardinality: ir.RelationOneToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}})
		}
	}
}

func addTransitionCatalogReferenceFields(spec *codegen.ProjectSpec, old, next ir.AccountRole) {
	for i := range spec.Apps {
		app := &spec.Apps[i]
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			if identity == (ir.ModelIdentity{AppLabel: "bridges", ModelName: "hub"}) {
				model.Fields = append(model.Fields, ir.Field{Name: "unowned_group", GoName: "UnownedGroupID", Column: "unowned_group_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Relation: &ir.ForeignKeyRelation{Target: next.Catalog.Group, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}})
			}
			var fields []struct {
				name, goName string
				target       ir.ModelIdentity
				one          bool
			}
			if identity == next.Catalog.Group {
				fields = []struct {
					name, goName string
					target       ir.ModelIdentity
					one          bool
				}{
					{"catalog_permission", "CatalogPermissionID", next.Catalog.Permission, false},
					{"legacy_permission", "LegacyPermissionID", old.Catalog.Permission, false},
					{"owner", "OwnerID", next.Model, false},
				}
			} else if identity == next.Catalog.Permission {
				fields = []struct {
					name, goName string
					target       ir.ModelIdentity
					one          bool
				}{
					{"parent_group", "ParentGroupID", next.Catalog.Group, false},
					{"legacy_group", "LegacyGroupID", old.Catalog.Group, false},
					{"previous", "PreviousID", next.Catalog.Permission, false},
					{"unique_group", "UniqueGroupID", next.Catalog.Group, true},
				}
			}
			for _, ref := range fields {
				cardinality := ir.RelationManyToOne
				if ref.one {
					cardinality = ir.RelationOneToOne
				}
				model.Fields = append(model.Fields, ir.Field{Name: ref.name, GoName: ref.goName, Column: ref.name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Unique: ref.one, Relation: &ir.ForeignKeyRelation{Target: ref.target, Cardinality: cardinality, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}})
			}
		}
	}
}
