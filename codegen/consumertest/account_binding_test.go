package codegen_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"slices"
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

func TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels(t *testing.T) {
	for _, variant := range []struct{ custom, normalized, noLastLogin bool }{{}, {custom: true}, {custom: true, normalized: true}, {custom: true, noLastLogin: true}} {
		custom := variant.custom
		name, source, required := "default", defaultAccountBindingConsumer, "TestDefaultAccountBindingOwnsTypedRows"
		if custom {
			name, source, required = "custom", customAccountBindingConsumer, "TestCustomAccountBindingOwnsTypedRowsAndHostGraph"
		}
		if variant.normalized {
			name = "custom_normalized"
		}
		if variant.noLastLogin {
			name = "custom_without_last_login"
		}
		t.Run(name, func(t *testing.T) {
			spec := accountBindingConsumerSpec(t, custom)
			if variant.normalized {
				spec.Account.AuthenticateNormalization = ir.AccountUnicode16NFKC
			}
			if variant.noLastLogin {
				field := spec.Account.LastLogin
				for i := range spec.Apps {
					if spec.Apps[i].Schema.AppLabel != spec.Account.Model.AppLabel {
						continue
					}
					for j := range spec.Apps[i].Schema.Models {
						model := &spec.Apps[i].Schema.Models[j]
						if model.Name == spec.Account.Model.ModelName {
							model.Fields = slices.DeleteFunc(model.Fields, func(candidate ir.Field) bool { return candidate.Name == field })
						}
					}
				}
				spec.Account.LastLogin = ""
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			writeGeneratedTestFile(t, root, "consumer/account_test.go", source)
			writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
			writeGeneratedTestFile(t, root, "consumer/profile_test.go", accountBindingProfileConsumer)
			writeGeneratedTestFile(t, root, "consumer/profile_http_test.go", accountBindingProfileHTTPConsumer)
			writeGeneratedTestFile(t, root, "consumer/catalog_test.go", accountBindingCatalogConsumer)
			writeGeneratedTestFile(t, root, "consumer/roles_test.go", accountBindingRoleConsumer)
			writeGeneratedTestFile(t, root, "consumer/creation_test.go", accountBindingCreationConsumer)
			output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-count=1", "./consumer"))
			assertGeneratedConsumerTests(t, output, required)
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

func TestGeneratedFirstAccountUsesRequiredProfiles(t *testing.T) {
	for _, variant := range []struct {
		name                string
		custom, noLastLogin bool
	}{{name: "default"}, {name: "custom", custom: true}, {name: "custom_without_last_login", custom: true, noLastLogin: true}} {
		t.Run(variant.name, func(t *testing.T) {
			spec := accountBindingConsumerSpec(t, variant.custom)
			source := firstAccountDefaultConsumer
			if variant.custom {
				*spec.Account.Staff.Constant = true
				source = firstAccountCustomConsumer
			}
			if variant.noLastLogin {
				name := spec.Account.LastLogin
				for i := range spec.Apps {
					if spec.Apps[i].Schema.AppLabel == spec.Account.Model.AppLabel {
						for j := range spec.Apps[i].Schema.Models {
							model := &spec.Apps[i].Schema.Models[j]
							if model.Name == spec.Account.Model.ModelName {
								model.Fields = slices.DeleteFunc(model.Fields, func(field ir.Field) bool { return field.Name == name })
							}
						}
					}
				}
				spec.Account.LastLogin = ""
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
			writeGeneratedTestFile(t, root, "consumer/first_account_test.go", firstAccountCommonConsumer)
			writeGeneratedTestFile(t, root, "consumer/first_test.go", source)
			output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-count=1", "./consumer"))
			assertGeneratedConsumerTests(t, output, "TestGeneratedFirstAccountBootstrap")
			t.Logf("first-account child events (sha256=%x):\n%s", sha256.Sum256(output), output)
		})
	}
}
