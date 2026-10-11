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
)

// These cases are a named subset of the independently recorded 77-case
// annotation exploration. Other annotations and relation aggregates remain
// requirements; they are not claimed as supported by this consumer.
var derivedReferenceCases = []string{
	"annotate_after_slice", "filter_after_slice", "order_after_slice",
	"aggregate_annotation_alias", "aggregate_sliced_annotation_alias",
	"group_then_computed_aggregate", "group_then_computed_having",
	"computed_key_then_group", "group_key_retained_when_hidden",
}

func TestGeneratedDerivedResults(t *testing.T) {
	bundle, err := codegen.GenerateProject(computedExpressionProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, source := range []struct{ directory, name string }{
		{"expressioncommon", "backend_test.go"}, {"expressioncommon", "scalars_test.go"},
		{"derivedresult", "reference_test.go"}, {"derivedresult", "runtime_test.go"},
	} {
		data, err := os.ReadFile(filepath.Join("testdata", source.directory, source.name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+source.name, data)
	}
	source := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/annotation_reference")
	for name, want := range map[string]string{"observe.py": "671c01676e34630d7d1f0ece791974e142bc198faad2a96670ee6b75fbade297", "source-verification.json": "96b6203d6d72e0031d1dd17bbee2acdef14e53c40637731e112161aa67401f87"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != want {
			t.Fatal("independent annotation source changed", name)
		}
	}
	required := []string{"TestDerivedReference", "TestDerivedRuntime"}
	for _, vendor := range []string{"sqlite", "postgres"} {
		name := "django61-" + vendor + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "derivedresult", name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != map[string]string{"sqlite": "890be7473d897c94fcab00c2a80a212b19cbb53f9c300a7bf5295dfa6532cb5c", "postgres": "7776b22d1dc3fdfefb80868ecaccebc42d96b85eb1503c6c2f3ffecc073dfd5d"}[vendor] {
			t.Fatal("independent annotation raw bytes changed", vendor)
		}
		var fixture struct {
			Kind, Backend string
			Observer      string `json:"observer_sha256"`
			Sources       string `json:"source_verification_sha256"`
			Commit        string `json:"django_commit"`
			Unchanged     bool   `json:"storage_unchanged"`
			Cases         []struct{ Name string }
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		backend := vendor
		if backend == "postgres" {
			backend = "postgresql"
		}
		if fixture.Kind != "django-annotation-expression-exploratory-reference-v1" || fixture.Backend != backend || !fixture.Unchanged ||
			fixture.Observer != "671c01676e34630d7d1f0ece791974e142bc198faad2a96670ee6b75fbade297" || fixture.Sources != "96b6203d6d72e0031d1dd17bbee2acdef14e53c40637731e112161aa67401f87" ||
			fixture.Commit != "fe0a859f537d4238cf49fca39073513206f83122" || len(fixture.Cases) != 77 {
			t.Fatal("annotation provenance or full inventory differs")
		}
		names := map[string]bool{}
		for _, record := range fixture.Cases {
			if names[record.Name] {
				t.Fatal("duplicate annotation case", record.Name)
			}
			names[record.Name] = true
		}
		for _, name := range derivedReferenceCases {
			if !names[name] {
				t.Fatal("missing derived reference", name)
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
		if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestDerivedReference/"+vendor, "TestDerivedRuntime/"+vendor)
		for _, name := range derivedReferenceCases {
			required = append(required, "TestDerivedReference/"+vendor+"/"+name)
		}
		for _, name := range []string{"computed_group_ratio_and_page", "stage_order_and_reaggregation", "duration_mean_keeps_native_comparison", "nullable_scalar_codecs", "empty_and_zero_division", "native_overflow_and_statement_failure", "scope_and_context_rejected_before_io", "cache_cells_and_concurrent_copies", "cursor_failure_and_session_lifetime"} {
			required = append(required, "TestDerivedRuntime/"+vendor+"/"+name)
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestDerived(Reference|Runtime)$", "./consumer"))
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
			t.Fatal("foreign derived consumer package")
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
		t.Fatal("incomplete derived consumer inventory", len(runs), len(required))
	}
	for _, n := range runs {
		if n != 1 {
			t.Fatal("duplicate derived consumer execution")
		}
	}
	t.Logf("derived generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"result_in_model_assignment", `_ = orm.AssignExpression(f.Amount,total)`, "does not match"},
		{"row_in_result_projection", `_,_ = orm.SelectResults(stage,orm.Project1(f.Amount,func(n int64)int64{return n}))`, "does not match"},
		{"foreign_model_selection", `_ = orm.SelectedField(stage,records.OtherFields.Amount)`, "does not match"},
		{"optional_arithmetic", `_ = orm.Add(orm.SelectedAggregate(stage,orm.Sum(f.Amount)),int64(1))`, "does not match"},
		{"earlier_stage_selection", `rows,_:=orm.SelectResults(stage,orm.Project1(total,func(n int64)int64{return n}));next,_:=orm.Derive(rows);_,_=orm.SelectResults(next,orm.Project1(total,func(n int64)int64{return n}))`, "does not match"},
		{"result_update", `rows,_:=orm.SelectResults(stage,orm.Project1(total,func(n int64)int64{return n}));_,_=rows.Update(context.Background())`, "Update undefined"},
		{"result_delete", `rows,_:=orm.SelectResults(stage,orm.Project1(total,func(n int64)int64{return n}));_,_=rows.Delete(context.Background())`, "Delete undefined"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			program := `package invalid
import("context";"example.com/godj-project-bundle/records";"example.com/godj-project-bundle/project";"github.com/progresshans/godj/orm")
func reject(){_=context.Background();f:=records.MeasureFields;api,_:=project.Using(nil);grouped,_:=project.GroupRecordsMeasureBy(api.RecordsMeasure,orm.Project1(f.GroupKey,func(s *string)*string{return s}),orm.Aggregate1(orm.CountRows[records.Measure](),func(n int64)int64{return n}),func(_ *string,n int64)int64{return n});stage,_:=orm.Derive(grouped);total:=orm.SelectedAggregate(stage,orm.CountRows[records.Measure]());_=total;
` + invalid.expression + "\n}\n"
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid derived program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
