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

func calendarExtractionProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	planning, err := schema.Build(schema.Definition{AppLabel: "planning", Models: []schema.Model{
		{Name: "schedule", GoName: "Schedule", DBTable: "gdj_calendar_schedule", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.DateTimeField("anchor", "Anchor", schema.Nullable()),
			schema.DateField("day", "Day", schema.Nullable()), schema.TimeField("wall", "Wall", schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "event", GoName: "Event", DBTable: "gdj_calendar_event", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.CharField("bucket", "Bucket", 24), schema.BooleanField("enabled", "Enabled"),
			schema.DateTimeField("start", "Start"), schema.DateTimeField("at", "At", schema.Nullable()),
			schema.DateField("day", "Day", schema.Nullable()), schema.TimeField("wall", "Wall", schema.Nullable()), schema.IntegerField("part", "Part", schema.Nullable()),
			schema.ForeignKey("schedule", "ScheduleID", schema.Target("planning", "schedule"), schema.RelatedName("events"), schema.SetNull, schema.Nullable()),
		}},
		{Name: "detail", GoName: "Detail", DBTable: "gdj_calendar_detail", Fields: []schema.Field{
			schema.OneToOne("event", "EventID", schema.Target("records", "event"), schema.RelatedName("detail"), schema.Cascade),
			schema.DateTimeField("at", "At", schema.Nullable()), schema.DateField("day", "Day", schema.Nullable()), schema.TimeField("wall", "Wall", schema.Nullable()),
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

var calendarReferenceCases = []string{
	"datetime_date",
	"datetime_time",
	"datetime_year",
	"datetime_iso_year",
	"datetime_quarter",
	"datetime_month",
	"datetime_week",
	"datetime_week_day",
	"datetime_iso_week_day",
	"datetime_day",
	"datetime_hour",
	"datetime_minute",
	"datetime_second",
	"date_year",
	"date_iso_year",
	"date_quarter",
	"date_month",
	"date_week",
	"date_week_day",
	"date_iso_week_day",
	"date_day",
	"time_hour",
	"time_minute",
	"time_second",
	"composed_date_year",
	"composed_time_second",
	"literal_components",
	"filter_year",
	"filter_date_range",
	"order_clock",
	"group_by_date",
	"group_iso_week",
	"aggregate_parts",
	"filtered_aggregate",
	"case_date",
	"case_time",
	"date_after_shift",
	"related_parts",
	"collection_years",
	"distinct_dates",
	"literal_aggregate",
	"empty_aggregate",
	"empty_group",
	"update_calendar",
	"update_filtered",
	"invalid_date_hour",
	"invalid_time_year",
	"invalid_duration_year",
	"invalid_date_storage",
	"outside_date_storage",
	"invalid_datetime_storage",
	"infinite_datetime_storage",
	"clock_24_storage",
	"unselected_corrupt_date",
	"filtered_corrupt_date",
	"trunc_datetime_year",
	"trunc_datetime_quarter",
	"trunc_datetime_month",
	"trunc_datetime_week",
	"trunc_datetime_day",
	"trunc_datetime_hour",
	"trunc_datetime_minute",
	"trunc_datetime_second",
	"trunc_date_year",
	"trunc_date_quarter",
	"trunc_date_month",
	"trunc_date_week",
	"trunc_date_day",
	"trunc_time_hour",
	"trunc_time_minute",
	"trunc_time_second",
	"trunc_date_after_shift",
	"trunc_clock_from_instant",
	"trunc_group_month",
	"trunc_group_week",
	"trunc_filter_month",
	"trunc_distinct_month",
	"trunc_aggregate",
	"trunc_filtered_aggregate",
	"trunc_related",
	"trunc_collection",
	"trunc_case_time",
	"trunc_case_date",
	"update_truncated_calendar",
	"update_truncated_filtered",
	"invalid_trunc_date_hour",
	"invalid_trunc_time_year",
	"invalid_trunc_duration_day",
	"trunc_invalid_date_storage",
	"trunc_outside_date_storage",
	"trunc_invalid_datetime_storage",
	"trunc_infinite_datetime_storage",
	"trunc_clock_24_storage",
	"trunc_unselected_corrupt_date",
	"trunc_filtered_corrupt_date",
	"arith_date_add",
	"arith_date_reverse",
	"arith_date_subtract",
	"arith_date_difference",
	"arith_date_instant_difference",
	"arith_instant_date_difference",
	"arith_time_add",
	"arith_time_reverse",
	"arith_time_subtract",
	"arith_time_difference",
	"arith_composed_start",
	"arith_filter_start",
	"arith_related_start",
	"arith_collection_start",
	"arith_aggregate_difference",
	"arith_case_time",
	"arith_date_fraction",
	"arith_time_fraction",
	"arith_invalid_date_storage",
	"arith_clock_24_storage",
	"arith_unselected_corrupt_date",
	"arith_filtered_corrupt_date",
	"update_arithmetic_calendar",
}

func TestGeneratedCalendarExtraction(t *testing.T) {
	bundle, err := codegen.GenerateProject(calendarExtractionProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "observations_test.go", "runtime_test.go", "truncation_test.go", "calendar_arithmetic_test.go", "zoned_calendar_test.go", "arithmetic-expectations.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", "calendarextraction", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	profile := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/calendar_extraction_reference")
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
	references := os.Getenv("GODJ_CALENDAR_EXTRACTION_REFERENCE_DIR")
	if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" && references == "" {
		t.Fatal("native consumer owner lacks its current Django reference")
	}
	if references != "" {
		for _, name := range []string{"reference_test.go", "calendar_arithmetic_reference_test.go"} {
			data, err := os.ReadFile(filepath.Join("testdata", "calendarextraction", name))
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
			if document.Kind != "django-calendar-extraction-reference-v1" || document.Backend != vendor || document.Commit != "fe0a859f537d4238cf49fca39073513206f83122" ||
				document.Observer != hashes["observe.py"] || document.Input != hashes["input.json"] || document.Sources != hashes["source-verification.json"] || !document.Unchanged || !document.Cleaned {
				t.Fatal("calendar reference is not bound to the current observer/input/source or its cleanup", vendor)
			}
			if document.Runtime.Python != "3.14.3" || document.Runtime.Django != "6.1" || document.Runtime.SQLite == "" || document.Runtime.Timezone != "UTC" ||
				(vendor == "postgres" && (!strings.HasPrefix(document.Runtime.PostgreSQL, "17.10") || document.Runtime.Psycopg != "3.3.6")) {
				t.Fatal("calendar reference runtime differs", vendor, document.Runtime)
			}
			var names []string
			for _, record := range document.Cases {
				if !record.Unchanged {
					t.Fatal("independent calendar case changed storage", vendor, record.Name)
				}
				if record.Error != nil && !slices.Contains([]string{"invalid_date_hour", "invalid_time_year", "invalid_duration_year", "infinite_datetime_storage",
					"invalid_trunc_date_hour", "invalid_trunc_time_year", "invalid_trunc_duration_day", "trunc_invalid_date_storage", "trunc_outside_date_storage", "trunc_invalid_datetime_storage", "trunc_infinite_datetime_storage", "arith_invalid_date_storage"}, record.Name) {
					t.Fatal("unowned independent calendar error", vendor, record.Name, record.Error)
				}
				names = append(names, record.Name)
			}
			if !slices.Equal(names, calendarReferenceCases) {
				t.Fatal("calendar reference inventory differs", vendor, names)
			}
			writeGeneratedTestFile(t, root, "consumer/django61-"+vendor+".json", data)
			t.Logf("calendar reference %s: sha256=%x observer=%s input=%s sources=%s cases=%d", vendor, sha256.Sum256(data), document.Observer, document.Input, document.Sources, len(names))
		}
	}
	required := []string{"TestCalendarRuntime"}
	if references != "" {
		required = append(required, "TestCalendarReference")
		required = append(required, prepareZonedCalendarReference(t, root, references)...)
	}
	for _, vendor := range []string{"sqlite", "postgres"} {
		if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestCalendarRuntime/"+vendor)
		for _, name := range []string{"typed_dynamic_and_exact_values", "filter_order_and_page", "aggregates_and_empty", "related_and_collection_scope", "selected_result_stages", "assignment_and_atomicity", "case_null_and_corruption", "cache_context_and_session", "native_failure_cleanup", "resource_budgets", "truncated_typed_dynamic_and_boundaries", "truncated_groups_relations_and_stages", "truncated_updates_and_failures", "arithmetic_typed_dynamic_and_domains", "arithmetic_relations_groups_and_stages", "arithmetic_updates_savepoints_and_cache", "arithmetic_failures_context_and_cursor_ownership", "zoned_typed_dynamic_and_dst_values", "zoned_groups_relations_and_stages", "zoned_updates_boundaries_and_failures", "zoned_ownership_context_and_cursors"} {
			required = append(required, "TestCalendarRuntime/"+vendor+"/"+name)
		}
		if references != "" {
			required = append(required, "TestCalendarReference/"+vendor)
			for _, name := range calendarReferenceCases {
				required = append(required, "TestCalendarReference/"+vendor+"/"+name)
			}
		}
	}
	output := runStrictGeneratedCommandWithCapture(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^(TestCalendar(Reference|Runtime)|TestZonedCalendarReference)$", "./consumer"), func(output []byte) error {
		if references == "" {
			return nil
		}
		// Retain the untruncated native child events alongside this owner's
		// independent reference, including failed executions. A later audit can
		// check each run/pass or inspect the exact synthetic failure itself.
		file, err := os.OpenFile(filepath.Join(references, "godj-calendar-native.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
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
			t.Fatal("foreign calendar consumer package")
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
		t.Fatal("incomplete calendar consumer inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate calendar consumer execution")
		}
	}
	t.Logf("calendar generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"date_has_no_zone", `_ = orm.Date(orm.F(records.EventFields.Day)).InZone(timezone.UTC())`, "InZone undefined"},
		{"time_has_no_zone", `_ = orm.Time(orm.F(records.EventFields.Wall)).InZone(timezone.UTC())`, "InZone undefined"},
		{"duration_has_no_zone", `_ = orm.Duration(orm.Value[records.Event](duration.Duration{})).InZone(timezone.UTC())`, "InZone undefined"},
		{"zone_is_not_location", `_ = orm.DateTime(orm.F(records.EventFields.At)).InZone(time.UTC)`, "cannot use"},
		{"zone_is_not_string", `_ = orm.DateTime(orm.F(records.EventFields.At)).InZone("UTC")`, "cannot use"},
		{"zone_is_not_scalar", `_ = orm.AssignExpression(records.EventFields.At,orm.DateTime(orm.F(records.EventFields.At)).InZone(timezone.UTC()))`, "does not match"},
		{"zoned_foreign_assignment", `_ = orm.AssignExpression(records.EventFields.Day,orm.DateTime(orm.F(planning.ScheduleFields.Anchor)).InZone(timezone.UTC()).Date())`, "does not match"},
		{"date_clock_part", `_ = orm.Date(orm.F(records.EventFields.Day)).Hour()`, "Hour undefined"},
		{"time_calendar_part", `_ = orm.Time(orm.F(records.EventFields.Wall)).Year()`, "Year undefined"},
		{"duration_calendar_part", `_ = orm.Duration(orm.Value[records.Event](duration.Duration{})).Year()`, "Year undefined"},
		{"integer_date", `_ = orm.Date(orm.F(records.EventFields.Part))`, "does not match"},
		{"wrong_date_projection", `_ = orm.Project1(orm.DateTime(orm.F(records.EventFields.At)).Date(),func(value time.Time)time.Time{return value})`, "does not match"},
		{"wrong_clock_assignment", `_ = orm.AssignExpression(records.EventFields.Day,orm.DateTime(orm.F(records.EventFields.At)).Time())`, "does not match"},
		{"foreign_date_assignment", `_ = orm.AssignExpression(records.EventFields.Day,orm.DateTime(orm.F(planning.ScheduleFields.Anchor)).Date())`, "does not match"},
		{"foreign_part_comparison", `_ = orm.ExactExpressions(orm.Date(orm.F(records.EventFields.Day)).Year(),orm.Date(orm.F(planning.ScheduleFields.Day)).Year())`, "does not match"},
		{"numeric_calendar", `_ = orm.Numeric(orm.F(records.EventFields.Day))`, "ArithmeticNumber"},
		{"date_clock_trunc", `_ = orm.Date(orm.F(records.EventFields.Day)).TruncHour()`, "TruncHour undefined"},
		{"time_calendar_trunc", `_ = orm.Time(orm.F(records.EventFields.Wall)).TruncYear()`, "TruncYear undefined"},
		{"duration_trunc", `_ = orm.Duration(orm.Value[records.Event](duration.Duration{})).TruncDay()`, "TruncDay undefined"},
		{"wrong_trunc_projection", `_ = orm.Project1(orm.Date(orm.F(records.EventFields.Day)).TruncMonth(),func(value orm.Optional[time.Time])any{return value})`, "does not match"},
		{"wrong_trunc_assignment", `_ = orm.AssignExpression(records.EventFields.At,orm.Date(orm.F(records.EventFields.Day)).TruncMonth())`, "does not match"},
		{"foreign_trunc_assignment", `_ = orm.AssignExpression(records.EventFields.Day,orm.Date(orm.F(planning.ScheduleFields.Day)).TruncMonth())`, "does not match"},
		{"date_add_clock", `_ = orm.Date(orm.F(records.EventFields.Day)).AddExpression(orm.Time(orm.F(records.EventFields.Wall)))`, "wrong type for method scalarOperand"},
		{"clock_add_date", `_ = orm.Time(orm.F(records.EventFields.Wall)).AddExpression(orm.Date(orm.F(records.EventFields.Day)))`, "wrong type for method scalarOperand"},
		{"date_shift_integer", `_ = orm.Date(orm.F(records.EventFields.Day)).Add(int64(1))`, "cannot use"},
		{"date_shift_date_assignment", `_ = orm.AssignExpression(records.EventFields.Day,orm.Date(orm.F(records.EventFields.Day)).Add(duration.Duration{}))`, "does not match"},
		{"clock_difference_clock_assignment", `_ = orm.AssignExpression(records.EventFields.Wall,orm.Time(orm.F(records.EventFields.Wall)).DifferenceExpression(orm.F(records.EventFields.Wall)))`, "does not match"},
		{"foreign_calendar_operand", `_ = orm.Date(orm.F(records.EventFields.Day)).DifferenceExpression(orm.F(planning.ScheduleFields.Day))`, "wrong type for method scalarOperand"},
		{"foreign_clock_operand", `_ = orm.Time(orm.F(records.EventFields.Wall)).DifferenceExpression(orm.F(planning.ScheduleFields.Wall))`, "wrong type for method scalarOperand"},
		{"date_difference_projection", `_ = orm.Project1(orm.Date(orm.F(records.EventFields.Day)).DifferenceExpression(orm.F(records.EventFields.Day)),func(value orm.Optional[time.Time])any{return value})`, "does not match"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			program := "package invalid\nimport(\"time\";\"example.com/godj-project-bundle/records\";\"example.com/godj-project-bundle/planning\";\"github.com/progresshans/godj/duration\";\"github.com/progresshans/godj/timezone\";\"github.com/progresshans/godj/orm\")\nfunc reject(){ _=records.EventFields; _=planning.ScheduleFields; _=time.Time{}; _=duration.Duration{};_=timezone.UTC();" + invalid.expression + "\n}\n"
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid calendar program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
