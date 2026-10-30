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

func numericAggregateProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	app, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "measure", GoName: "Measure", DBTable: "gdj_numeric_measure", Fields: []schema.Field{
			schema.CharField("group_key", "GroupKey", 20, schema.Nullable()), schema.BooleanField("enabled", "Enabled"),
			schema.IntegerField("amount", "Amount", schema.Nullable()), schema.FloatField("score", "Score", schema.Nullable()),
			schema.DecimalField("price", "Price", 14, 2, schema.Nullable()), schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
			schema.DecimalField("precise_price", "PrecisePrice", 40, 24, schema.Nullable()), schema.DecimalField("wide_price", "WidePrice", 40, 2, schema.Nullable()),
			schema.DecimalField("full_price", "FullPrice", 1000, 1000, schema.Nullable()), schema.DecimalField("full_whole_price", "FullWholePrice", 1000, 0, schema.Nullable()),
		}},
		{Name: "required", GoName: "Required", DBTable: "gdj_numeric_required", Fields: []schema.Field{
			schema.IntegerField("amount", "Amount"), schema.FloatField("score", "Score"), schema.DecimalField("price", "Price", 14, 2), schema.DurationField("elapsed", "Elapsed"),
		}},
		{Name: "link", GoName: "Link", DBTable: "gdj_numeric_link", Fields: []schema.Field{
			schema.CharField("group_key", "GroupKey", 20),
			schema.ForeignKey("measure", "MeasureID", schema.Target("records", "measure"), schema.RelatedName("links"), schema.Cascade, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "records", Package: codegen.PackageSpec{PackageName: "records", ImportPath: "example.com/godj-project-bundle/records", Directory: "records"}, Schema: app}}}
}

type numericFixtureInventory struct {
	Kind     string
	Backend  string
	Observer string `json:"observer_sha256"`
	Sources  string `json:"source_verification_sha256"`
	Commit   string `json:"django_commit"`
	Cases    []struct {
		Name    string
		Actions []struct{ Name string }
	}
}

func TestGeneratedNumericAggregation(t *testing.T) {
	bundle, err := codegen.GenerateProject(numericAggregateProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "reference_test.go", "runtime_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "numericaggregate", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	observer, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/numeric_aggregate_reference/observe.py"))
	if err != nil {
		t.Fatal(err)
	}
	observerSHA := sha256.Sum256(observer)
	if hex.EncodeToString(observerSHA[:]) != "26dc4886594473fe778b5030c85944d7fcd2bef6175c8484e8c1e278398020af" {
		t.Fatal("independent numeric observer changed")
	}
	boundaryObserver, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/numeric_aggregate_reference/duration_boundaries.py"))
	if err != nil {
		t.Fatal(err)
	}
	boundarySHA := sha256.Sum256(boundaryObserver)
	if hex.EncodeToString(boundarySHA[:]) != "87fbe1d81e52b3b042105d3b22f00814e1fbad17939a5e1303032a75ffe008ea" {
		t.Fatal("independent duration boundary observer changed")
	}
	source, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/numeric_aggregate_reference/source-verification.json"))
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := sha256.Sum256(source)
	if hex.EncodeToString(sourceSHA[:]) != "8584f23e350a2908bd25e1419f657abc1384e8e2502ccd2abe89c887eb844d7c" {
		t.Fatal("upstream source verification changed")
	}
	required := []string{"TestNumericReference", "TestNumericRuntime"}
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "numericaggregate", name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != map[string]string{"sqlite": "7122c1d4036dadb45a18cf58930c7791ea6b879176d15cb828ce51ce039c97cf", "postgres": "6721d69a79b8c23028dd27cb3ea7f1d85e9a6ff15dc655742c7f23a4d57d1080"}[backend] {
			t.Fatal("raw numeric reference changed", backend)
		}
		var fixture numericFixtureInventory
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		vendor := backend
		if vendor == "postgres" {
			vendor = "postgresql"
		}
		if fixture.Kind != "django-numeric-precision-exploratory-reference-v1" || fixture.Backend != vendor || fixture.Observer != hex.EncodeToString(observerSHA[:]) || fixture.Sources != "8584f23e350a2908bd25e1419f657abc1384e8e2502ccd2abe89c887eb844d7c" || fixture.Commit != "fe0a859f537d4238cf49fca39073513206f83122" || len(fixture.Cases) != 30 {
			t.Fatal("numeric reference source or inventory", backend)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
		boundaryName := "django61-duration-" + backend + ".json"
		boundaryData, err := os.ReadFile(filepath.Join("testdata", "numericaggregate", boundaryName))
		if err != nil {
			t.Fatal(err)
		}
		boundaryDigest := sha256.Sum256(boundaryData)
		if hex.EncodeToString(boundaryDigest[:]) != map[string]string{"sqlite": "e8159030700f0fb2b0ed2c68925d9081d1cfa60317ae3443d02e3301b29c497e", "postgres": "520ff87e2bac2598b0d7e0a240d392909428deb99a6e07266de5d2d8bcd50b21"}[backend] {
			t.Fatal("raw duration boundary reference changed")
		}
		var boundaryFixture numericFixtureInventory
		if err := json.Unmarshal(boundaryData, &boundaryFixture); err != nil {
			t.Fatal(err)
		}
		if boundaryFixture.Kind != "django-duration-aggregate-boundary-reference-v1" || boundaryFixture.Backend != vendor || boundaryFixture.Observer != hex.EncodeToString(boundarySHA[:]) || boundaryFixture.Sources != fixture.Sources || boundaryFixture.Commit != fixture.Commit || len(boundaryFixture.Cases) != 4 {
			t.Fatal("duration reference source or inventory")
		}
		for _, record := range boundaryFixture.Cases {
			record.Name = "boundary_" + record.Name
			fixture.Cases = append(fixture.Cases, record)
		}
		writeGeneratedTestFile(t, root, "consumer/"+boundaryName, boundaryData)
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestNumericReference/"+backend, "TestNumericRuntime/"+backend)
		actions := 0
		for _, record := range fixture.Cases {
			prefix := "TestNumericReference/" + backend + "/" + record.Name
			required = append(required, prefix)
			for _, action := range record.Actions {
				required = append(required, prefix+"/"+action.Name)
				actions++
			}
		}
		if actions != 548 {
			t.Fatal("numeric action inventory", actions)
		}
		for _, name := range []string{"typed_dynamic_group_and_having", "required_and_forward_numeric_fields", "scalar_slice_distinct_empty_and_cancellation", "native_failure_and_special_values"} {
			required = append(required, "TestNumericRuntime/"+backend+"/"+name)
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestNumeric(Reference|Runtime)$", "./consumer"))
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
			t.Fatal("foreign numeric consumer package")
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
		t.Fatal("numeric consumer incomplete inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate numeric consumer test")
		}
	}
	t.Logf("numeric generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"sum_text", `_ = orm.Sum(records.MeasureFields.GroupKey)`, "SumField"},
		{"avg_boolean", `_ = orm.Avg(records.MeasureFields.Enabled)`, "AvgField"},
		{"integer_average_result", `var _ orm.AggregateExpression[records.Measure,orm.Optional[int64]] = orm.Avg(records.MeasureFields.Amount)`, "cannot use"},
		{"foreign_model", `_,_ = project.AggregateRecordsMeasureInto(context.Background(),source,orm.Aggregate1(orm.Sum(records.RequiredFields.Amount),func(v orm.Optional[int64])orm.Optional[int64]{return v}))`, "does not match"},
		{"external_numeric_operand", `_ = orm.Sum[records.Measure,int64](struct{}{})`, "scalarSumField"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(`package invalid
import("context";"example.com/godj-project-bundle/records";"example.com/godj-project-bundle/project";"github.com/progresshans/godj/orm")
func reject(){api,_:=project.Using(nil);source:=api.RecordsMeasure;_=source;_=context.Background;_=records.MeasureFields
`+invalid.expression+"\n}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid numeric program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
