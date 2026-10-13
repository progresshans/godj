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
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func relatedExpressionProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	app, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "group", GoName: "Group", DBTable: "gdj_related_group", Fields: []schema.Field{schema.IntegerField("limit", "Limit")}},
		{Name: "datum", GoName: "Datum", DBTable: "gdj_related_datum", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.IntegerField("amount", "Amount", schema.Nullable()), schema.BooleanField("flag", "Flag"),
			schema.FloatField("score", "Score", schema.Nullable()), schema.DecimalField("price", "Price", 14, 2, schema.Nullable()), schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
			schema.ForeignKey("group", "GroupID", schema.Target("records", "group"), schema.RelatedName("data"), schema.SetNull, schema.Nullable()),
		}},
		{Name: "measure", GoName: "Measure", DBTable: "gdj_related_measure", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.IntegerField("amount", "Amount"), schema.IntegerField("other", "Other", schema.Nullable()), schema.BooleanField("enabled", "Enabled"),
			schema.ForeignKey("datum", "DatumID", schema.Target("records", "datum"), schema.RelatedName("measures"), schema.SetNull, schema.Nullable()),
		}},
		{Name: "detail", GoName: "Detail", DBTable: "gdj_related_detail", Fields: []schema.Field{
			schema.OneToOne("measure", "MeasureID", schema.Target("records", "measure"), schema.RelatedName("detail"), schema.Cascade),
			schema.IntegerField("bonus", "Bonus"), schema.BooleanField("enabled", "Enabled"),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"},
		Apps: []codegen.AppSpec{{Alias: "records", Package: codegen.PackageSpec{PackageName: "records", ImportPath: "example.com/godj-project-bundle/records", Directory: "records"}, Schema: app}}}
}

var relatedReferenceCases = []string{
	"forward_add", "forward_chain_add", "rhs_exact", "rhs_ordered", "rhs_not", "related_both_sides", "predicate_or_missing",
	"case_rhs_related", "case_related_predicate", "reverse_add", "mixed_route", "related_group", "filtered_count", "order_slice", "distinct_computed", "aggregate_computed", "related_codecs",
}

func TestGeneratedRelatedExpressions(t *testing.T) {
	bundle, err := codegen.GenerateProject(relatedExpressionProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "observations_test.go", "runtime_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "relatedexpression", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	profile := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/related_expression_reference")
	hashes := map[string]string{}
	for _, name := range []string{"observe.py", "input.json", "source-verification.json"} {
		data, err := os.ReadFile(filepath.Join(profile, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(digest[:])
		if name == "input.json" {
			writeGeneratedTestFile(t, root, "consumer/input.json", data)
		}
	}
	// The three PostgreSQL consumer owners run the pinned independent observer
	// before this test. Other platforms run the invariant suite without claiming
	// a reference execution or introducing a passing empty reference test.
	references := os.Getenv("GODJ_RELATED_EXPRESSION_REFERENCE_DIR")
	if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" && references == "" {
		t.Fatal("native consumer owner lacks its current Django reference")
	}
	if references != "" {
		data, err := os.ReadFile(filepath.Join("testdata", "relatedexpression", "reference_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/reference_test.go", data)
		for _, vendor := range []string{"sqlite", "postgres"} {
			data, err := os.ReadFile(filepath.Join(references, "django61-"+vendor+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				Kind, Backend string
				Commit        string `json:"django_commit"`
				Observer      string `json:"observer_sha256"`
				Input         string `json:"input_sha256"`
				Sources       string `json:"source_verification_sha256"`
				Unchanged     bool   `json:"storage_unchanged"`
				Cleaned       bool   `json:"storage_cleaned"`
				Cases         []struct {
					Name  string
					Error json.RawMessage
				}
			}
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if document.Kind != "django-related-expression-reference-v1" || document.Backend != vendor || document.Commit != "fe0a859f537d4238cf49fca39073513206f83122" ||
				document.Observer != hashes["observe.py"] || document.Input != hashes["input.json"] || document.Sources != hashes["source-verification.json"] || !document.Unchanged || !document.Cleaned {
				t.Fatal("related reference is not bound to the current observer/input/source or its cleanup", vendor)
			}
			var names []string
			for _, record := range document.Cases {
				if len(record.Error) != 0 {
					t.Fatal("independent observer case failed", vendor, record.Name)
				}
				names = append(names, record.Name)
			}
			if !slices.Equal(names, relatedReferenceCases) {
				t.Fatal("related reference inventory differs", vendor, names)
			}
			writeGeneratedTestFile(t, root, "consumer/django61-"+vendor+".json", data)
			t.Logf("related reference %s: sha256=%x observer=%s input=%s sources=%s cases=%d", vendor, sha256.Sum256(data), document.Observer, document.Input, document.Sources, len(names))
		}
	}
	required := []string{"TestRelatedRuntime"}
	if references != "" {
		required = append(required, "TestRelatedReference")
	}
	for _, vendor := range []string{"sqlite", "postgres"} {
		if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestRelatedRuntime/"+vendor)
		for _, name := range []string{"typed_dynamic_and_nulls", "related_results_and_join_ownership", "result_scopes_and_page", "source_assignment_and_input_rejection", "cache_context_and_cursor_failure", "physical_target_domains_and_native_errors"} {
			required = append(required, "TestRelatedRuntime/"+vendor+"/"+name)
		}
		if references != "" {
			required = append(required, "TestRelatedReference/"+vendor)
			for _, name := range relatedReferenceCases {
				required = append(required, "TestRelatedReference/"+vendor+"/"+name)
			}
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestRelated(Reference|Runtime)$", "./consumer"))
	if references != "" {
		// Retain the untruncated native child events alongside this owner's
		// independent reference. A later audit can check each run/pass itself.
		file, err := os.OpenFile(filepath.Join(references, "godj-related-native.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(output)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot preserve related native events", writeErr, closeErr)
		}
	}
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
			t.Fatal("foreign related consumer package")
		}
		if event.Test != "" {
			if event.Action == "run" {
				runs[event.Test]++
			}
			if event.Action == "pass" {
				passes[event.Test]++
			}
		}
	}
	if !maps.Equal(runs, passes) || len(runs) != len(required) {
		t.Fatal("incomplete related consumer inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate related consumer execution")
		}
	}
	t.Logf("related generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"foreign_model_comparison", `_ = orm.ExactExpressions(orm.F(records.MeasureFields.Amount), orm.F(records.DetailFields.Bonus))`, "does not match"},
		{"mixed_value_comparison", `_ = orm.ExactExpressions(orm.F(records.MeasureFields.Amount), relations.RecordsMeasure.Datum.Flag)`, "does not match"},
		{"foreign_related_operand", `_ = orm.AddExpressions(orm.F(records.MeasureFields.Amount), relations.RecordsDatum.Group.Limit)`, "does not match"},
		{"related_update_target", `_ = orm.AssignExpression(relations.RecordsMeasure.Datum.Amount, orm.F(records.MeasureFields.Amount))`, "orm.ReferenceField"},
		{"wrong_related_projection", `_ = orm.Project1(orm.Scalar(relations.RecordsMeasure.Datum.Amount),func(value int64)int64{return value})`, "does not match"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			program := "package invalid\nimport(\"example.com/godj-project-bundle/records\";\"example.com/godj-project-bundle/project\";\"github.com/progresshans/godj/orm\")\nfunc reject(){ relations,_:=project.BindRelations(); _=relations; _=records.MeasureFields;" + invalid.expression + "\n}\n"
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid related program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
