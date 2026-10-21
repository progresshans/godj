package codegen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func TestGeneratedNamedAnnotations(t *testing.T) {
	for _, variant := range []string{"scalars", "collections"} {
		t.Run(variant, func(t *testing.T) {
			spec := computedExpressionProject(t)
			backend, rootTest := "expressioncommon", "TestNamedScalarRuntime"
			cases := []string{"model_rows_and_typed_dynamic_projection", "rebind_alias_and_projection_ownership", "grouping_page_and_explicit_stage", "nullable_decimal_and_duration_codecs", "cache_failure_and_session_lifetime", "concurrent_copies_and_pre_io_errors"}
			if variant == "collections" {
				spec = collectionAggregateProject(t)
				backend, rootTest = "collectionaggregate", "TestNamedCollectionRuntime"
				cases = []string{"definition_time_join_and_multiple_collections", "relation_names_and_project_authority", "aggregate_dependencies_filters_and_nulls", "model_relation_wrappers_and_storage"}
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			for _, file := range []struct{ from, to string }{
				{filepath.Join("testdata", backend, "backend_test.go"), "consumer/backend_test.go"},
				{filepath.Join("testdata", "namedannotation", variant+"_test.go"), "consumer/named_test.go"},
			} {
				data, err := os.ReadFile(file.from)
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, file.to, data)
			}
			required := []string{rootTest}
			for _, vendor := range []string{"sqlite", "postgres"} {
				if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
					continue
				}
				required = append(required, rootTest+"/"+vendor)
				for _, name := range cases {
					required = append(required, rootTest+"/"+vendor+"/"+name)
				}
			}
			output := runStrictGeneratedCommandWithCapture(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^"+rootTest+"$", "./consumer"), func(output []byte) error {
				directory := os.Getenv("GODJ_NAMED_ANNOTATION_CAPTURE_DIR")
				if directory == "" {
					return nil
				}
				if err := os.MkdirAll(directory, 0700); err != nil {
					return err
				}
				file, err := os.OpenFile(filepath.Join(directory, variant+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return err
				}
				_, writeErr := file.Write(output)
				return errors.Join(writeErr, file.Close())
			})
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
					t.Fatal("foreign named consumer package")
				}
				if event.Test != "" {
					switch event.Action {
					case "run":
						runs[event.Test]++
					case "pass":
						passes[event.Test]++
					}
				}
			}
			if !maps.Equal(runs, passes) || len(runs) != len(required) {
				t.Fatal("incomplete named consumer execution", len(runs), len(passes), len(required))
			}
			for _, count := range runs {
				if count != 1 {
					t.Fatal("duplicate named consumer execution")
				}
			}
			t.Logf("named %s consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", variant, len(runs), len(passes), len(required), sha256.Sum256(output))
			if variant == "scalars" {
				checkNamedCompileFailures(t, root)
			}
		})
	}
}

func checkNamedCompileFailures(t *testing.T, root string) {
	t.Helper()
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"foreign_model_source", `_,_,_=project.AnnotateRecordsMeasure(api.RecordsOther,"bad",f.Amount)`, "does not implement"},
		{"foreign_model_operand", `_,_,_=project.AnnotateRecordsMeasure(api.RecordsMeasure,"bad",records.OtherFields.Amount)`, "does not match"},
		{"optional_name_arithmetic", `_,_,_=project.AnnotateRecordsMeasure(named,"bad",orm.Add(column,int64(1)))`, "does not match"},
		{"foreign_model_row", `var row orm.NamedRow[records.Other,records.Other];_,_=column.Get(row)`, "does not implement"},
		{"named_update", `_,_=named.Update(context.Background())`, "Update undefined"},
		{"named_delete", `_,_=named.Delete(context.Background())`, "Delete undefined"},
		{"stage_operand_in_model", `stage,_:=named.Derive();selected:=orm.SelectedField(stage,column);_,_,_=project.AnnotateRecordsMeasure(named,"bad",selected)`, "does not match"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			program := `package invalid
import("context";"example.com/godj-project-bundle/records";"example.com/godj-project-bundle/project";"github.com/progresshans/godj/orm")
func reject(){_=context.Background();f:=records.MeasureFields;api,_:=project.Using(nil);named,column,_:=project.AnnotateRecordsMeasure(api.RecordsMeasure,"adjusted",orm.Add(orm.F(f.Amount),int64(1)));_=named;_=column;
` + invalid.expression + "\n}\n"
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid named program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
