package codegen_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedBulkCreateReference(t *testing.T) {
	spec := bulkCreateProjectSpec(t)
	reference, err := schema.Build(schema.Definition{AppLabel: "owners", Models: []schema.Model{
		{Name: "bulk_reference_group", GoName: "BulkReferenceGroup", DBTable: "gdj_bulk_group", Fields: []schema.Field{schema.CharField("name", "Name", 32)}},
		{Name: "bulk_reference_item", GoName: "BulkReferenceItem", DBTable: "gdj_bulk_item", Fields: []schema.Field{
			schema.CharField("name", "Name", 32, schema.Unique()), schema.IntegerField("amount", "Amount", schema.Default(int64(0))),
			schema.CharField("note", "Note", 32, schema.Nullable()),
			schema.ForeignKey("group", "GroupID", schema.Target("owners", "bulk_reference_group"), schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, reference.Models...)
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "single_object_test.go", "bulk_create_test.go", "bulk_create_reference_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	cases := []string{"empty", "empty_invalid_conflicts", "empty_invalid_batch", "one_batch", "batched", "mixed_explicit_keys", "failure_later_batch_unique", "ignore_unique", "ignore_check", "ignore_not_null", "invalid_conflict_options", "update_missing_fields", "update_missing_target", "update_primary_key", "update_conflicts", "duplicate_upsert_one_batch", "duplicate_upsert_two_batches", "existing_fk", "missing_fk", "parent_rollback", "borrowed_failure", "no_model_clean", "filtered_query_ignores_read_shape"}
	observer, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/bulk_create_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(observer)
	required := []string{"TestBulkCreateReference"}
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "bulk_create-django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "bulkcreate", name))
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Kind, Django, Python, Backend string
			Observer                      string            `json:"observer_sha256"`
			Sources                       map[string]string `json:"source_sha256"`
			Cases                         []struct{ Case string }
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		vendor := backend
		if vendor == "postgres" {
			vendor = "postgresql"
		}
		if fixture.Kind != "django-bulk-create-reference-v1" || fixture.Django != "6.1" || fixture.Python != "3.14.3" || fixture.Backend != vendor || fixture.Observer != hex.EncodeToString(digest[:]) || len(fixture.Cases) != len(cases) {
			t.Fatal("bulk observer/provenance/case inventory changed", backend)
		}
		if !maps.Equal(fixture.Sources, map[string]string{
			"Atomic":            "aee995a682da7157a7ef415521e4d3336c25fd5a25c1b8582e2453c5f1d30a79",
			"QuerySet":          "5e86af673328a1d6800342f4bec9d4e2352b7919bebef349d98962d67569eae6",
			"SQLInsertCompiler": "38e5ed0147669e8085f00b02646e5b7436ece76f6bb399bef4acd4ae9f07feb4",
		}) {
			t.Fatal("bulk reference has foreign upstream source")
		}
		for index, caseName := range cases {
			if fixture.Cases[index].Case != caseName {
				t.Fatal("bulk reference case order changed", caseName)
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
		if backend != "postgres" || strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" || os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			for _, caseName := range cases {
				required = append(required, "TestBulkCreateReference/"+backend+"/"+caseName)
			}
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestBulkCreateReference$", "./consumer")
	output := runStrictGeneratedCommand(t, command)
	assertGeneratedConsumerTests(t, output, required...)
	t.Logf("bulk native reference: required=%d skips=0 log_sha256=%x", len(required), sha256.Sum256(output))
}
