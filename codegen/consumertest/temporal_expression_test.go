package codegen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func temporalExpressionProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	planning, err := schema.Build(schema.Definition{AppLabel: "planning", Models: []schema.Model{
		{Name: "schedule", GoName: "Schedule", DBTable: "gdj_temporal_schedule", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.DurationField("offset", "Offset", schema.Nullable()), schema.DateTimeField("anchor", "Anchor", schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "event", GoName: "Event", DBTable: "gdj_temporal_event", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.CharField("bucket", "Bucket", 24), schema.BooleanField("enabled", "Enabled"),
			schema.DateTimeField("start", "Start"), schema.DateTimeField("finish", "Finish", schema.Nullable()),
			schema.DurationField("span", "Span", schema.Nullable()), schema.DurationField("other", "Other", schema.Nullable()),
			schema.ForeignKey("schedule", "ScheduleID", schema.Target("planning", "schedule"), schema.RelatedName("events"), schema.SetNull, schema.Nullable()),
		}},
		{Name: "detail", GoName: "Detail", DBTable: "gdj_temporal_detail", Fields: []schema.Field{
			schema.OneToOne("event", "EventID", schema.Target("records", "event"), schema.RelatedName("detail"), schema.Cascade),
			schema.DurationField("offset", "Offset", schema.Nullable()), schema.DateTimeField("occurred", "Occurred", schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{
		{Alias: "planning", Package: codegen.PackageSpec{PackageName: "planning", ImportPath: "example.com/godj-project-bundle/planning", Directory: "planning"}, Schema: planning},
		{Alias: "records", Package: codegen.PackageSpec{PackageName: "records", ImportPath: "example.com/godj-project-bundle/records", Directory: "records"}, Schema: records},
	}}
}

var temporalReferenceCases = []string{
	"datetime_add_duration",
	"duration_add_datetime",
	"datetime_subtract_duration",
	"datetime_difference",
	"duration_add",
	"duration_subtract",
	"duration_negate",
	"typed_null_duration",
	"typed_null_datetime",
	"null_difference",
	"case_fallback",
	"related_shift",
	"reverse_shift",
	"compare_computed",
	"order_page",
	"computed_aggregate",
	"distinct_filtered_span",
	"groups_having",
	"empty_aggregate",
	"all_null_aggregate",
	"collection_duration",
	"collection_shift_min",
	"derived_duration",
	"update_shift",
	"update_old_row",
	"update_null",
	"large_datetime_difference",
	"negative_datetime_difference",
	"datetime_upper_overflow",
	"datetime_lower_overflow",
	"duration_int64_overflow",
	"duration_full_model_literal",
	"duration_model_overflow",
	"case_unselected_overflow",
	"mean_fractional_shift",
	"mean_integral_shift",
	"mean_zero_shift",
	"mean_null_shift",
	"mean_reverse_shift",
	"mean_add_duration",
	"mean_negate_duration",
	"mean_reaggregate",
	"update_datetime_overflow",
	"update_duration_overflow",
	"scale_integer",
	"scale_integer_reverse",
	"scale_divide_restore",
	"scale_half_final",
	"scale_float_tie",
	"scale_decimal_tie",
	"fraction_sum",
	"fraction_distinct_sum",
	"fraction_shift",
	"scale_zero_divisor",
	"duration_ratio",
}

func TestGeneratedTemporalExpressions(t *testing.T) {
	bundle, err := codegen.GenerateProject(temporalExpressionProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "observations_test.go", "runtime_test.go", "duration_scaling_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "temporalexpression", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	profile := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/temporal_expression_reference")
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
	references := os.Getenv("GODJ_TEMPORAL_EXPRESSION_REFERENCE_DIR")
	if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" && references == "" {
		t.Fatal("native consumer owner lacks its current Django reference")
	}
	if references != "" {
		for _, name := range []string{"reference_test.go", "duration_scaling_reference_test.go"} {
			data, err := os.ReadFile(filepath.Join("testdata", "temporalexpression", name))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/"+name, data)
		}
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
			if document.Kind != "django-temporal-expression-reference-v1" || document.Backend != vendor || document.Commit != "fe0a859f537d4238cf49fca39073513206f83122" ||
				document.Observer != hashes["observe.py"] || document.Input != hashes["input.json"] || document.Sources != hashes["source-verification.json"] || !document.Unchanged || !document.Cleaned {
				t.Fatal("temporal reference is not bound to the current observer/input/source or its cleanup", vendor)
			}
			if document.Runtime.Python != "3.14.3" || document.Runtime.Django != "6.1" || document.Runtime.SQLite == "" || document.Runtime.Timezone != "UTC" ||
				(vendor == "postgres" && (!strings.HasPrefix(document.Runtime.PostgreSQL, "17.10") || document.Runtime.Psycopg != "3.3.6")) {
				t.Fatal("temporal reference runtime differs", vendor, document.Runtime)
			}
			var names []string
			for _, record := range document.Cases {
				if !record.Unchanged {
					t.Fatal("independent temporal case changed storage", vendor, record.Name)
				}
				if record.Error != nil {
					boundary := []string{"datetime_upper_overflow", "datetime_lower_overflow", "duration_full_model_literal", "duration_model_overflow", "mean_zero_shift", "update_datetime_overflow", "update_duration_overflow", "scale_zero_divisor"}
					ratioOperator := vendor == "postgres" && record.Name == "duration_ratio" && record.Error.Type == "ProgrammingError"
					if !ratioOperator && (!slices.Contains(boundary, record.Name) || !slices.Contains([]string{"DataError", "OperationalError", "OverflowError"}, record.Error.Type)) {
						t.Fatal("unowned independent temporal error", vendor, record.Name, record.Error)
					}
				}
				names = append(names, record.Name)
			}
			if !slices.Equal(names, temporalReferenceCases) {
				t.Fatal("temporal reference inventory differs", vendor, names)
			}
			writeGeneratedTestFile(t, root, "consumer/django61-"+vendor+".json", data)
			t.Logf("temporal reference %s: sha256=%x observer=%s input=%s sources=%s cases=%d", vendor, sha256.Sum256(data), document.Observer, document.Input, document.Sources, len(names))
		}
	}
	required := []string{"TestTemporalRuntime"}
	if references != "" {
		required = append(required, "TestTemporalReference", "TestTemporalReference/scaling_reference_integrity")
	}
	for _, vendor := range []string{"sqlite", "postgres"} {
		if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestTemporalRuntime/"+vendor)
		for _, name := range []string{"typed_dynamic_and_exact_values", "filter_order_and_page", "aggregates_and_empty", "related_and_collection_scope", "native_mean_and_result_stages", "assignment_and_atomicity", "case_null_and_corruption", "cache_context_and_session", "native_failure_cleanup", "resource_budgets", "typed_dynamic_duration_scaling", "selected_fraction_stages_and_page", "fraction_groups_distinct_and_collections", "factor_domains_and_boundary_rounding", "scaling_assignment_savepoint_and_cache", "fraction_authority_context_and_cursor_failures"} {
			required = append(required, "TestTemporalRuntime/"+vendor+"/"+name)
		}
		if references != "" {
			required = append(required, "TestTemporalReference/"+vendor)
			for _, name := range temporalReferenceCases {
				required = append(required, "TestTemporalReference/"+vendor+"/"+name)
			}
		}
	}
	output := runStrictGeneratedCommandWithCapture(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestTemporal(Reference|Runtime)$", "./consumer"), func(output []byte) error {
		if references == "" {
			return nil
		}
		// Retain the untruncated native child events alongside this owner's
		// independent reference, including failed executions. A later audit can
		// check each run/pass or inspect the exact synthetic failure itself.
		file, err := os.OpenFile(filepath.Join(references, "godj-temporal-native.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(output)
		closeErr := file.Close()
		return errors.Join(writeErr, closeErr)
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
			t.Fatal("foreign temporal consumer package")
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
		t.Fatal("incomplete temporal consumer inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate temporal consumer execution")
		}
	}
	t.Logf("temporal generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"integer_duration", `_ = orm.Duration(orm.F(records.EventFields.Span)).Add(int64(1))`, "duration.Duration"},
		{"nanosecond_duration", `_ = orm.Duration(orm.F(records.EventFields.Span)).Add(time.Second)`, "duration.Duration"},
		{"instant_plus_instant", `_ = orm.DateTime(orm.F(records.EventFields.Finish)).AddExpression(orm.F(records.EventFields.Start))`, "scalarOperand"},
		{"duration_minus_instant", `_ = orm.Duration(orm.F(records.EventFields.Span)).SubtractExpression(orm.F(records.EventFields.Start))`, "scalarOperand"},
		{"foreign_temporal_model", `_ = orm.DateTime(orm.F(records.EventFields.Finish)).AddExpression(orm.F(planning.ScheduleFields.Offset))`, "scalarOperand"},
		{"wrong_duration_average", `_ = orm.Aggregate1(orm.Avg(orm.Duration(orm.F(records.EventFields.Span))),func(value orm.Optional[float64])float64{return 0})`, "does not match"},
		{"wrong_datetime_projection", `_ = orm.Project1(orm.DateTime(orm.F(records.EventFields.Finish)),func(value time.Time)time.Time{return value})`, "does not match"},
		{"wrong_temporal_assignment", `_ = orm.AssignExpression(records.EventFields.Finish,orm.Duration(orm.F(records.EventFields.Span)))`, "does not match"},
		{"foreign_temporal_assignment", `_ = orm.AssignExpression(records.EventFields.Finish,orm.DateTime(orm.F(planning.ScheduleFields.Anchor)))`, "does not match"},
		{"numeric_duration", `_ = orm.Numeric(orm.F(records.EventFields.Span))`, "ArithmeticNumber"},
		{"boolean_duration_factor", `_ = orm.ScaleDuration(orm.F(records.EventFields.Span),true)`, "DurationFactor"},
		{"nanosecond_duration_factor", `_ = orm.ScaleDuration(orm.F(records.EventFields.Span),time.Second)`, "DurationFactor"},
		{"instant_duration_factor", `_ = orm.ScaleDurationExpressions(orm.F(records.EventFields.Span),orm.F(records.EventFields.Finish))`, "DurationFactor"},
		{"foreign_duration_factor", `_ = orm.ScaleDurationExpressions(orm.F(records.EventFields.Span),orm.F(planning.ScheduleFields.ID))`, "does not match"},
		{"wrong_scaled_projection", `_ = orm.Project1(orm.ScaleDuration(orm.F(records.EventFields.Span),int64(2)),func(value orm.Optional[float64])float64{return 0})`, "does not match"},
		{"wrong_ratio_assignment", `_ = orm.AssignExpression(records.EventFields.Span,orm.Duration(orm.F(records.EventFields.Span)).Ratio(duration.FromMicroseconds(1)))`, "does not match"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			program := "package invalid\nimport(\"time\";\"example.com/godj-project-bundle/records\";\"example.com/godj-project-bundle/planning\";\"github.com/progresshans/godj/duration\";\"github.com/progresshans/godj/orm\")\nfunc reject(){ _=records.EventFields; _=planning.ScheduleFields; _=time.Time{}; _=duration.Duration{};" + invalid.expression + "\n}\n"
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid temporal program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
