package codegen_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/custom_test.go
var customAccountBindingConsumer []byte

//go:embed testdata/accountbinding/default_test.go
var defaultAccountBindingConsumer []byte

//go:embed testdata/accountbinding/database_test.go
var accountBindingDatabaseConsumer []byte

//go:embed testdata/accountbinding/profile_test.go
var accountBindingProfileConsumer []byte

//go:embed testdata/accountbinding/profile_http_test.go
var accountBindingProfileHTTPConsumer []byte

//go:embed testdata/accountbinding/password_http_test.go
var accountBindingPasswordHTTPConsumer []byte

//go:embed testdata/accountbinding/admin_mutation_http_test.go
var accountBindingAdminMutationHTTPConsumer []byte

//go:embed testdata/accountbinding/admin_catalog_http_test.go
var accountBindingAdminCatalogHTTPConsumer []byte

//go:embed testdata/accountbinding/admin_account_http_test.go
var accountBindingAdminAccountHTTPConsumer []byte

//go:embed testdata/accountbinding/password_admin_http_test.go
var accountBindingPasswordAdminHTTPConsumer []byte

//go:embed testdata/accountbinding/catalog_test.go
var accountBindingCatalogConsumer []byte

//go:embed testdata/accountbinding/first_account_test.go
var firstAccountCommonConsumer []byte

//go:embed testdata/accountbinding/first_default_test.go
var firstAccountDefaultConsumer []byte

//go:embed testdata/accountbinding/first_custom_test.go
var firstAccountCustomConsumer []byte

//go:embed testdata/accountbinding/creation_test.go
var accountBindingCreationConsumer []byte

//go:embed testdata/accountbinding/roles_test.go
var accountBindingRoleConsumer []byte

//go:embed testdata/accountbinding/delete_http_test.go
var accountBindingDeleteHTTPConsumer []byte

type accountLoginVariant struct {
	name                                                                   string
	custom, normalized, noLastLogin, formNormalized, shortLogin, textLogin bool
}

func accountLoginVariants() []accountLoginVariant {
	return []accountLoginVariant{
		{name: "default"}, {name: "custom", custom: true},
		{name: "custom_normalized", custom: true, normalized: true},
		{name: "custom_without_last_login", custom: true, noLastLogin: true},
		{name: "custom_form_normalized", custom: true, formNormalized: true},
		{name: "custom_short_login", custom: true, shortLogin: true},
		{name: "custom_text_login", custom: true, textLogin: true},
	}
}

func applyAccountLoginVariant(spec *codegen.ProjectSpec, variant accountLoginVariant) {
	if variant.normalized {
		spec.Account.AuthenticateNormalization = ir.AccountUnicode16NFKC
	}
	if variant.formNormalized {
		spec.Account.LoginFormNormalization = ir.AccountUnicode16NFKC
	}
	for i := range spec.Apps {
		for j := range spec.Apps[i].Schema.Models {
			model := &spec.Apps[i].Schema.Models[j]
			if spec.Apps[i].Schema.AppLabel != spec.Account.Model.AppLabel || model.Name != spec.Account.Model.ModelName {
				continue
			}
			for k := range model.Fields {
				field := &model.Fields[k]
				if field.Name != spec.Account.Login {
					continue
				}
				if variant.shortLogin {
					field.MaxLength = 100
				}
				if variant.textLogin {
					field.Kind, field.MaxLength = ir.FieldText, 0
				}
			}
			if variant.noLastLogin {
				model.Fields = slices.DeleteFunc(model.Fields, func(field ir.Field) bool { return field.Name == spec.Account.LastLogin })
			}
		}
	}
	if variant.noLastLogin {
		spec.Account.LastLogin = ""
	}
}

func TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels(t *testing.T) {
	for _, variant := range accountLoginVariants() {
		source, required := defaultAccountBindingConsumer, "TestDefaultAccountBindingOwnsTypedRows"
		if variant.custom {
			source, required = customAccountBindingConsumer, "TestCustomAccountBindingOwnsTypedRowsAndHostGraph"
		}
		t.Run(variant.name, func(t *testing.T) {
			spec := accountBindingConsumerSpec(t, variant.custom)
			applyAccountLoginVariant(&spec, variant)
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			writeGeneratedTestFile(t, root, "consumer/account_test.go", source)
			writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
			writeGeneratedTestFile(t, root, "consumer/profile_test.go", accountBindingProfileConsumer)
			writeGeneratedTestFile(t, root, "consumer/profile_http_test.go", accountBindingProfileHTTPConsumer)
			writeGeneratedTestFile(t, root, "consumer/password_http_test.go", accountBindingPasswordHTTPConsumer)
			writeGeneratedTestFile(t, root, "consumer/catalog_test.go", accountBindingCatalogConsumer)
			writeGeneratedTestFile(t, root, "consumer/roles_test.go", accountBindingRoleConsumer)
			writeGeneratedTestFile(t, root, "consumer/creation_test.go", accountBindingCreationConsumer)
			writeGeneratedTestFile(t, root, "consumer/delete_http_test.go", accountBindingDeleteHTTPConsumer)
			output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-count=1", "./consumer"))
			requiredNames := []string{required, required + "/password_change", required + "/profile_role_edit"}
			for _, name := range []string{"", "/duplicate", "/reserved", "/missing_choice", "/empty_password", "/missing_fields", "/missing_credential", "/null_fields", "/null_credential", "/unknown_envelope", "/unknown_command", "/csrf_before_parse", "/malformed_before_setters", "/raw_login_bytes"} {
				requiredNames = append(requiredNames, required+"/account_creation"+name)
			}
			if variant.custom {
				requiredNames = append(requiredNames, required+"/account_creation/required_profile")
			}
			for _, name := range []string{"", "/csrf", "/stale", "/additional_permission", "/commit"} {
				requiredNames = append(requiredNames, required+"/account_delete"+name)
			}
			if variant.custom {
				requiredNames = append(requiredNames, required+"/account_delete/protected")
			}
			for _, name := range []string{"login_presentation", "login_csrf", "login_input_limit", "login_fullwidth_policy", "form_csrf", "form_origin", "form_confirmation", "form_combined", "form_target", "json_csrf_before_parse", "json_target", "json_duplicate", "json_null", "json_combined", "form_success", "json_success"} {
				requiredNames = append(requiredNames, required+"/password_change/"+name)
			}
			assertGeneratedConsumerTests(t, output, requiredNames...)
			t.Logf("account binding child events (sha256=%x):\n%s", sha256.Sum256(output), output)
		})
	}
}

func TestGeneratedAccountBindingCannotMixSelectedPolicies(t *testing.T) {
	spec := accountBindingConsumerSpec(t, true)
	before, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	*spec.Account.Staff.Constant = true
	after, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, after)
	if output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./...").CombinedOutput(); err != nil {
		t.Fatalf("complete selected-policy control did not compile: %v\n%s", err, output)
	}
	for _, file := range before.Files() {
		if file.Path == "project/zz_godj_account.go" {
			writeGeneratedTestFile(t, root, file.Path, file.Source())
		}
	}
	output, err := generatedGoCommand(t.Context(), root, "test", "-run", "^$", "./...").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("undefined: goDjProjectSnapshot_"+before.SnapshotSHA256())) {
		t.Fatalf("mixed account policy did not fail on its source marker: %v\n%s", err, output)
	}
}

func accountBindingConsumerSpec(t *testing.T, custom bool) codegen.ProjectSpec {
	t.Helper()
	const module = "example.com/godj-project-bundle"
	if !custom {
		spec, err := modeldef.ProjectSpec(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		spec.Project.ImportPath = module + "/project"
		spec.Apps[0].Package.ImportPath = module + "/models"
		return spec
	}
	role, schemas, err := testschema.CustomAccount()
	if err != nil {
		t.Fatal(err)
	}
	spec := codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Account: &role}
	for _, definition := range schemas {
		label := definition.AppLabel
		spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: label, Package: codegen.PackageSpec{PackageName: label, ImportPath: module + "/" + label, Directory: label}, Schema: definition})
	}
	return spec
}

func firstAccountConsumerSpec(t *testing.T, variant accountLoginVariant) codegen.ProjectSpec {
	t.Helper()
	spec := accountBindingConsumerSpec(t, variant.custom)
	applyAccountLoginVariant(&spec, variant)
	if variant.custom {
		*spec.Account.Staff.Constant = true
	}
	for i := range spec.Apps {
		for j := range spec.Apps[i].Schema.Models {
			model := &spec.Apps[i].Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: spec.Apps[i].Schema.AppLabel, ModelName: model.Name}
			if variant.custom && (identity == spec.Account.Catalog.Group || identity == spec.Account.Catalog.Permission) {
				// Catalog creation owns revision=1, independently of a model default.
				revision := spec.Account.Catalog.GroupRevision
				if identity == spec.Account.Catalog.Permission {
					revision = spec.Account.Catalog.PermissionRevision
					model.UniqueConstraints = append(model.UniqueConstraints, ir.UniqueConstraint{Name: "catalog_title_description", Fields: []string{spec.Account.Catalog.PermissionName, "description"}})
				}
				for k := range model.Fields {
					if model.Fields[k].Name == revision {
						model.Fields[k].Default = nil
					}
				}
			}
			if identity != spec.Account.Model {
				continue
			}
			for k := range model.Fields {
				field := &model.Fields[k]
				if variant.custom && field.Name == "region" {
					field.Choices = []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "NZ"}, Label: "New Zealand"}, {Value: ir.Scalar{Kind: ir.ScalarString, String: "KR"}, Label: "South Korea"}}
				} else if !variant.custom && field.Name == "first_name" {
					field.Default = &ir.Scalar{Kind: ir.ScalarString, String: "NZ"}
				}
			}
		}
	}
	return spec
}

func TestGeneratedFirstAccountUsesRequiredProfiles(t *testing.T) {
	for _, variant := range accountLoginVariants() {
		t.Run(variant.name, func(t *testing.T) {
			spec := firstAccountConsumerSpec(t, variant)
			source := firstAccountDefaultConsumer
			if variant.custom {
				source = firstAccountCustomConsumer
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
			writeGeneratedTestFile(t, root, "consumer/first_account_test.go", firstAccountCommonConsumer)
			writeGeneratedTestFile(t, root, "consumer/password_admin_http_test.go", accountBindingPasswordAdminHTTPConsumer)
			writeGeneratedTestFile(t, root, "consumer/admin_mutation_http_test.go", accountBindingAdminMutationHTTPConsumer)
			writeGeneratedTestFile(t, root, "consumer/admin_catalog_http_test.go", accountBindingAdminCatalogHTTPConsumer)
			writeGeneratedTestFile(t, root, "consumer/admin_account_http_test.go", accountBindingAdminAccountHTTPConsumer)
			writeGeneratedTestFile(t, root, "consumer/first_test.go", source)
			output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-count=1", "./consumer"))
			required := []string{"TestGeneratedFirstAccountBootstrap", "TestGeneratedAdminAccountMutation"}
			required = append(required, "TestGeneratedAccountAdminLifecycle")
			for _, name := range []string{"bounded_choices", "partial_unique", "invalid_choice", "password_required", "password_mismatch", "password_bytes", "password_policy", "csrf", "protected_input", "additional_permission", "add_requires_change", "create_usable", "create_unusable", "stale", "edit_policy", "edit_additional", "choice_operation", "update_and_history", "password_command", "self_deactivation", "delete_and_audit"} {
				required = append(required, "TestGeneratedAccountAdminLifecycle/"+name)
			}
			for _, name := range []string{"csrf", "stale", "protected_input", "cross_field", "self_deactivation"} {
				required = append(required, "TestGeneratedAdminAccountMutation/"+name)
			}
			adminParents := []string{"TestGeneratedFirstAccountBootstrap/admin_password"}
			if !variant.custom {
				adminParents = nil
				for _, name := range []string{"trimmed", "omitted_default", "explicit_empty"} {
					required = append(required, "TestGeneratedFirstAccountBootstrap/"+name)
					adminParents = append(adminParents, "TestGeneratedFirstAccountBootstrap/"+name+"/admin_password")
				}
			}
			for _, parent := range adminParents {
				catalogParent := strings.TrimSuffix(parent, "/admin_password") + "/admin_catalog"
				required = append(required, catalogParent)
				if variant.custom {
					required = append(required, catalogParent+"/partial_constraint")
				}
				for _, name := range []string{"create_permission", "bounded_choices", "create_group", "partial_unique", "invalid_choice", "csrf", "stale", "protected_input", "additional_permission", "choice_operation", "update_and_history", "delete_collected_membership"} {
					required = append(required, catalogParent+"/"+name)
				}
				required = append(required, parent)
				if !variant.custom {
					required = append(required, parent+"/login_staff")
				}
				for _, name := range []string{"login_presentation", "login_csrf", "login_input_limit", "login_byte_limit", "login_duplicate", "login_reserved", "login_inactive", "login_access", "login_final_access", "login_fullwidth_policy", "login_trimmed", "csrf", "mismatch", "byte_limit", "policy", "reserved", "stale", "additional_permission", "change", "disable_confirmation", "disable"} {
					required = append(required, parent+"/"+name)
				}
			}
			assertGeneratedConsumerTests(t, output, required...)
			t.Logf("first-account child events (sha256=%x):\n%s", sha256.Sum256(output), output)
		})
	}
}
