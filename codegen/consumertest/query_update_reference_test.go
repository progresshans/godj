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
)

func TestGeneratedQueryUpdateReference(t *testing.T) {
	bundle, err := codegen.GenerateProject(queryUpdateProjectSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "single_object_test.go", "query_update_test.go", "query_update_reference_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	cases := strings.Fields(`empty_assignments unknown_field many_to_many_field related_assignment pk_alias_assignment primary_key_constant primary_key_expression primary_key_collision primary_key_referenced constant unchanged_count no_matches empty_query empty_query_unknown_field empty_query_invalid_value query_filter forward_filter many_to_many_filter boolean_filter ordered_distinct distinct_fields sliced sliced_empty_assignments union row_lock_read_shape eager_prefetch_read_shape projection_read_shape original_row_swap original_row_order original_row_reverse_order add subtract multiply divide remainder negate nested nullable_add nullable_copy null_to_nonnullable literal_null null_expression field_string field_boolean field_fk set_fk_id set_fk_null missing_fk joined_reference missing_reference aggregate_reference failure_unique failure_check failure_not_null cached_success cached_empty_assignments cached_failure parent_commit parent_rollback borrowed_failure savepoint_failure overflow_add overflow_subtract overflow_multiply overflow_negate overflow_divide minimum_remainder max_exact_add min_exact_subtract divide_by_zero nullable_divide_by_zero remainder_by_zero float_add float_divide float_divide_by_zero float_to_integer integer_mixed_float decimal_add decimal_multiply decimal_divide_by_zero decimal_overflow`)
	if len(cases) != 81 {
		t.Fatal("query update native inventory is incomplete")
	}
	static := map[string]string{"distinct_fields": "TypeError", "union": "NotSupportedError", "projection_read_shape": ""}
	observer, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/query_update_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	observerDigest := sha256.Sum256(observer)
	if hex.EncodeToString(observerDigest[:]) != "a61ce4048904d6678fc9e8db4ded3f8ead04ba921a196096598543831aeb18f3" {
		t.Fatal("independent query update observer changed")
	}
	required := []string{"TestQueryUpdateReference"}
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "query_update-django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "queryupdate", name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != map[string]string{"sqlite": "2b53a7fe688a97ec0150cb2c4f00ec99f201060c0230a401aa1f91a3e2e21130", "postgres": "9dc25a0e3eba658fd4906715423124c54b0d570bd5e1109ae7eaf8a141e3f76c"}[backend] {
			t.Fatal("independent query update bytes changed", backend)
		}
		var fixture struct {
			Kind, Django, Python, Backend string
			Observer                      string            `json:"observer_sha256"`
			Sources                       map[string]string `json:"source_sha256"`
			Cases                         []struct {
				Case  string
				Value struct {
					Error string
					Count *int64
				}
				Updates int `json:"update_statements"`
			}
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		vendor := backend
		if vendor == "postgres" {
			vendor = "postgresql"
		}
		if fixture.Kind != "django-query-update-reference-v1" || fixture.Django != "6.1" || fixture.Python != "3.14.3" || fixture.Backend != vendor || fixture.Observer != hex.EncodeToString(observerDigest[:]) || len(fixture.Cases) != len(cases) {
			t.Fatal("query update reference provenance/inventory changed", backend)
		}
		if !maps.Equal(fixture.Sources, map[string]string{
			"Atomic":             "aee995a682da7157a7ef415521e4d3336c25fd5a25c1b8582e2453c5f1d30a79",
			"QuerySet":           "5e86af673328a1d6800342f4bec9d4e2352b7919bebef349d98962d67569eae6",
			"SQLUpdateCompiler":  "38e5ed0147669e8085f00b02646e5b7436ece76f6bb399bef4acd4ae9f07feb4",
			"UpdateQuery":        "bf3416518f396f6745dc0476cb1720695bd0f9ade7e30a24ab425ff3dcad09f5",
			"CombinedExpression": "5e2cd9d23b08da0ff7cfedab7f20164bce1f0ab61de9947a7219d2a14d344971",
		}) {
			t.Fatal("query update fixture has foreign upstream source")
		}
		for index, name := range cases {
			observed := fixture.Cases[index]
			if observed.Case != name {
				t.Fatal("native query update case order changed", name)
			}
			if wantError, unavailable := static[name]; unavailable {
				if observed.Value.Error != wantError {
					t.Fatal("native unavailable shape changed", name)
				}
				if name == "projection_read_shape" {
					if observed.Value.Count == nil || *observed.Value.Count != 3 || observed.Updates != 1 {
						t.Fatal("native projection update changed")
					}
				} else if observed.Value.Count != nil || observed.Updates != 0 {
					t.Fatal("native shape rejection changed", name)
				}
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
		if backend != "postgres" || strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" || os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			required = append(required, "TestQueryUpdateReference/"+backend)
			for _, name := range cases {
				if _, unavailable := static[name]; !unavailable {
					required = append(required, "TestQueryUpdateReference/"+backend+"/"+name)
				}
			}
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestQueryUpdateReference$", "./consumer"))
	assertGeneratedConsumerTests(t, output, required...)
	checkQueryUpdateEvents(t, output, required)
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"union", `_,_ = source.Union(source).Update(ctx)`, "Union undefined"},
		{"distinct_fields", `_,_ = source.Distinct(owners.QueryUpdateItemFields.Name).Update(ctx)`, "too many arguments"},
		{"projection_read_shape", `projection:=orm.Project1(owners.QueryUpdateItemFields.Name,func(value string)string{return value});_,_ = projection.Update(ctx)`, "Update undefined"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte("package invalid\nimport (\"context\";\"example.com/godj-project-bundle/owners\";\"example.com/godj-project-bundle/project\";\"github.com/progresshans/godj/orm\")\nfunc invalidInput(){ctx:=context.Background();api,_:=project.Using(nil);source:=api.OwnersQueryUpdateItem;_=ctx;_=source;_=owners.QueryUpdateItem{};_=orm.Value[owners.QueryUpdateItem](int64(1));"+invalid.expression+"}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("unavailable query shape compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
