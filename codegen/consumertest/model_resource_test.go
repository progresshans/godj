package codegen_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

// The exact same consumer source runs against two independently generated
// cross-app projects. Only the model declaration and explicit surface policy
// change: no DTO, setter, Form conversion or CRUD implementation is regenerated
// by this test. Generated model/facade files are the generator's responsibility.
func TestGeneratedModelResources(t *testing.T) {
	for _, evolved := range []bool{false, true} {
		name := "baseline"
		if evolved {
			name = "changed"
		}
		t.Run(name, func(t *testing.T) {
			bundle, err := codegen.GenerateProject(modelResourceProject(t, evolved))
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			for _, name := range []string{"backend_test.go", "assembly_test.go", "runtime_test.go", "http_test.go", "admin_test.go", "comparison_test.go"} {
				data, err := os.ReadFile(filepath.Join("testdata", "modelresource", name))
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, "consumer/"+name, data)
			}
			manual, err := os.ReadFile(filepath.Join("testdata", "modelresource", "manual_"+name+".go.txt"))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/manual_test.go", manual)
			writeGeneratedTestFile(t, root, "consumer/version_test.go", []byte(fmt.Sprintf("package consumer\nconst evolved = %t\n", evolved)))
			required := []string{"TestModelResourceAssembly"}
			for _, vendor := range []string{"sqlite", "postgres"} {
				if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
					continue
				}
				prefix := "TestModelResourceAssembly/" + vendor
				required = append(required, prefix)
				for _, name := range []string{"form_json_defaults_and_fresh_update", "source_exposure_and_target_rejection", "scope_uniqueness_rollback_and_cross_app_delete", "http_permission_response_and_schema", "admin_form_permission_and_csrf", "same_change_manual_and_model_assembly"} {
					required = append(required, prefix+"/"+name)
				}
			}
			output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=3m", "-run", "^TestModelResourceAssembly$", "./consumer"))
			assertGeneratedConsumerTests(t, output, required...)
			// Keep the original child events in the owner's CI log, including each
			// backend/case, instead of replacing execution evidence with a count.
			t.Logf("model resource child events (sha256=%x):\n%s", sha256.Sum256(output), output)
			for _, invalid := range []struct{ name, expression, diagnostic string }{
				{"foreign_input", `_, _ = resource.CreateInSession(context.Background(), session, input, crud.SaveOptions[inventory.Asset]{})`, "cannot use input"},
				{"foreign_form_manager", `_, _ = formmodel.PrepareInput[inventory.Asset](directory.WorkspaceObjects, formmodel.BoundForm{})`, "cannot use directory.WorkspaceObjects"},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					program := `package invalid
import (
 "context"
 "example.com/godj-project-bundle/directory"
 "example.com/godj-project-bundle/inventory"
 "github.com/progresshans/godj/crud"
 "github.com/progresshans/godj/db"
 formmodel "github.com/progresshans/godj/forms/model"
)
func reject(session db.Session) {
 resource, _ := crud.New(inventory.AssetObjects, crud.Config{Fields: []string{"code"}})
 input, _ := directory.WorkspaceObjects.Input(nil)
 _ = resource; _ = input; _ = context.Background(); _ = formmodel.BoundForm{}
` + invalid.expression + "\n}\n"
					writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
					output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
					if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
						t.Fatalf("invalid model resource consumer compiled or failed elsewhere: %v\n%s", err, output)
					}
				})
			}
		})
	}
}

func modelResourceProject(t *testing.T, evolved bool) codegen.ProjectSpec {
	t.Helper()
	directory, err := schema.Build(schema.Definition{AppLabel: "directory", Models: []schema.Model{{
		Name: "workspace", GoName: "Workspace", DBTable: "gdj_resource_workspace", Fields: []schema.Field{
			schema.CharField("name", "Name", 32, schema.Unique()),
			schema.BooleanField("writable", "Writable", schema.Default(true)),
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	fields := []schema.Field{
		schema.CharField("code", "Code", 12, schema.Unique()),
		schema.BooleanField("enabled", "Enabled", schema.Default(true)),
		schema.TextField("note", "Note", schema.Nullable(), schema.Blank()),
		schema.CharField("stamp", "Stamp", 24, schema.Default("server"), schema.Editable(false)),
		schema.ForeignKey("workspace", "WorkspaceID", schema.Target("directory", "workspace"), schema.RelatedName("assets"), schema.Protect),
	}
	if evolved {
		fields = append(fields, schema.IntegerField("priority", "Priority", schema.Default(int64(5)),
			schema.Choices(schema.Choice(int64(1), "Low"), schema.Choice(int64(5), "Normal"), schema.Choice(int64(9), "High"))))
	}
	inventory, err := schema.Build(schema.Definition{AppLabel: "inventory", Models: []schema.Model{{Name: "asset", GoName: "Asset", DBTable: "gdj_resource_asset", Fields: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{
		{Alias: "directory", Package: codegen.PackageSpec{PackageName: "directory", ImportPath: "example.com/godj-project-bundle/directory", Directory: "directory"}, Schema: directory},
		{Alias: "inventory", Package: codegen.PackageSpec{PackageName: "inventory", ImportPath: "example.com/godj-project-bundle/inventory", Directory: "inventory"}, Schema: inventory},
	}}
}
