package codegen_test

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/creation_input_test.go.tmpl
var accountCreationInputConsumer string

//go:embed testdata/accountbinding/password_policy_test.go.tmpl
var accountPasswordPolicyConsumer string

//go:embed testdata/accountbinding/admin_creation_choice_test.go.tmpl
var accountAdminCreationChoiceConsumer string

//go:embed testdata/accountbinding/creation_read_validation_test.go.tmpl
var accountCreationReadValidationConsumer string

//go:embed testdata/accountbinding/admin_unicode_test.go.tmpl
var accountAdminUnicodeConsumer string

func TestGeneratedAccountCreationInputPolicy(t *testing.T) {
	inputs, err := identityreference.UserCreationInputs()
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name string }
	if err := json.Unmarshal(inputs, &cases); err != nil || len(cases) != 20 {
		t.Fatal("creation input corpus is incomplete", err)
	}
	unicodeInputs, err := identityreference.UnicodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	unicodeReference, err := identityreference.Unicode()
	if err != nil {
		t.Fatal(err)
	}
	var unicodeCases struct{ Cases []struct{ Name string } }
	if err := json.Unmarshal(unicodeInputs, &unicodeCases); err != nil || len(unicodeCases.Cases) != 12 {
		t.Fatal("Unicode input corpus is incomplete", err)
	}
	for _, custom := range []bool{false, true} {
		variant := "default"
		if custom {
			variant = "renamed"
		}
		t.Run(variant, func(t *testing.T) {
			spec := accountBindingConsumerSpec(t, custom)
			profile := "first_name"
			if custom {
				profile = "region"
				spec.Account.Email, spec.Account.WriteEmailNormalization = "", ""
				spec.Account.Staff = ir.AccountBoolean{Field: "privileged"}
			}
			var imports, schemas []string
			types := make(map[ir.ModelIdentity]string)
			for i := range spec.Apps {
				app := &spec.Apps[i]
				alias := fmt.Sprintf("model%d", i)
				imports = append(imports, fmt.Sprintf("%s %q", alias, app.Package.ImportPath))
				schemas = append(schemas, alias+".GoDjRelationSchema()")
				for j := range app.Schema.Models {
					model := &app.Schema.Models[j]
					identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
					types[identity] = alias + "." + model.GoName
					if !custom || identity != spec.Account.Model {
						continue
					}
					for k := range model.Fields {
						field := &model.Fields[k]
						if field.Name == spec.Account.Login {
							field.Kind, field.MaxLength = ir.FieldChar, 256
						}
					}
					model.Fields = append(model.Fields,
						ir.Field{Name: "privileged", GoName: "Privileged", Kind: ir.FieldBoolean, Default: &ir.Scalar{Kind: ir.ScalarBoolean}},
						ir.Field{Name: "username", GoName: "Username", Kind: ir.FieldChar, MaxLength: 64, Default: &ir.Scalar{Kind: ir.ScalarString, String: "unrelated-name"}},
						ir.Field{Name: "password1", GoName: "Password1", Kind: ir.FieldChar, MaxLength: 64, Default: &ir.Scalar{Kind: ir.ScalarString, String: "unrelated-command-name"}})
				}
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			source := strings.NewReplacer("@@IMPORTS@@", strings.Join(imports, "\n"), "@@SCHEMAS@@", strings.Join(schemas, ","),
				"@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission], "@@PROFILE@@", profile).Replace(accountCreationInputConsumer)
			formatted, err := format.Source([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/creation_input_test.go", formatted)
			formatted, err = format.Source([]byte(accountPasswordPolicyConsumer))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/password_policy_test.go", formatted)
			formatted, err = format.Source([]byte(accountAdminCreationChoiceConsumer))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/admin_creation_choice_test.go", formatted)
			formatted, err = format.Source([]byte(accountCreationReadValidationConsumer))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/creation_read_validation_test.go", formatted)
			formatted, err = format.Source([]byte(strings.ReplaceAll(accountAdminUnicodeConsumer, "@@PROFILE@@", profile)))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/admin_unicode_test.go", formatted)
			writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
			writeGeneratedTestFile(t, root, "consumer/testdata/user-creation-inputs.json", inputs)
			writeGeneratedTestFile(t, root, "consumer/testdata/unicode-inputs.json", unicodeInputs)
			writeGeneratedTestFile(t, root, "consumer/testdata/unicode-django61.json", unicodeReference)
			for _, vendor := range []string{"sqlite", "postgres"} {
				reference, err := identityreference.UserCreation(vendor)
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, "consumer/testdata/user-creation-django61-"+vendor+".json", reference)
				reference, err = identityreference.UnusablePassword(vendor)
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, "consumer/testdata/unusable-password-django61-"+vendor+".json", reference)
			}
			required := []string{"TestCreationInputPolicy"}
			required = append(required, "TestAccountAdminUnicodeInput")
			for _, probe := range unicodeCases.Cases {
				required = append(required, "TestAccountAdminUnicodeInput/create/"+probe.Name)
			}
			for _, name := range []string{"astral256", "ascii256", "compatibility_expansion"} {
				required = append(required, "TestAccountAdminUnicodeInput/edit_stored_width/"+name)
			}
			for _, surface := range []string{"form", "admin"} {
				modes := []string{"standard", "admin_enabled", "admin_disabled"}
				if surface == "admin" {
					modes = modes[1:]
				}
				for _, mode := range modes {
					prefix := required[0] + "/" + surface + "/" + mode
					required = append(required, prefix)
					for _, probe := range cases {
						required = append(required, prefix+"/"+probe.Name)
					}
					if surface == "form" {
						for _, operation := range []string{"immediate", "deferred", "abandoned", "invalid"} {
							required = append(required, prefix+"/lifecycle_"+operation)
						}
					}
				}
			}
			for _, child := range []string{"stored_limit", "raw_normalization_limit", "forged_length", "forged_grammar", "forged_normalization", "late_duplicate"} {
				required = append(required, required[0]+"/boundaries/"+child)
			}
			for _, operation := range []string{"single_attempt_after_delete", "concurrent_copies", "unadmitted_calls"} {
				for _, mode := range []string{"usable", "unusable"} {
					required = append(required, required[0]+"/prepared_ownership/"+operation+"/"+mode)
				}
			}
			required = append(required, required[0]+"/foreign_values")
			for _, mode := range []string{"standard", "admin_enabled"} {
				prefix := required[0] + "/foreign_values/" + mode
				required = append(required, prefix)
				for _, kind := range []string{"mismatch", "grammar", "type", "field", "canceled"} {
					required = append(required, prefix+"/"+kind)
				}
			}
			required = append(required, "TestAccountPasswordPolicyBoundaries")
			for _, operation := range []string{"create", "password"} {
				for _, mode := range []string{"preflight", "final", "cleanup", "swallowed", "unknown", "cancel_preflight", "cancel_final"} {
					required = append(required, "TestAccountPasswordPolicyBoundaries/"+operation+"/"+mode)
				}
			}
			const defaults = "TestAccountAdminDefaultPasswordPolicy"
			required = append(required, defaults)
			for _, operation := range []string{"create", "password"} {
				prefix := defaults + "/" + operation
				required = append(required, prefix)
				for _, name := range []string{"short", "common", "numeric", "similarity", "combined", "success_and_reopen"} {
					required = append(required, prefix+"/"+name)
				}
			}
			const disablement = "TestAccountAdminPasswordDisablement"
			required = append(required, disablement)
			for _, mode := range []string{"csrf", "confirmation", "stale", "disable", "repeat_disable", "restore_policy", "restore_and_reopen"} {
				required = append(required, disablement+"/"+mode)
			}
			const choices = "TestAccountAdminCreationChoices"
			required = append(required, choices, choices+"/password_choice", choices+"/policy_and_validation", choices+"/current_authority")
			for _, name := range []string{"default_missing", "enabled_missing", "enabled_mismatch", "enabled_equal", "disabled_empty", "disabled_mismatch", "disabled_equal", "disabled_duplicate"} {
				required = append(required, choices+"/password_choice/"+name)
			}
			for _, name := range []string{"csrf", "grammar", "disabled", "host_policy"} {
				required = append(required, choices+"/policy_and_validation/"+name)
			}
			for _, mode := range []string{"usable", "unusable"} {
				required = append(required, choices+"/current_authority/"+mode)
			}
			const reads = "TestAccountCreationReadValidation"
			required = append(required, reads, reads+"/current_authority_precedes_diagnostics", reads+"/read_failures_are_not_input", reads+"/revoked_authority", reads+"/duplicate_after_hash")
			for _, mode := range []string{"read_end_failure", "read_zero", "read_nil", "read_twice", "read_swallowed_failure", "canceled"} {
				required = append(required, reads+"/read_failures_are_not_input/"+mode)
			}
			runAccountConsumerDatabases(t, root, required)
		})
	}
}
