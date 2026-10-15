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

func decimalExpressionProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	pricing, err := schema.Build(schema.Definition{AppLabel: "pricing", Models: []schema.Model{
		{Name: "rate", GoName: "Rate", DBTable: "gdj_decimal_rate", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.DecimalField("factor", "Factor", 12, 4, schema.Nullable()), schema.DecimalField("flat", "Flat", 14, 2, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := schema.Build(schema.Definition{AppLabel: "ledger", Models: []schema.Model{
		{Name: "entry", GoName: "Entry", DBTable: "gdj_decimal_entry", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.CharField("bucket", "Bucket", 24), schema.BooleanField("enabled", "Enabled"),
			schema.DecimalField("amount", "Amount", 14, 2, schema.Nullable()), schema.DecimalField("balance", "Balance", 14, 2),
			schema.DecimalField("fine", "Fine", 20, 6, schema.Nullable()), schema.DecimalField("wide", "Wide", 1000, 1000, schema.Nullable()),
			schema.ForeignKey("rate", "RateID", schema.Target("pricing", "rate"), schema.RelatedName("entries"), schema.SetNull, schema.Nullable()),
		}},
		{Name: "adjustment", GoName: "Adjustment", DBTable: "gdj_decimal_adjustment", Fields: []schema.Field{
			schema.OneToOne("entry", "EntryID", schema.Target("ledger", "entry"), schema.RelatedName("adjustment"), schema.Cascade),
			schema.DecimalField("bonus", "Bonus", 14, 2, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{
		{Alias: "pricing", Package: codegen.PackageSpec{PackageName: "pricing", ImportPath: "example.com/godj-project-bundle/pricing", Directory: "pricing"}, Schema: pricing},
		{Alias: "ledger", Package: codegen.PackageSpec{PackageName: "ledger", ImportPath: "example.com/godj-project-bundle/ledger", Directory: "ledger"}, Schema: ledger},
	}}
}

var decimalReferenceCases = []string{
	"add",
	"subtract",
	"multiply",
	"negate",
	"case_null",
	"related_arithmetic",
	"reverse_arithmetic",
	"comparison_rhs",
	"order_page",
	"computed_aggregate",
	"distinct_filtered_aggregate",
	"groups_having",
	"empty_aggregate",
	"all_null_aggregate",
	"derived_aggregate",
	"update_add",
	"update_old_row",
	"update_null",
	"divide",
	"divide_integer",
	"divide_native_scale",
	"remainder",
	"divide_zero",
	"remainder_zero",
	"large_literal",
	"update_rounding",
	"update_overflow",
	"case_unselected_zero",
	"wide_product",
}

func TestGeneratedDecimalExpressions(t *testing.T) {
	bundle, err := codegen.GenerateProject(decimalExpressionProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "observations_test.go", "runtime_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "decimalexpression", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	profile := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/decimal_expression_reference")
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
	references := os.Getenv("GODJ_DECIMAL_EXPRESSION_REFERENCE_DIR")
	if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" && references == "" {
		t.Fatal("native consumer owner lacks its current Django reference")
	}
	if references != "" {
		data, err := os.ReadFile(filepath.Join("testdata", "decimalexpression", "reference_test.go"))
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
				Runtime       struct{ Python, Django, SQLite, PostgreSQL, Psycopg, Timezone string }
				Unchanged     bool `json:"storage_unchanged"`
				Cleaned       bool `json:"storage_cleaned"`
				Cases         []struct {
					Name      string
					Unchanged bool `json:"storage_unchanged"`
					Error     *struct{ Type, SQLState string }
				}
			}
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if document.Kind != "django-decimal-expression-reference-v1" || document.Backend != vendor || document.Commit != "fe0a859f537d4238cf49fca39073513206f83122" ||
				document.Observer != hashes["observe.py"] || document.Input != hashes["input.json"] || document.Sources != hashes["source-verification.json"] || !document.Unchanged || !document.Cleaned {
				t.Fatal("decimal reference is not bound to the current observer/input/source or its cleanup", vendor)
			}
			if document.Runtime.Python != "3.14.3" || document.Runtime.Django != "6.1" || document.Runtime.SQLite == "" || document.Runtime.Timezone != "UTC" ||
				(vendor == "postgres" && (!strings.HasPrefix(document.Runtime.PostgreSQL, "17.10") || document.Runtime.Psycopg != "3.3.6")) {
				t.Fatal("decimal reference runtime differs", vendor, document.Runtime)
			}
			var names []string
			for _, record := range document.Cases {
				if !record.Unchanged {
					t.Fatal("independent decimal case changed storage", vendor, record.Name)
				}
				wantType, wantState := "", ""
				if record.Name == "update_overflow" {
					wantType = "InvalidOperation"
					if vendor == "postgres" {
						wantType, wantState = "DataError", "22003"
					}
				} else if vendor == "postgres" && (record.Name == "divide_zero" || record.Name == "remainder_zero") {
					wantType, wantState = "DataError", "22012"
				}
				if (record.Error == nil) != (wantType == "") || record.Error != nil && (record.Error.Type != wantType || record.Error.SQLState != wantState) {
					t.Fatal("independent decimal error inventory differs", vendor, record.Name, record.Error)
				}
				names = append(names, record.Name)
			}
			if !slices.Equal(names, decimalReferenceCases) {
				t.Fatal("decimal reference inventory differs", vendor, names)
			}
			writeGeneratedTestFile(t, root, "consumer/django61-"+vendor+".json", data)
			t.Logf("decimal reference %s: sha256=%x observer=%s input=%s sources=%s cases=%d", vendor, sha256.Sum256(data), document.Observer, document.Input, document.Sources, len(names))
		}
	}
	required := []string{"TestDecimalRuntime"}
	if references != "" {
		required = append(required, "TestDecimalReference")
	}
	for _, vendor := range []string{"sqlite", "postgres"} {
		if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestDecimalRuntime/"+vendor)
		for _, name := range []string{"typed_dynamic_and_exact_values", "aggregates_having_and_result_stages", "related_source_precision_and_scope", "assignment_precision_and_atomicity", "case_and_null_evaluation", "cache_context_cursor_and_session", "physical_corruption_and_intermediate_bounds", "native_division_scale_and_wide_values", "resource_budgets"} {
			required = append(required, "TestDecimalRuntime/"+vendor+"/"+name)
		}
		if references != "" {
			required = append(required, "TestDecimalReference/"+vendor)
			for _, name := range decimalReferenceCases {
				required = append(required, "TestDecimalReference/"+vendor+"/"+name)
			}
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestDecimal(Reference|Runtime)$", "./consumer"))
	if references != "" {
		// Retain the untruncated native child events alongside this owner's
		// independent reference. A later audit can check each run/pass itself.
		file, err := os.OpenFile(filepath.Join(references, "godj-decimal-native.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(output)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot preserve decimal native events", writeErr, closeErr)
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
			t.Fatal("foreign decimal consumer package")
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
		t.Fatal("incomplete decimal consumer inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate decimal consumer execution")
		}
	}
	t.Logf("decimal generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"foreign_decimal_model", `_ = orm.Decimal(orm.F(ledger.EntryFields.Amount)).AddExpression(orm.F(pricing.RateFields.Factor))`, "wrong type for method scalarOperand"},
		{"mixed_float_operand", `_ = orm.Decimal(orm.F(ledger.EntryFields.Amount)).Add(float64(1))`, "decimal.Decimal"},
		{"mixed_integer_operand", `_ = orm.Decimal(orm.F(ledger.EntryFields.Amount)).AddExpression(orm.Value[ledger.Entry](int64(1)))`, "wrong type for method scalarOperand"},
		{"wrong_decimal_average", `_ = orm.Aggregate1(orm.Avg(orm.Decimal(orm.F(ledger.EntryFields.Amount))),func(value orm.Optional[float64])float64{return 0})`, "does not match"},
		{"wrong_nullable_projection", `_ = orm.Project1(orm.Decimal(orm.F(ledger.EntryFields.Amount)),func(value decimal.Decimal)decimal.Decimal{return value})`, "does not match"},
		{"foreign_assignment_model", `_ = orm.AssignExpression(ledger.EntryFields.Amount,orm.Decimal(orm.F(pricing.RateFields.Factor)))`, "does not match"},
		{"string_decimal_assignment", `_ = orm.AssignExpression(ledger.EntryFields.Amount,orm.Value[ledger.Entry]("1.25"))`, "does not match"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			program := "package invalid\nimport(\"example.com/godj-project-bundle/ledger\";\"example.com/godj-project-bundle/pricing\";\"github.com/progresshans/godj/decimal\";\"github.com/progresshans/godj/orm\")\nfunc reject(){ _=ledger.EntryFields; _=pricing.RateFields; _=decimal.Decimal{};" + invalid.expression + "\n}\n"
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid decimal program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
