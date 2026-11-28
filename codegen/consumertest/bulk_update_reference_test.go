package codegen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedBulkUpdateReference(t *testing.T) {
	spec := bulkCreateProjectSpec(t)
	reference, err := schema.Build(schema.Definition{AppLabel: "owners", Models: []schema.Model{
		{Name: "bulk_reference_group", GoName: "BulkReferenceGroup", DBTable: "gdj_bulk_group", Fields: []schema.Field{schema.CharField("name", "Name", 32)}},
		{Name: "bulk_reference_item", GoName: "BulkReferenceItem", DBTable: "gdj_bulk_item", Fields: []schema.Field{
			schema.CharField("name", "Name", 32, schema.Unique()), schema.IntegerField("amount", "Amount", schema.Default(int64(0))),
			schema.CharField("note", "Note", 32, schema.Nullable()),
			schema.ForeignKey("group", "GroupID", schema.Target("owners", "bulk_reference_group"), schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("peers", "Peers", schema.Target("owners", "bulk_reference_item"), schema.NoReverse(), schema.Directed(), schema.Through(schema.Target("owners", "bulk_reference_peer"), "source", "target"))}},
		{Name: "bulk_reference_peer", GoName: "BulkReferencePeer", DBTable: "gdj_bulk_peer", Fields: []schema.Field{
			schema.ForeignKey("source", "SourceID", schema.Target("owners", "bulk_reference_item"), schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("target", "TargetID", schema.Target("owners", "bulk_reference_item"), schema.NoReverse(), schema.Cascade),
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
	for _, name := range []string{"consumer_test.go", "single_object_test.go", "bulk_create_test.go", "bulk_create_reference_test.go", "bulk_update_test.go", "bulk_update_reference_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	cases := []string{"empty", "empty_missing_fields", "empty_unknown_field", "empty_primary_key", "empty_invalid_batch", "missing_primary_key", "primary_key_field", "unknown_field", "many_to_many_field", "one_batch_selected_fields", "two_fields_and_null", "batched", "duplicate_field_names", "duplicate_keys_one_batch", "duplicate_keys_two_batches", "missing_key_count", "all_missing", "unchanged_value_count", "large_zero_negative_keys", "query_filter", "forward_filter", "many_to_many_filter", "boolean_filter", "empty_query_filter", "ordering_and_distinct", "sliced", "sliced_empty_input", "row_lock_read_shape", "eager_and_prefetch_read_shape", "cached_query", "failure_later_unique", "failure_later_check", "failure_later_not_null", "unselected_invalid_value", "selected_foreign_key", "missing_foreign_key", "unsaved_related_object", "parent_commit", "parent_rollback", "borrowed_failure"}
	observer, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/bulk_update_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(observer)
	required := []string{"TestBulkUpdateReference"}
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "bulk_update-django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "bulkupdate", name))
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
		if fixture.Kind != "django-bulk-update-reference-v1" || fixture.Django != "6.1" || fixture.Python != "3.14.3" || fixture.Backend != vendor || fixture.Observer != hex.EncodeToString(digest[:]) || len(fixture.Cases) != len(cases) {
			t.Fatal("bulk update reference provenance/inventory changed", backend)
		}
		if !maps.Equal(fixture.Sources, map[string]string{
			"Atomic":            "aee995a682da7157a7ef415521e4d3336c25fd5a25c1b8582e2453c5f1d30a79",
			"QuerySet":          "5e86af673328a1d6800342f4bec9d4e2352b7919bebef349d98962d67569eae6",
			"SQLUpdateCompiler": "38e5ed0147669e8085f00b02646e5b7436ece76f6bb399bef4acd4ae9f07feb4",
		}) {
			t.Fatal("bulk update fixture has foreign upstream source")
		}
		for index, caseName := range cases {
			if fixture.Cases[index].Case != caseName {
				t.Fatal("bulk update reference case order changed", caseName)
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
		if backend != "postgres" || strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" || os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			required = append(required, "TestBulkUpdateReference/"+backend)
			for _, caseName := range cases {
				required = append(required, "TestBulkUpdateReference/"+backend+"/"+caseName)
			}
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestBulkUpdateReference$", "./consumer"))
	assertGeneratedConsumerTests(t, output, required...)
	runs, passes := map[string]int{}, map[string]int{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var event struct{ Action, Package, Test string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event.Package != "example.com/godj-project-bundle/consumer" {
			t.Fatal("foreign generated reference package", event)
		}
		if event.Test != "" && event.Action == "run" {
			runs[event.Test]++
		}
		if event.Test != "" && event.Action == "pass" {
			passes[event.Test]++
		}
	}
	if !maps.Equal(runs, passes) || len(runs) != len(required) {
		t.Fatal("bulk update reference did not execute exactly its complete inventory")
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("bulk update reference repeated a test")
		}
	}
	t.Logf("bulk update native reference: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"null_nonnullable", `value.Name = nil`, "string"},
		{"unsaved_related_object", `value.GroupID = owners.BulkReferenceGroup{Name:"not saved"}`, "*int64"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/compile.go", []byte("package invalid\nimport \"example.com/godj-project-bundle/owners\"\nfunc invalidInput(){value:=owners.NewBulkReferenceItemWithID(1);"+invalid.expression+";_=value}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "cannot use") || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid reference input compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
