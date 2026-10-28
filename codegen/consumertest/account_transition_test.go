package codegen_test

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/migrationautodetect"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/transition_test.go.tmpl
var accountTransitionConsumer string

//go:embed testdata/accountbinding/transition_creation_test.go.tmpl
var accountTransitionCreationConsumer string

//go:embed testdata/accountbinding/transition_references_test.go.tmpl
var accountTransitionReferencesConsumer string

func TestGeneratedAccountTransition(t *testing.T) {
	root := writeAccountTransitionConsumer(t)
	required := []string{"TestAccountTransition"}
	for _, name := range []string{"lifecycle", "signed_keys", "round_trip", "duplicate_mapping", "incomplete_mapping", "account_limit", "wrong_password", "wrong_principal", "wrong_roles", "wrong_membership", "source_changed", "target_changed", "profile_collection_changed", "new_source", "new_target", "grant_changed", "policy_changed", "permission_revoked", "old_domain", "canceled", "read_cleanup", "rollback", "audit_failure", "receipt_failure", "mapping_failure", "late_target_change", "unknown_commit", "unknown_rollback", "concurrent", "configuration"} {
		required = append(required, "TestAccountTransition/"+name)
	}
	required = append(required, "TestAccountTransitionCreation", "TestAccountTransitionCreationToGeneratedDefault")
	for _, name := range []string{"lifecycle", "signed_keys", "defaults", "invalid_login", "empty_required_profile", "missing_login", "protected_input", "role_membership_input", "target_key", "incomplete_mapping", "duplicate_source", "source_revision", "source_changed", "grant_changed", "new_source", "new_target", "initial_target", "unexpected_reference_read", "policy_changed", "choice_changed", "request_owned", "profile_limit", "unknown_commit", "unknown_rollback", "insert_after_write", "collection_after_write", "creation_audit", "receipt_failure", "mapping_failure", "late_target_change", "target_rewrite", "rollback", "read_cleanup", "canceled", "concurrent", "configuration", "late_choice_change", "duplicate_login"} {
		required = append(required, "TestAccountTransitionCreation/"+name)
	}
	required = append(required, "TestAccountTransitionReferences")
	for _, strategy := range []string{"staged", "created"} {
		required = append(required, "TestAccountTransitionReferences/"+strategy)
		for _, name := range accountTransitionReferenceCases(strategy) {
			required = append(required, "TestAccountTransitionReferences/"+strategy+"/"+name)
		}
	}
	runAccountConsumerDatabases(t, root, required)
}

func accountTransitionReferenceCases(strategy string) []string {
	result := []string{"lifecycle", "schema_lifecycle", "signed_keys", "null_references", "no_revision", "page_boundary", "row_limit", "revision_limit", "callback_owned", "config_owned", "missing_grant", "permission_revoked", "policy_rejected", "row_changed", "new_row", "row_deleted", "second_write_failure", "audit_after_write", "late_host_change", "host_source_change", "lost_update", "unknown_commit", "unknown_rollback", "concurrent", "configuration", "read_cleanup", "canceled", "late_policy_change", "one_to_one_conflict"}
	if strategy == "staged" {
		result = append(result, "already_mapped", "wrong_target", "target_without_source")
	} else {
		result = append(result, "target_insert_host_change")
	}
	return result
}

func writeAccountTransitionConsumer(t *testing.T) string {
	t.Helper()
	spec := accountBindingConsumerSpec(t, false)
	var member ir.Model
	for _, model := range spec.Apps[0].Schema.Models {
		if model.Name == spec.Account.Model.ModelName {
			member = model.Clone()
		}
	}
	member.Name, member.GoName, member.DBTable = "member", "Member", "members_member"
	names := map[string][2]string{"username": {"handle", "Handle"}, "principal_id": {"subject", "Subject"}, "encoded_password": {"secret_hash", "SecretHash"}, "revision": {"version", "Version"}}
	for i := range member.Fields {
		if renamed, ok := names[member.Fields[i].Name]; ok {
			member.Fields[i].Name, member.Fields[i].Column, member.Fields[i].GoName = renamed[0], renamed[0], renamed[1]
		}
	}
	for i := range member.ManyToMany {
		member.ManyToMany[i].Reverse = ir.ReverseRelation{Disabled: true}
	}
	member.Fields = append(member.Fields,
		ir.Field{Name: "department", GoName: "DepartmentID", Column: "department_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Relation: &ir.ForeignKeyRelation{Target: spec.Account.Catalog.Group, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: ir.DeleteProtect}},
		ir.Field{Name: "portrait", GoName: "Portrait", Column: "portrait", Kind: ir.FieldFile, MaxLength: 100, Blank: true},
		ir.Field{Name: "region", GoName: "Region", Column: "region", Kind: ir.FieldChar, MaxLength: 32, Default: &ir.Scalar{Kind: ir.ScalarString, String: "earth"}},
	)
	member.ManyToMany = append(member.ManyToMany, ir.ManyToManyField{Name: "interests", GoName: "Interests", Target: spec.Account.Catalog.Permission, Reverse: ir.ReverseRelation{Disabled: true}, Blank: true})
	for i := range spec.Apps[0].Schema.Models {
		model := &spec.Apps[0].Schema.Models[i]
		if model.Name == spec.Account.Model.ModelName {
			model.ManyToMany = append(model.ManyToMany, ir.ManyToManyField{Name: "topics", GoName: "Topics", Target: spec.Account.Catalog.Permission, Reverse: ir.ReverseRelation{Disabled: true}, Blank: true})
		}
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "members", Package: codegen.PackageSpec{PackageName: "members", ImportPath: "example.com/godj-project-bundle/members", Directory: "members"}, Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "members", Models: []ir.Model{member}}})
	addAccountTransitionHostModels(&spec)
	role := spec.Account.Clone()
	role.Model = ir.ModelIdentity{AppLabel: "members", ModelName: "member"}
	role.Login, role.PrincipalID, role.Password, role.Revision = "handle", "subject", "secret_hash", "version"
	encoded, err := json.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	source := strings.NewReplacer("@@ROLE@@", string(encoded), "@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec)).Replace(accountTransitionConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_test.go", formatted)
	source = strings.NewReplacer("@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec)).Replace(accountTransitionCreationConsumer)
	formatted, err = format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_creation_test.go", formatted)
	legacy, contracted := accountTransitionSchemaVariant(spec, "legacy"), accountTransitionSchemaVariant(spec, "contracted")
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
	source = strings.NewReplacer("@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec), "@@LEGACY_MIGRATIONS@@", stages[0], "@@EXPANDED_MIGRATIONS@@", stages[1], "@@CONTRACTED_MIGRATIONS@@", stages[2]).Replace(accountTransitionReferencesConsumer)
	formatted, err = format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_references_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}

func accountTransitionSchemaVariant(spec codegen.ProjectSpec, phase string) codegen.ProjectSpec {
	result := spec
	result.Project.Directory = phase + "/project"
	result.Project.ImportPath = "example.com/godj-project-bundle/" + phase + "/project"
	result.Apps = slices.Clone(spec.Apps)
	for i := range result.Apps {
		app := &result.Apps[i]
		app.Package.Directory = phase + "/" + app.Package.Directory
		app.Package.ImportPath = "example.com/godj-project-bundle/" + app.Package.Directory
		app.Schema = app.Schema.Clone()
		if app.Schema.AppLabel != "host" {
			continue
		}
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			model.Fields = slices.DeleteFunc(model.Fields, func(field ir.Field) bool {
				if phase == "contracted" {
					return strings.HasPrefix(field.Name, "old_")
				}
				return field.Relation != nil && field.Relation.Target == (ir.ModelIdentity{AppLabel: "members", ModelName: "member"})
			})
			model.ManyToMany = slices.DeleteFunc(model.ManyToMany, func(field ir.ManyToManyField) bool {
				if phase == "contracted" {
					return field.Name == "legacy_members"
				}
				return field.Name == "members"
			})
		}
	}
	return result
}

func accountTransitionMigrationStages(t *testing.T, specs ...codegen.ProjectSpec) []string {
	t.Helper()
	var sources []definition.Source
	var result []string
	for _, spec := range specs {
		var schemas []ir.Schema
		for _, app := range spec.Apps {
			schemas = append(schemas, app.Schema)
		}
		desired, err := migrations.NewProjectState(schemas...)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(sources...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := migrationautodetect.Detect(migrationautodetect.Request{Definitions: loaded, Desired: desired, ManagedApps: desired.Apps()})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Migrations()) == 0 {
			t.Fatal("account transition schema phase produced no migration")
		}
		for _, migration := range plan.Migrations() {
			wire, err := definition.Encode(definition.Producer{Name: "account-transition-consumer", Version: "1"}, migration)
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, definition.Source{SourceID: migration.App + "/" + migration.Name, Document: wire})
		}
		var literals []string
		for _, source := range sources {
			literals = append(literals, fmt.Sprintf("definition.Source{SourceID:%q,Document:[]byte(%q)}", source.SourceID, source.Document))
		}
		result = append(result, strings.Join(literals, ","))
	}
	return result
}

func addAccountTransitionHostModels(spec *codegen.ProjectSpec) {
	old := spec.Account.Model
	next := ir.ModelIdentity{AppLabel: "members", ModelName: "member"}
	key := ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
	revision := ir.Field{Name: "revision", GoName: "Revision", Column: "revision", Kind: ir.FieldInteger, Default: &ir.Scalar{Kind: ir.ScalarInteger, Integer: 1}}
	label := func(name, goName string) ir.Field {
		return ir.Field{Name: name, GoName: goName, Column: name, Kind: ir.FieldChar, MaxLength: 64}
	}
	ref := func(name, goName string, target ir.ModelIdentity, one bool) ir.Field {
		cardinality := ir.RelationManyToOne
		if one {
			cardinality = ir.RelationOneToOne
		}
		return ir.Field{Name: name, GoName: goName, Column: name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Unique: one, Relation: &ir.ForeignKeyRelation{Target: target, Cardinality: cardinality, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	ticket := ir.Model{Name: "ticket", GoName: "Ticket", DBTable: "transition_ticket", Fields: []ir.Field{key, label("title", "Title"), revision, ref("old_owner", "OldOwnerID", old, false), ref("owner", "OwnerID", next, false), ref("old_reviewer", "OldReviewerID", old, false), ref("reviewer", "ReviewerID", next, false)}}
	badge := ir.Model{Name: "badge", GoName: "Badge", DBTable: "transition_badge", Fields: []ir.Field{key, label("name", "Name"), ref("old_user", "OldUserID", old, false), ref("user", "UserID", next, true)}}
	workspace := ir.Model{Name: "workspace", GoName: "Workspace", DBTable: "transition_workspace", Fields: []ir.Field{key, label("name", "Name")}}
	assignment := ir.Model{Name: "assignment", GoName: "Assignment", DBTable: "transition_assignment", Fields: []ir.Field{key, label("note", "Note"), revision, ref("workspace", "WorkspaceID", ir.ModelIdentity{AppLabel: "host", ModelName: "workspace"}, false), ref("old_user", "OldUserID", old, false), ref("user", "UserID", next, false)}}
	for _, item := range []struct {
		name, goName, field string
		target              ir.ModelIdentity
	}{{"legacy_members", "LegacyMembers", "old_user", old}, {"members", "Members", "user", next}} {
		workspace.ManyToMany = append(workspace.ManyToMany, ir.ManyToManyField{Name: item.name, GoName: item.goName, Target: item.target, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "host", ModelName: "assignment"}, SourceField: "workspace", TargetField: item.field}})
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "host", Package: codegen.PackageSpec{PackageName: "host", ImportPath: "example.com/godj-project-bundle/host", Directory: "host"}, Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "host", Models: []ir.Model{ticket, badge, workspace, assignment}}})
}
