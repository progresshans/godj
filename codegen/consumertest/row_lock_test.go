package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func TestGeneratedRowLocking(t *testing.T) {
	bundle, err := codegen.GenerateProject(customSinglePrefetchSpec())
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "row_lock_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestGeneratedRowLocks"}
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, name := range []string{"root_cache_count_and_errors", "typed_dynamic_targets", "terminals_and_scope_lifetime", "default_prefetch_and_custom_locks", "eager_cache_cannot_replace_lock", "locked_refresh_preserves_current_descendants", "actual_generated_contention"} {
			required = append(required, "TestGeneratedRowLocks/"+backend+"/"+name)
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-run", "^TestGeneratedRowLocks$", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
	writeGeneratedTestFile(t, root, "wronglock/compile.go", []byte(`package wronglock
import (
 "example.com/godj-project-bundle/project"
 "github.com/progresshans/godj/orm"
)
func reject() {
 api, _ := project.Using(nil)
 _ = api.OwnersOwner.SelectForUpdate(orm.RowLockOptions{}, api.LabelsLabel.LockTarget())
}
`))
	output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./wronglock").CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot use") || !strings.Contains(string(output), "RowLockTarget") {
		t.Fatalf("foreign model lock target compiled or failed elsewhere: %v\n%s", err, output)
	}
}
