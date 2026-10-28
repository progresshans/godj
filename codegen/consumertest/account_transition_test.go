package codegen_test

import (
	_ "embed"
	"encoding/json"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/transition_test.go.tmpl
var accountTransitionConsumer string

//go:embed testdata/accountbinding/transition_creation_test.go.tmpl
var accountTransitionCreationConsumer string

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
	runAccountConsumerDatabases(t, root, required)
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
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}
