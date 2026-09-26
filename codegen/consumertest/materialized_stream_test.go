package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func TestGeneratedMaterializedStream(t *testing.T) {
	bundle, err := codegen.GenerateProject(customSinglePrefetchSpec())
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "sessions_test.go", "queries_test.go", "prefetch_test.go", "prefetch_tree_test.go", "prefetch_filtered_test.go", "prefetch_stream_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "many-to-many-django61-sqlite.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/django-query.json", data)
	var required []string
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, name := range []string{"chunk_1", "chunk_2", "chunk_3", "chunk_8", "invalid_zero", "invalid_negative", "early_stop", "callback_error", "nested_filtered", "owner_scope_1", "owner_scope_2", "warm_cache", "transaction", "affinity_origin_writes", "eager_reverse_and_source_slice", "paths_duplicates_and_cache", "session_variants", "failure_cancel_panic", "cross_batch_cardinality", "nested_stream", "concurrent_contexts", "unsupported_and_empty"} {
			required = append(required, "TestMaterializedStream/"+backend+"/"+name)
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-run", "^TestMaterializedStream$", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
