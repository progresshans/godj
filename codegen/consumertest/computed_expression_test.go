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

func computedExpressionProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	app, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "measure", GoName: "Measure", DBTable: "gdj_computed_measure", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.CharField("group_key", "GroupKey", 24, schema.Nullable()),
			schema.BooleanField("enabled", "Enabled"), schema.IntegerField("amount", "Amount"), schema.IntegerField("other", "Other", schema.Nullable()),
			schema.FloatField("score", "Score", schema.Nullable()), schema.DecimalField("price", "Price", 14, 2, schema.Nullable()), schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
		}},
		{Name: "other", GoName: "Other", DBTable: "gdj_computed_other", Fields: []schema.Field{schema.IntegerField("amount", "Amount"), schema.BooleanField("enabled", "Enabled")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "records", Package: codegen.PackageSpec{PackageName: "records", ImportPath: "example.com/godj-project-bundle/records", Directory: "records"}, Schema: app}}}
}

func TestGeneratedComputedExpressions(t *testing.T) {
	bundle, err := codegen.GenerateProject(computedExpressionProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "reference_test.go", "runtime_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "computedexpression", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	source := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/computed_expression_reference")
	observer, err := os.ReadFile(filepath.Join(source, "observe.py"))
	if err != nil {
		t.Fatal(err)
	}
	observerDigest := sha256.Sum256(observer)
	verification, err := os.ReadFile(filepath.Join(source, "source-verification.json"))
	if err != nil {
		t.Fatal(err)
	}
	verificationDigest := sha256.Sum256(verification)
	if hex.EncodeToString(verificationDigest[:]) != "96b6203d6d72e0031d1dd17bbee2acdef14e53c40637731e112161aa67401f87" {
		t.Fatal("independent source verification changed")
	}
	required := []string{"TestComputedReference", "TestComputedRuntime"}
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "computedexpression", name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != map[string]string{"sqlite": "2001bec8108cdc6ff653ab0a52c67da1e6d6dbe925c3ced61abb5a409d67e755", "postgres": "0f4602f9fe2332e0f9a0b29357cdf2ff5c956b03ff93585a8bee35f6cb5d82d2"}[backend] {
			t.Fatal("raw computed reference changed", backend)
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
		vendor := backend
		if vendor == "postgres" {
			vendor = "postgresql"
		}
		if fixture.Kind != "django-computed-case-reference-v1" || fixture.Backend != vendor || fixture.Observer != hex.EncodeToString(observerDigest[:]) || fixture.Sources != hex.EncodeToString(verificationDigest[:]) || fixture.Commit != "fe0a859f537d4238cf49fca39073513206f83122" || !fixture.Unchanged || len(fixture.Cases) != 20 {
			t.Fatal("computed reference provenance or inventory")
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestComputedReference/"+backend, "TestComputedRuntime/"+backend)
		for _, record := range fixture.Cases {
			required = append(required, "TestComputedReference/"+backend+"/"+record.Name)
		}
		for _, name := range []string{"typed_dynamic_projection_and_predicates", "group_keys_aggregates_and_having", "slice_distinct_empty_and_arguments", "source_cache_and_lifetime", "native_failures_and_precision", "dynamic_rejection_and_ownership"} {
			required = append(required, "TestComputedRuntime/"+backend+"/"+name)
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestComputed(Reference|Runtime)$", "./consumer"))
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
			t.Fatal("foreign computed consumer package")
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
		t.Fatal("computed consumer incomplete inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate computed consumer test")
		}
	}
	t.Logf("computed generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"sum_boolean", `_ = orm.Sum(orm.Value[records.Measure](true))`, "SumField"},
		{"numeric_boolean", `_ = orm.Numeric(orm.Value[records.Measure](true))`, "ArithmeticNumber"},
		{"wrong_average_type", `var _ orm.AggregateExpression[records.Measure,orm.Optional[int64]] = orm.Avg(orm.Add(orm.F(records.MeasureFields.Amount),int64(1)))`, "cannot use"},
		{"wrong_projection_type", `_ = orm.Project1(orm.Add(orm.F(records.MeasureFields.Amount),int64(1)),func(v int64)int64{return v})`, "does not match"},
		{"mixed_case", `_ = orm.Case(orm.F(records.MeasureFields.Amount),orm.When(records.MeasureFields.Enabled.Exact(true),orm.Value[records.Measure]("text")))`, "does not match"},
		{"foreign_predicate", `_ = orm.When(records.OtherFields.Enabled.Exact(true),orm.F(records.MeasureFields.Amount))`, "does not match"},
		{"external_operand", `_ = orm.Add[records.Measure,int64](struct{}{},1)`, "scalarOperand"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte("package invalid\nimport(\"example.com/godj-project-bundle/records\";\"github.com/progresshans/godj/orm\")\nfunc reject(){ _=records.MeasureFields;"+invalid.expression+"\n}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid computed program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
