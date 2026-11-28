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

func groupedProjectSpec(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	model, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "group", GoName: "Group", DBTable: "gdj_grouped_group", Fields: []schema.Field{schema.CharField("name", "Name", 32), schema.CharField("region", "Region", 32, schema.Nullable())}},
		{Name: "item", GoName: "Item", DBTable: "gdj_grouped_item", Fields: []schema.Field{
			schema.CharField("name", "Name", 32), schema.IntegerField("amount", "Amount"), schema.IntegerField("rank", "Rank", schema.Nullable()), schema.CharField("note", "Note", 32, schema.Nullable()), schema.BooleanField("enabled", "Enabled"),
			schema.FloatField("score", "Score"), schema.DecimalField("price", "Price", 9, 2), schema.UUIDField("identity", "Identity"), schema.BinaryField("binary", "Binary"),
			schema.DateField("date", "Date"), schema.TimeField("time", "Time"), schema.DateTimeField("stamp", "Stamp"), schema.DurationField("duration", "Duration"), schema.JSONField("document", "Document"),
			schema.ForeignKey("group", "GroupID", schema.Target("records", "group"), schema.RelatedName("items"), schema.Cascade, schema.Nullable()),
		}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("peers", "Peers", schema.Target("records", "item"), schema.NoReverse(), schema.Directed(), schema.Through(schema.Target("records", "peer"), "source", "target"))}},
		{Name: "peer", GoName: "Peer", DBTable: "gdj_grouped_peer", Fields: []schema.Field{
			schema.ForeignKey("source", "SourceID", schema.Target("records", "item"), schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("target", "TargetID", schema.Target("records", "item"), schema.NoReverse(), schema.Cascade),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "records", Package: codegen.PackageSpec{PackageName: "records", ImportPath: "example.com/godj-project-bundle/records", Directory: "records"}, Schema: model}}}
}

func TestGeneratedGroupedAggregation(t *testing.T) {
	bundle, err := codegen.GenerateProject(groupedProjectSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "reference_test.go", "runtime_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "grouped", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	cases := strings.Fields(`nullable_key multi_key key_order_asc_native key_order_desc_native key_order_desc_null_first where_before_group where_after_group having_total having_opened having_or having_not having_key_not having_mixed_where_or having_nullable_min having_nullable_filtered_min having_nullable_not having_nullable_note_not having_no_groups empty_native empty_folded empty_slice page_groups page_past_end count_groups order_aggregate source_ordering retained_source_ordering default_model_ordering source_slice source_slice_inherited_ordering source_distinct distinct_fields filtered_count_star filtered_aggregates count_null_and_empty forward_key forward_nullable_key forward_filter forward_conditional_count forward_missing_conditional_count conditional_negation collection_filter collection_negation collection_key reverse_counts eager_source row_locked_group row_locked_group_in_atomic cached_snapshot missing_key missing_aggregate_field alias_conflict missing_having_alias filter_after_slice ordering_after_slice aggregate_only_empty codec_key_enabled codec_key_score codec_key_price codec_key_identity codec_key_binary codec_key_date codec_key_time codec_key_stamp codec_key_duration codec_key_document`)
	if len(cases) != 66 {
		t.Fatal("grouped reference inventory")
	}
	static := map[string]bool{"where_after_group": true, "distinct_fields": true, "alias_conflict": true, "eager_source": true}
	observer, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/grouped_aggregation_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	observerSHA := sha256.Sum256(observer)
	if hex.EncodeToString(observerSHA[:]) != "5d0977299b9c39ec9a95e3042cf4372eac76a1263e368dc0f838d1ae20047c88" {
		t.Fatal("independent observer changed")
	}
	required := []string{"TestGroupedReference", "TestGroupedRuntime"}
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "grouped-django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "grouped", name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != map[string]string{"sqlite": "e970d9164274aefe3ef6c4848e1f4f4e1afacc74f9ea1bf0e5334c4dd81abb93", "postgres": "8f3ce3ab2ac05760a9490dd939f69443300dbba1b48431107acafffd0bf99256"}[backend] {
			t.Fatal("raw reference bytes changed", backend)
		}
		var fixture struct {
			Kind, Django, Python, Backend string
			Observer                      string            `json:"observer_sha256"`
			Sources                       map[string]string `json:"source_sha256"`
			Cases                         []struct{ Case string }
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		vendor := backend
		if vendor == "postgres" {
			vendor = "postgresql"
		}
		if fixture.Kind != "django-grouped-aggregation-reference-v1" || fixture.Django != "6.1" || fixture.Python != "3.14.3" || fixture.Backend != vendor || fixture.Observer != hex.EncodeToString(observerSHA[:]) || len(fixture.Cases) != len(cases) {
			t.Fatal("reference provenance/inventory", backend)
		}
		if !maps.Equal(fixture.Sources, map[string]string{"Aggregate": "5306e824e61be06dc20c1d322586f84697f9ecec7064b33405235feae5441eb1", "Query": "dc599140baef8c28a832279007873f8dfd42c5b8aa0d4451c95aae2076425a01", "QuerySet": "5e86af673328a1d6800342f4bec9d4e2352b7919bebef349d98962d67569eae6", "SQLCompiler": "38e5ed0147669e8085f00b02646e5b7436ece76f6bb399bef4acd4ae9f07feb4"}) {
			t.Fatal("foreign upstream modules")
		}
		for i, name := range cases {
			if fixture.Cases[i].Case != name {
				t.Fatal("reference inventory order", name)
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestGroupedReference/"+backend, "TestGroupedRuntime/"+backend)
		for _, name := range cases {
			if !static[name] {
				required = append(required, "TestGroupedReference/"+backend+"/"+name)
			}
		}
		for _, name := range []string{"typed_dynamic_plan_and_decoding", "typed_forward_keys_and_count", "page_snapshot_and_cache", "native_ordered_codecs", "source_guards_before_io", "borrowed_scope_and_cancellation", "read_only_transaction"} {
			required = append(required, "TestGroupedRuntime/"+backend+"/"+name)
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestGrouped(Reference|Runtime)$", "./consumer"))
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
			t.Fatal("foreign grouped consumer package")
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
		t.Fatal("grouped consumer incomplete inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate grouped consumer test")
		}
	}
	t.Logf("grouped generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"wrong_key_model", `_,_ = project.GroupRecordsItemBy(source,orm.Project1(records.GroupFields.Name,func(v string)string{return v}),aggregate,func(k string,n int64)int64{return n})`, "does not match"},
		{"wrong_aggregate_model", `_,_ = project.GroupRecordsItemBy(source,keys,orm.Aggregate1(orm.CountRows[records.Group](),func(v int64)int64{return v}),func(k *int64,n int64)int64{return n})`, "does not match"},
		{"wrong_having_value", `_ = orm.CountRows[records.Item]().GreaterThan("wrong")`, "as int64 value"},
		{"foreign_order", `_ = grouped.OrderBy(records.GroupFields.Name.Asc())`, "GroupOrder"},
		{"where_after_group", `_ = grouped.Filter(records.ItemFields.Enabled.Exact(true))`, "Filter undefined"},
		{"distinct_fields", `_ = source.Distinct(records.ItemFields.Rank)`, "too many arguments"},
		{"alias_conflict", `_,_ = source.GroupValues([]string{"rank"},[]orm.DynamicAggregateInput{{Kind:query.ResultCountAll}},"rank")`, "too many arguments"},
		{"eager_source", `_,_ = project.GroupRecordsItemBy(source.SelectRelated(source.Related.Group),keys,aggregate,func(k *int64,n int64)int64{return n})`, "RecordsItemQuery"},
		{"external_selector", `_ = grouped.OrderBy(struct{}{})`, "GroupOrder"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(`package invalid
import("example.com/godj-project-bundle/records";"example.com/godj-project-bundle/project";"github.com/progresshans/godj/orm";"github.com/progresshans/godj/query")
func reject(){api,_:=project.Using(nil);source:=api.RecordsItem;keys:=orm.Project1(records.ItemFields.Rank,func(v *int64)*int64{return v});aggregate:=orm.Aggregate1(orm.CountRows[records.Item](),func(v int64)int64{return v});grouped,_:=project.GroupRecordsItemBy(source,keys,aggregate,func(k *int64,n int64)int64{return n});_=grouped;_=query.ResultCountAll
`+invalid.expression+"\n}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid group program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
