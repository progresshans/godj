package codegen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func bulkCreateProjectSpec(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	spec := customSinglePrefetchSpec()
	spec.Apps[1].Schema.Models[0].Fields[0].Unique = true
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, ir.Model{Name: "bulk_only", GoName: "BulkOnly"})
	scalar, err := schema.Build(schema.Definition{AppLabel: "owners", Models: []schema.Model{{Name: "bulk_scalar", GoName: "BulkScalar", Fields: []schema.Field{
		schema.TextField("name", "Name", schema.Unique()), schema.BooleanField("enabled", "Enabled", schema.Default(true)),
		schema.IntegerField("amount", "Amount", schema.Nullable()), schema.FloatField("ratio", "Ratio", schema.Nullable()),
		schema.DecimalField("cost", "Cost", 12, 2, schema.Nullable()), schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
		schema.TimeField("at", "At", schema.Nullable()), schema.DateField("day", "Day", schema.Nullable()),
		schema.DateTimeField("moment", "Moment", schema.Nullable()), schema.UUIDField("reference", "Reference", schema.Nullable()),
		schema.BinaryField("payload", "Payload", schema.Nullable()), schema.JSONField("document", "Document", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, scalar.Models...)
	return spec
}

func TestGeneratedBulkCreate(t *testing.T) {
	spec := bulkCreateProjectSpec(t)
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "single_object_test.go", "bulk_create_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestBulkCreate"}
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, name := range []string{"input_order_keys_defaults_and_cache", "all_input_prepared_before_insert", "all_batches_rollback", "ignore_unique_only", "update_target_and_native_duplicates", "composite_target_and_relations", "borrowed_scopes", "root_batch_affinity", "readonly_and_cancellation", "auto_key_only", "scalar_codecs", "concurrent_unique_arbitration", "concurrent_unique_arbitration/normal", "concurrent_unique_arbitration/ignore", "concurrent_unique_arbitration/update"} {
			required = append(required, "TestBulkCreate/"+backend+"/"+name)
		}
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			for _, finish := range []string{"commit", "rollback"} {
				required = append(required, "TestBulkCreate/"+backend+"/borrowed_scopes/"+mode+"/"+finish)
			}
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestBulkCreate$", "./consumer")
	output := runStrictGeneratedCommand(t, command)
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
			t.Fatal("unexpected generated package owner", event)
		}
		if event.Test == "" {
			continue
		}
		if event.Action == "run" {
			runs[event.Test]++
		}
		if event.Action == "pass" {
			passes[event.Test]++
		}
	}
	if !maps.Equal(runs, passes) {
		t.Fatal("generated bulk consumer did not finish every started test")
	}
	for name, count := range runs {
		if count != 1 {
			t.Fatal("generated bulk consumer repeated test", name, count)
		}
	}
	t.Logf("bulk native consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"foreign_input", `_,_ = api.LabelsLabel.BulkCreateInputs(ctx, []owners.OwnerCreate{owners.NewOwnerCreate("wrong")})`, "LabelCreate"},
		{"foreign_target", `_ = orm.BulkUpdateConflicts([]orm.BulkConflictField[labels.Label]{owners.OwnerFields.Name}, labels.LabelFields.Note)`, "BulkConflictField"},
		{"primary_update", `_ = orm.BulkUpdateConflicts([]orm.BulkConflictField[labels.Label]{labels.LabelFields.ID}, labels.LabelFields.ID)`, "WritableField"},
		{"related_target", `_ = orm.BulkUpdateConflicts([]orm.BulkConflictField[labels.Label]{orm.RelatedStringField[labels.Label]{}}, labels.LabelFields.Note)`, "BulkConflictField"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/compile.go", []byte(`package invalid
import("context";"example.com/godj-project-bundle/project";"example.com/godj-project-bundle/labels";"example.com/godj-project-bundle/owners";"github.com/progresshans/godj/orm")
func reject(){ctx:=context.Background();api,_:=project.Using(nil);_=ctx;_=api;_=labels.Label{};_=owners.Owner{};_=orm.BulkBatchSize[labels.Label](1)
`+invalid.expression+"\n}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			typeRejection := strings.Contains(string(output), "cannot use") || strings.Contains(string(output), "does not match inferred type")
			if err == nil || !typeRejection || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid bulk program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
