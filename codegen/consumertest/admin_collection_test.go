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
	"github.com/progresshans/godj/schema/ir"
)

// Both projects use the same external Admin writer. The evolved version changes
// the IR field/choices and explicit permission, not a second form or save DTO.
func TestGeneratedAdminCollections(t *testing.T) {
	for _, evolved := range []bool{false, true} {
		version := "baseline"
		if evolved {
			version = "changed"
		}
		t.Run(version, func(t *testing.T) {
			bundle, err := codegen.GenerateProject(adminCollectionProject(t, evolved))
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			for _, file := range []struct{ folder, name string }{{"modelresource", "backend_test.go"}, {"admincollection", "runtime_test.go"}} {
				data, err := os.ReadFile(filepath.Join("testdata", file.folder, file.name))
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, "consumer/"+file.name, data)
			}
			writeGeneratedTestFile(t, root, "consumer/version_test.go", []byte(fmt.Sprintf("package consumer\nconst evolved = %t\n", evolved)))
			required := []string{"TestAdminCollectionConsumer"}
			for _, vendor := range []string{"sqlite", "postgres"} {
				if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
					continue
				}
				prefix := "TestAdminCollectionConsumer/" + vendor
				required = append(required, prefix)
				for _, scenario := range []string{"mixed_current_and_files", "readonly_existing_new_file", "clear_and_preserve", "late_audit_rollback_keeps_files", "protected_delete_rolls_back_and_keeps_files", "partial_file_publication", "unknown_file_publication", "commit_unknown_keeps_native_state", "rollback_unknown_preserves_failure", "post_commit_cancellation", "current_scope_denial", "late_cohort_gain", "stale_revision", "readonly_stale_revision", "deleted_stale_revision", "reordered_identities", "foreign_identity", "cohort_overflow", "changed_policy"} {
					required = append(required, prefix+"/"+scenario)
				}
			}
			output := runStrictGeneratedCommandWithCapture(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=3m", "-run", "^TestAdminCollectionConsumer$", "./consumer"), func(events []byte) error {
				t.Logf("admin collection child events (sha256=%x):\n%s", sha256.Sum256(events), events)
				return nil
			})
			assertGeneratedConsumerTests(t, output, required...)
			for _, invalid := range []struct{ name, expression, diagnostic string }{
				{"foreign_current_loader", `_, _ = declaration.BindCurrent(context.Background(), auth.Anonymous(), admin.CollectionSetSubmission{}, func(context.Context, admin.CollectionSetAccess) (admin.CollectionSetCurrent[directory.Workspace], error) { return admin.CollectionSetCurrent[directory.Workspace]{}, nil })`, "cannot use"},
				{"foreign_file_saver", `prepared, _ := bound.Set().Prepare(); _, _, _ = prepared.SaveFiles(context.Background(), func(formmodel.PreparedSetRow[inventory.Asset]) ([]formmodel.FileSaver[directory.Workspace], error) { return nil, nil })`, "cannot use"},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					program := `package invalid
import (
 "context"
 "example.com/godj-project-bundle/directory"
 "example.com/godj-project-bundle/inventory"
 "github.com/progresshans/godj/admin"
 "github.com/progresshans/godj/auth"
 formmodel "github.com/progresshans/godj/forms/model"
)
func reject(declaration admin.ModelCollectionFormSet[inventory.Asset], bound admin.BoundCollectionFormSet[inventory.Asset]) {
 _ = context.Background(); _ = auth.Anonymous(); _ = formmodel.PreparedSetRow[inventory.Asset]{}
` + invalid.expression + "\n}\n"
					writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
					output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
					if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
						t.Fatalf("foreign typed collection input compiled or failed elsewhere: %v\n%s", err, output)
					}
				})
			}
		})
	}
}

func adminCollectionProject(t *testing.T, evolved bool) codegen.ProjectSpec {
	t.Helper()
	project := modelResourceProject(t, evolved)
	extensions, err := schema.Build(schema.Definition{AppLabel: "inventory", Models: []schema.Model{
		{Name: "asset", GoName: "Asset", DBTable: "gdj_resource_asset", Fields: []schema.Field{
			schema.FileField("attachment", "Attachment", schema.MaxLength(100), schema.Blank(), schema.Default("")),
			schema.IntegerField("revision", "Revision", schema.Default(int64(1))),
		}},
		{Name: "hold", GoName: "Hold", DBTable: "gdj_resource_hold", Fields: []schema.Field{schema.ForeignKey("asset", "AssetID", schema.Target("inventory", "asset"), schema.RelatedName("holds"), schema.Protect)}},
		{Name: "audit", GoName: "Audit", DBTable: "gdj_resource_audit", Fields: []schema.Field{
			schema.CharField("action", "Action", 8), schema.IntegerField("asset_key", "AssetKey"), schema.CharField("actor", "Actor", 32), schema.IntegerField("revision", "Revision"),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range extensions.Models {
		if model.Name == "asset" {
			for _, field := range model.Fields {
				if !field.PrimaryKey {
					project.Apps[1].Schema.Models[0].Fields = append(project.Apps[1].Schema.Models[0].Fields, field)
				}
			}
		} else {
			project.Apps[1].Schema.Models = append(project.Apps[1].Schema.Models, model)
		}
	}
	project.Apps[1].Schema, err = ir.Normalize(project.Apps[1].Schema)
	if err != nil {
		t.Fatal(err)
	}
	return project
}
