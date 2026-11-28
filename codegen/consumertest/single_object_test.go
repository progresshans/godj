package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func TestGeneratedSingleObjectCreation(t *testing.T) {
	spec := customSinglePrefetchSpec()
	// The creation race is arbitrated by an actual unique database constraint.
	spec.Apps[1].Schema.Models[0].Fields[0].Unique = true
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "single_object_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestSingleObjectCreation"}
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, name := range []string{"fresh_cache_cardinality_dynamic", "slice_and_empty", "existing_graphs", "created_graphs", "explicit_input_and_lazy_validation", "relation_query_creation", "borrowed_scopes", "post_commit_cancellation", "root_batch_affinity"} {
			required = append(required, "TestSingleObjectCreation/"+backend+"/"+name)
		}
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			for _, finish := range []string{"commit", "rollback"} {
				required = append(required, "TestSingleObjectCreation/"+backend+"/borrowed_scopes/"+mode+"/"+finish)
			}
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-run", "^TestSingleObjectCreation$", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
