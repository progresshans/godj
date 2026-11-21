package codegen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func namedAnnotationReferenceProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	app, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "category", GoName: "Category", DBTable: "gdj_annotation_category", Fields: []schema.Field{schema.CharField("name", "Name", 24)}},
		{Name: "tag", GoName: "Tag", DBTable: "gdj_annotation_tag", Fields: []schema.Field{schema.CharField("name", "Name", 24)}},
		{Name: "measure", GoName: "Measure", DBTable: "gdj_annotation_measure", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.CharField("group_key", "GroupKey", 24, schema.Nullable()),
			schema.BooleanField("enabled", "Enabled"), schema.IntegerField("amount", "Amount"), schema.IntegerField("other", "Other", schema.Nullable()),
			schema.FloatField("score", "Score", schema.Nullable()), schema.DecimalField("price", "Price", 14, 2, schema.Nullable()),
			schema.DurationField("elapsed", "Elapsed", schema.Nullable()), schema.JSONField("document", "Document", schema.Nullable()),
			schema.ForeignKey("category", "CategoryID", schema.Target("records", "category"), schema.RelatedName("measures"), schema.Cascade, schema.Nullable()),
		}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("tags", "Tags", schema.Target("records", "tag"), schema.RelatedName("measures"), schema.Through(schema.Target("records", "measure_tag"), "measure", "tag"))}},
		{Name: "measure_tag", GoName: "MeasureTag", DBTable: "gdj_annotation_measure_tags", Fields: []schema.Field{
			schema.ForeignKey("measure", "MeasureID", schema.Target("records", "measure"), schema.RelatedName("tag_links"), schema.Cascade),
			schema.ForeignKey("tag", "TagID", schema.Target("records", "tag"), schema.RelatedName("measure_links"), schema.Cascade),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "annotation_pair", Fields: []string{"measure", "tag"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "records", Package: codegen.PackageSpec{PackageName: "records", ImportPath: "example.com/godj-project-bundle/records", Directory: "records"}, Schema: app}}}
}

func TestGeneratedNamedAnnotationReference(t *testing.T) {
	references := os.Getenv("GODJ_ANNOTATION_REFERENCE_DIR")
	if references == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required annotation reference is absent")
		}
		// Portable owners validate the unchanged historical observation bytes;
		// native owners must consume freshly executed source-bound observations.
		references = filepath.Join("testdata", "derivedresult")
	}
	bundle, err := codegen.GenerateProject(namedAnnotationReferenceProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "reference_test.go", "operations_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "namedannotationreference", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	source := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/annotation_reference")
	hashes := map[string]string{}
	for _, name := range []string{"observe.py", "source-verification.json", "run.py"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(digest[:])
	}
	if hashes["observe.py"] != "671c01676e34630d7d1f0ece791974e142bc198faad2a96670ee6b75fbade297" || hashes["source-verification.json"] != "96b6203d6d72e0031d1dd17bbee2acdef14e53c40637731e112161aa67401f87" {
		t.Fatal("independent annotation source changed")
	}
	required := []string{"TestNamedAnnotationReference"}
	for _, vendor := range []string{"sqlite", "postgres"} {
		historical, err := os.ReadFile(filepath.Join("testdata", "derivedresult", "django61-"+vendor+".json"))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(historical)
		if hex.EncodeToString(digest[:]) != map[string]string{"sqlite": "890be7473d897c94fcab00c2a80a212b19cbb53f9c300a7bf5295dfa6532cb5c", "postgres": "7776b22d1dc3fdfefb80868ecaccebc42d96b85eb1503c6c2f3ffecc073dfd5d"}[vendor] {
			t.Fatal("historical annotation bytes changed")
		}
		data, err := os.ReadFile(filepath.Join(references, "django61-"+vendor+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Kind, Backend string
			Commit        string `json:"django_commit"`
			Observer      string `json:"observer_sha256"`
			Sources       string `json:"source_verification_sha256"`
			Runner        string `json:"runner_sha256"`
			Runtime       struct{ Python, Django, SQLite string }
			RunnerRuntime struct{ Python, Timezone, Psycopg, Postgresql string } `json:"runner_runtime"`
			Unchanged     bool                                                   `json:"storage_unchanged"`
			Cleaned       bool                                                   `json:"storage_cleaned"`
			Cases         []struct{ Name string }
		}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		backend := vendor
		if backend == "postgres" {
			backend = "postgresql"
		}
		if document.Kind != "django-annotation-expression-exploratory-reference-v1" || document.Backend != backend || document.Commit != "fe0a859f537d4238cf49fca39073513206f83122" || document.Observer != hashes["observe.py"] || document.Sources != hashes["source-verification.json"] || !document.Unchanged || document.Runtime.Python != "3.14.3" || document.Runtime.Django != "6.1" || document.Runtime.SQLite != "3.50.4" {
			t.Fatal("annotation reference provenance or runtime differs", vendor)
		}
		if os.Getenv("GODJ_ANNOTATION_REFERENCE_DIR") != "" {
			if document.Runner != hashes["run.py"] || !document.Cleaned || document.RunnerRuntime.Python != "3.14.3" || document.RunnerRuntime.Timezone != "UTC" || vendor == "postgres" && (document.RunnerRuntime.Psycopg != "3.3.6" || !strings.HasPrefix(document.RunnerRuntime.Postgresql, "17.10")) {
				t.Fatal("fresh annotation reference or cleanup differs", vendor)
			}
		}
		if err := validateNamedAnnotationBoundaries(data, historical, vendor); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, record := range document.Cases {
			names = append(names, record.Name)
		}
		if len(names) != 77 || !slices.Equal(names, namedAnnotationReferenceCases) {
			t.Fatal("independent annotation case inventory differs", names)
		}
		writeGeneratedTestFile(t, root, "consumer/django61-"+vendor+".json", data)
		if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestNamedAnnotationReference/"+vendor)
		for _, name := range names {
			required = append(required, "TestNamedAnnotationReference/"+vendor+"/"+strings.ReplaceAll(name, " ", "_"))
		}
	}
	output := runStrictGeneratedCommandWithCapture(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestNamedAnnotationReference$", "./consumer"), func(output []byte) error {
		directory := os.Getenv("GODJ_ANNOTATION_REFERENCE_DIR")
		if directory == "" {
			return nil
		}
		file, err := os.OpenFile(filepath.Join(directory, "godj-annotation-native.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = file.Write(output)
		return errors.Join(err, file.Close())
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
			t.Fatal("foreign annotation reference consumer")
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
		t.Fatal("incomplete annotation comparison", len(runs), len(passes), len(required))
	}
	for _, n := range runs {
		if n != 1 {
			t.Fatal("duplicate annotation comparison")
		}
	}
	t.Logf("annotation reference: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
}

type namedAnnotationBoundaryRecord struct {
	Name          string
	Result, Error any
}

type namedAnnotationBoundaryDocument struct {
	Input any
	Cases []namedAnnotationBoundaryRecord
}

// Go differences have closed Django counterparts. A new upstream result must
// fail even when the expected Go-side rejection remains unchanged. All other
// cases are compared to their fresh observation by the native consumer.
func validateNamedAnnotationBoundaries(data, historical []byte, vendor string) error {
	var fresh, retained namedAnnotationBoundaryDocument
	if err := json.Unmarshal(data, &fresh); err != nil {
		return err
	}
	if err := json.Unmarshal(historical, &retained); err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh.Input, retained.Input) {
		return errors.New("annotation reference inputs differ from the pinned observer")
	}
	names := []string{"projection_excluded_field_shadow", "alias_name_1", "alias_name_part__name", "mixed_integer_float"}
	if vendor == "sqlite" {
		names = append(names, "integer_constant_overflow")
	} else if vendor != "postgres" {
		return errors.New("unknown annotation reference backend")
	}
	for _, name := range names {
		var got, want namedAnnotationBoundaryRecord
		observed, expected := 0, 0
		for _, record := range fresh.Cases {
			if record.Name == name {
				got, observed = record, observed+1
			}
		}
		for _, record := range retained.Cases {
			if record.Name == name {
				want, expected = record, expected+1
			}
		}
		if observed != 1 || expected != 1 || want.Error != nil || want.Result == nil || !reflect.DeepEqual(got, want) {
			return fmt.Errorf("Django counterpart for declared Go difference changed: %s/%s", vendor, name)
		}
	}
	return nil
}

func TestNamedAnnotationReferenceBoundaryEvidence(t *testing.T) {
	for _, vendor := range []string{"sqlite", "postgres"} {
		data, err := os.ReadFile(filepath.Join("testdata", "derivedresult", "django61-"+vendor+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateNamedAnnotationBoundaries(data, data, vendor); err != nil {
			t.Fatal(err)
		}
		for _, mutation := range []string{"projection_excluded_field_shadow", "alias_name_1", "alias_name_part__name", "mixed_integer_float", "error", "missing", "duplicate", "input", "vendor", "integer_constant_overflow"} {
			if mutation == "integer_constant_overflow" && vendor == "postgres" {
				continue // PostgreSQL overflow is a native SQLSTATE comparison.
			}
			var document namedAnnotationBoundaryDocument
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			backend := vendor
			switch mutation {
			case "input":
				document.Input = nil
			case "vendor":
				backend = "unknown"
			default:
				name := mutation
				if mutation == "error" || mutation == "missing" || mutation == "duplicate" {
					name = "mixed_integer_float"
				}
				index := slices.IndexFunc(document.Cases, func(record namedAnnotationBoundaryRecord) bool { return record.Name == name })
				if index < 0 {
					t.Fatal("missing boundary case", name)
				}
				switch mutation {
				case "error":
					document.Cases[index].Error = "unexpected failure"
				case "missing":
					document.Cases = slices.Delete(document.Cases, index, index+1)
				case "duplicate":
					document.Cases = append(document.Cases, document.Cases[index])
				default:
					document.Cases[index].Result = "unexpected result"
				}
			}
			changed, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateNamedAnnotationBoundaries(changed, data, backend); err == nil {
				t.Fatal("altered boundary evidence was accepted", vendor, mutation)
			}
		}
	}
}

var namedAnnotationReferenceCases = []string{
	"scalar_model_and_projection",
	"scalar_values_keyword",
	"same_call_dependency",
	"same_call_forward_reference",
	"rebind_prior_alias",
	"dependent_alias_before_rebind",
	"field_shadow",
	"fk_attname_shadow",
	"projection_excluded_field_shadow",
	"unknown_reference",
	"cycle_reference",
	"missing_output_null",
	"typed_output_null",
	"nullable_arithmetic",
	"integer_divide_by_zero",
	"integer_remainder_by_zero",
	"integer_negative_division",
	"integer_negative_remainder",
	"integer_constant_overflow",
	"integer_overflow_empty_native",
	"integer_overflow_empty_folded",
	"mixed_integer_float",
	"decimal_arithmetic",
	"duration_arithmetic",
	"field_copy_domains",
	"forward_optional_field",
	"scalar_filter",
	"scalar_not_nullable_filter",
	"scalar_compare_aliases",
	"scalar_order_nulls_first",
	"scalar_order_nulls_last",
	"annotate_before_slice",
	"annotate_after_slice",
	"filter_after_slice",
	"order_after_slice",
	"count_scalar_annotation",
	"exists_scalar_annotation",
	"alias_filter_not_selected",
	"alias_select_rejected",
	"alias_promoted_to_annotation",
	"sum_computed_operand",
	"avg_computed_operand",
	"sum_distinct_computed_operand",
	"sum_filtered_computed_operand",
	"aggregate_annotation_alias",
	"aggregate_sliced_annotation_alias",
	"distinct_projection_with_annotation",
	"distinct_projection_order_expands_selection",
	"group_then_computed_aggregate",
	"group_then_computed_having",
	"computed_key_then_group",
	"group_key_retained_when_hidden",
	"row_aggregate_annotation",
	"related_count_annotation",
	"related_count_having",
	"related_count_mixed_or",
	"filter_then_related_annotation",
	"related_annotation_then_filter",
	"related_annotate_then_slice",
	"related_slice_then_annotate",
	"terminal_average_of_annotation",
	"nested_aggregate_annotation_rejected",
	"reverse_count_and_sum",
	"multi_join_count_multiplicity",
	"multi_join_distinct_count",
	"reverse_count_filter_before",
	"reverse_count_filter_after",
	"aggregate_empty_default",
	"empty_annotation",
	"annotation_expression_sql_literals",
	"alias_name_bad name",
	"alias_name_quote\"name",
	"alias_name_comment--name",
	"alias_name_1",
	"alias_name_part__name",
	"cache_owned_by_annotation_query",
	"immutable_annotation_branches",
}
