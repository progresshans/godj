package codegen_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func TestGeneratedAdminInlines(t *testing.T) {
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
			for _, file := range []struct{ directory, name string }{{"modelresource", "backend_test.go"}, {"admininline", "runtime_test.go"}} {
				data, err := os.ReadFile(filepath.Join("testdata", file.directory, file.name))
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, "consumer/"+file.name, data)
			}
			writeGeneratedTestFile(t, root, "consumer/version_test.go", []byte(fmt.Sprintf("package consumer\nconst evolved = %t\n", evolved)))
			required := []string{"TestAdminInlineConsumer"}
			for _, vendor := range []string{"sqlite", "postgres"} {
				if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
					continue
				}
				prefix := "TestAdminInlineConsumer/" + vendor
				required = append(required, prefix)
				for _, scenario := range []string{"current_values_and_changed_model", "pending_parent_and_delete", "late_failure_rolls_back", "current_scope_denial", "foreign_parent_and_cohort", "read_only_initial_and_new_rows"} {
					required = append(required, prefix+"/"+scenario)
				}
			}
			output := runStrictGeneratedCommandWithCapture(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=3m", "-run", "^TestAdminInlineConsumer$", "./consumer"), func(events []byte) error {
				// Retain original child events on failures as well as success. A
				// diagnostic summary must not erase the failing native scenario.
				t.Logf("admin inline child events (sha256=%x):\n%s", sha256.Sum256(events), events)
				return nil
			})
			assertGeneratedConsumerTests(t, output, required...)
			for _, invalid := range []struct{ name, expression, diagnostic string }{
				{"foreign_current_loader", `_, _, _ = inline.BindCurrent(context.Background(), auth.Anonymous(), admin.InlineSubmission{}, func(context.Context, admin.InlineAccess) (admin.InlineCurrent[inventory.Asset, directory.Workspace], error) { return admin.InlineCurrent[inventory.Asset, directory.Workspace]{}, nil })`, "cannot use"},
				{"foreign_saved_parent", `_, _ = bound.Set().PrepareWithParent(inventory.Asset{})`, "cannot use inventory.Asset"},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					program := `package invalid
import (
 "context"
 "example.com/godj-project-bundle/directory"
 "example.com/godj-project-bundle/inventory"
 "github.com/progresshans/godj/admin"
 "github.com/progresshans/godj/auth"
)
func reject(inline admin.ModelInline[directory.Workspace, inventory.Asset], bound admin.BoundInline[directory.Workspace, inventory.Asset]) {
 _ = context.Background(); _ = auth.Anonymous()
` + invalid.expression + "\n}\n"
					writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
					output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
					if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
						t.Fatalf("invalid typed inline consumer compiled or failed elsewhere: %v\n%s", err, output)
					}
				})
			}
		})
	}
}
