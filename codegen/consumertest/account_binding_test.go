package codegen_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
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

func TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels(t *testing.T) {
	for _, variant := range []struct{ custom, normalized bool }{{}, {custom: true}, {custom: true, normalized: true}} {
		custom := variant.custom
		name, source, required := "default", defaultAccountBindingConsumer, "TestDefaultAccountBindingOwnsTypedRows"
		if custom {
			name, source, required = "custom", customAccountBindingConsumer, "TestCustomAccountBindingOwnsTypedRowsAndHostGraph"
		}
		if variant.normalized {
			name = "custom_normalized"
		}
		t.Run(name, func(t *testing.T) {
			spec := accountBindingConsumerSpec(t, custom)
			if variant.normalized {
				spec.Account.AuthenticateNormalization = ir.AccountUnicode16NFKC
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
