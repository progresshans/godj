package codegen_test

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func selectedResetConsumerSpec(t *testing.T, variant string) codegen.ProjectSpec {
	t.Helper()
	spec := accountBindingConsumerSpec(t, variant != "default")
	for i := range spec.Apps {
		for j := range spec.Apps[i].Schema.Models {
			model := &spec.Apps[i].Schema.Models[j]
			if spec.Apps[i].Schema.AppLabel != spec.Account.Model.AppLabel || model.Name != spec.Account.Model.ModelName {
				continue
			}
			if variant == "custom_without_last_login" {
				model.Fields = slices.DeleteFunc(model.Fields, func(field ir.Field) bool { return field.Name == spec.Account.LastLogin })
				spec.Account.LastLogin = ""
			}
			if variant == "custom_renamed_key_email" {
				for k := range model.Fields {
					if model.Fields[k].PrimaryKey {
						model.Fields[k].Name, model.Fields[k].Column, model.Fields[k].GoName = "account_key", "account_key", "AccountKey"
					}
				}
				model.Fields = append(model.Fields, ir.Field{Name: "delivery_address", Column: "delivery_address", GoName: "DeliveryAddress", Kind: ir.FieldText, Blank: true, Default: &ir.Scalar{Kind: ir.ScalarString}})
				spec.Account.Email = "delivery_address"
			}
		}
	}
	if variant == "custom_without_email" {
		spec.Account.Email, spec.Account.WriteEmailNormalization = "", ""
	}
	if variant == "custom_active_true" || variant == "custom_active_false" {
		active := variant == "custom_active_true"
		spec.Account.Active = ir.AccountBoolean{Constant: &active}
	}
	if variant != "default" {
		for i := range spec.Apps {
			if spec.Apps[i].Schema.AppLabel != spec.Account.Model.AppLabel {
				continue
			}
			for _, model := range spec.Apps[i].Schema.Models {
				if model.Name == spec.Account.Model.ModelName {
					shadow := model.Clone()
					shadow.Name, shadow.GoName, shadow.DBTable = "replacement", "Replacement", "selected_reset_replacement"
					spec.Apps[i].Schema.Models = append(spec.Apps[i].Schema.Models, shadow)
					break
				}
			}
		}
	}
	return spec
}

func TestGeneratedAccountResetUsesSelectedModels(t *testing.T) {
	for _, variant := range []string{"default", "custom", "custom_without_last_login", "custom_without_email", "custom_renamed_key_email", "custom_active_true", "custom_active_false"} {
		t.Run(variant, func(t *testing.T) {
			bundle, err := codegen.GenerateProject(selectedResetConsumerSpec(t, variant))
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
			entry := "reset_custom_test.go"
			if variant == "default" {
				entry = "reset_default_test.go"
			}
			for _, name := range []string{"reset_database_test.go", "reset_http_test.go", "reset_model_boundary_test.go", entry} {
				data, err := os.ReadFile(filepath.Join("testdata", "accountbinding", name))
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, "consumer/"+name, data)
			}
			required := []string{"TestSelectedAccountReset"}
			for _, vendor := range []string{"sqlite", "postgres"} {
				if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
					continue
				}
				prefix := "TestSelectedAccountReset/" + vendor
				required = append(required, prefix)
				if variant != "default" && variant != "custom_active_false" {
					required = append(required, prefix+"/different_model_and_generation")
				}
				if variant == "custom_without_email" {
					required = append(required, prefix+"/no_email_role")
					continue
				}
				if variant == "custom_active_false" {
					required = append(required, prefix+"/inactive_constant")
					continue
				}
				required = append(required, prefix+"/owner_mix_rejected")
				if variant == "custom_renamed_key_email" {
					required = append(required, prefix+"/stored_email_bound")
				}
				for _, surface := range []string{"form", "json"} {
					required = append(required, prefix+"/"+surface)
					for _, mode := range []string{"missing", "anonymous", "self", "other"} {
						parent := prefix + "/" + surface + "/" + mode
						required = append(required, parent)
						for _, refusal := range []string{"csrf", "origin", "weak", "byte_limit", "extra_target", "wrong_target", "fresh_browser"} {
							required = append(required, parent+"/"+refusal)
						}
					}
				}
			}
			output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-count=1", "-timeout=5m", "-run", "^TestSelectedAccountReset$", "./consumer"))
			assertGeneratedConsumerTests(t, output, required...)
			t.Logf("selected account reset child events (sha256=%x):\n%s", sha256.Sum256(output), output)
		})
	}
}
