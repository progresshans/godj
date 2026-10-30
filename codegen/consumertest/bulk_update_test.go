package codegen_test

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func TestGeneratedBulkUpdate(t *testing.T) {
	bundle, err := codegen.GenerateProject(bulkCreateProjectSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "single_object_test.go", "bulk_update_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestBulkUpdate"}
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, name := range []string{"selected_fields_cache_and_filtered_counts", "duplicate_missing_and_batch_counts", "all_inputs_prepared_before_update", "all_batches_rollback_on_unique_and_late_failure", "all_batches_rollback_on_unique_and_late_failure/unique", "all_batches_rollback_on_unique_and_late_failure/after_write", "relation_filters_typed_dynamic_and_collection_scope", "eager_prefetch_and_read_lock_shapes", "empty_sliced_and_dynamic_validation", "borrowed_scopes", "root_cursor_affinity", "readonly_and_cancellation", "scalar_codecs", "native_large_batch", "two_connection_atomicity"} {
			required = append(required, "TestBulkUpdate/"+backend+"/"+name)
		}
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			for _, finish := range []string{"commit", "rollback"} {
				required = append(required, "TestBulkUpdate/"+backend+"/borrowed_scopes/"+mode+"/"+finish)
			}
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestBulkUpdate$", "./consumer"))
	assertGeneratedConsumerTests(t, output, required...)
	t.Logf("bulk update native consumer: required=%d skips=0 log_sha256=%x", len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"foreign_input", `_,_ = api.LabelsLabel.BulkUpdate(ctx, []owners.Owner{}, orm.BulkUpdateFields(labels.LabelFields.Note))`, "labels.Label"},
		{"foreign_mask", `_,_ = api.LabelsLabel.BulkUpdate(ctx, []labels.Label{}, orm.BulkUpdateFields(owners.OwnerFields.Name))`, "BulkUpdateOption"},
		{"primary_field", `_ = orm.BulkUpdateFields[labels.Label](labels.LabelFields.ID)`, "WritableField"},
		{"related_field", `_ = orm.BulkUpdateFields[labels.Label](orm.RelatedStringField[labels.Label]{})`, "WritableField"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/compile.go", []byte(`package invalid
import("context";"example.com/godj-project-bundle/project";"example.com/godj-project-bundle/labels";"example.com/godj-project-bundle/owners";"github.com/progresshans/godj/orm")
func reject(){ctx:=context.Background();api,_:=project.Using(nil);_=ctx;_=api;_=labels.Label{};_=owners.Owner{};_=orm.BulkUpdateBatchSize[labels.Label](1)
`+invalid.expression+"\n}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !(strings.Contains(string(output), "cannot use") || strings.Contains(string(output), "does not match inferred type")) || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid bulk update program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
