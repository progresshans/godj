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

func collectionAggregateProject(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	catalog, err := schema.Build(schema.Definition{AppLabel: "catalog", Models: []schema.Model{
		{Name: "category", GoName: "Category", DBTable: "gdj_collection_category", Fields: []schema.Field{schema.CharField("code", "Code", 24, schema.Unique())}},
		{Name: "label", GoName: "Label", DBTable: "gdj_collection_label", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()),
			schema.ForeignKey("category", "CategoryID", schema.Target("catalog", "category"), schema.RelatedName("labels"), schema.SetNull, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := schema.Build(schema.Definition{AppLabel: "records", Models: []schema.Model{
		{Name: "ticket", GoName: "Ticket", DBTable: "gdj_collection_ticket", Fields: []schema.Field{
			schema.CharField("code", "Code", 24, schema.Unique()), schema.IntegerField("amount", "Amount", schema.Nullable()), schema.BooleanField("enabled", "Enabled", schema.Nullable()),
			schema.FloatField("score", "Score", schema.Nullable()), schema.DecimalField("price", "Price", 14, 2, schema.Nullable()), schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
			schema.BinaryField("binary", "Binary", schema.Nullable()), schema.UUIDField("identity", "Identity", schema.Nullable()),
			schema.DateField("date", "Date", schema.Nullable()), schema.TimeField("time", "Time", schema.Nullable()), schema.DateTimeField("stamp", "Stamp", schema.Nullable()), schema.JSONField("document", "Document", schema.Nullable()),
			schema.ForeignKey("category", "CategoryID", schema.Target("catalog", "category"), schema.RelatedName("tickets"), schema.SetNull, schema.Nullable()),
		}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("labels", "Labels", schema.Target("catalog", "label"), schema.RelatedName("tickets"), schema.Through(schema.Target("records", "link"), "ticket", "label"))}},
		{Name: "link", GoName: "Link", DBTable: "gdj_collection_link", Fields: []schema.Field{
			schema.ForeignKey("ticket", "TicketID", schema.Target("records", "ticket"), schema.RelatedName("label_links"), schema.Cascade, schema.Nullable()),
			schema.ForeignKey("label", "LabelID", schema.Target("catalog", "label"), schema.RelatedName("ticket_links"), schema.Cascade, schema.Nullable()),
		}},
		{Name: "detail", GoName: "Detail", DBTable: "gdj_collection_detail", Fields: []schema.Field{
			schema.OneToOne("ticket", "TicketID", schema.Target("records", "ticket"), schema.RelatedName("detail"), schema.Cascade), schema.IntegerField("bonus", "Bonus", schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{
		{Alias: "catalog", Package: codegen.PackageSpec{PackageName: "catalog", ImportPath: "example.com/godj-project-bundle/catalog", Directory: "catalog"}, Schema: catalog},
		{Alias: "records", Package: codegen.PackageSpec{PackageName: "records", ImportPath: "example.com/godj-project-bundle/records", Directory: "records"}, Schema: records},
	}}
}

var collectionReferenceCases = []string{
	"count_and_distinct",
	"numeric_aggregates",
	"min_max",
	"distinct_numeric",
	"conditional_counts",
	"negated_count",
	"negated_root_count",
	"nullable_or_condition",
	"optional_ancestor",
	"source_filter_before_group",
	"chained_filters",
	"chained_filters_reversed",
	"root_exclusion",
	"root_exclusion_then_filter",
	"root_or_condition",
	"independent_collection_counts",
	"independent_distinct_aggregates",
	"nested_collections",
	"reverse_one_to_one_leaf",
	"source_key_isnull",
	"global_aggregates",
	"empty_global",
	"empty_groups",
	"having_zero",
	"having_filtered",
	"duration_average_having",
	"order_slice",
	"page_past_end",
	"derived_aggregate",
}

func TestGeneratedCollectionAggregates(t *testing.T) {
	bundle, err := codegen.GenerateProject(collectionAggregateProject(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"backend_test.go", "observations_test.go", "runtime_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "collectionaggregate", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	profile := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/collection_aggregate_reference")
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
	references := os.Getenv("GODJ_COLLECTION_AGGREGATE_REFERENCE_DIR")
	if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" && references == "" {
		t.Fatal("native consumer owner lacks its current Django reference")
	}
	if references != "" {
		data, err := os.ReadFile(filepath.Join("testdata", "collectionaggregate", "reference_test.go"))
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
					Name  string
					Error json.RawMessage
				}
			}
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if document.Kind != "django-collection-aggregate-reference-v1" || document.Backend != vendor || document.Commit != "fe0a859f537d4238cf49fca39073513206f83122" ||
				document.Observer != hashes["observe.py"] || document.Input != hashes["input.json"] || document.Sources != hashes["source-verification.json"] || !document.Unchanged || !document.Cleaned {
				t.Fatal("collection reference is not bound to the current observer/input/source or its cleanup", vendor)
			}
			if document.Runtime.Python != "3.14.3" || document.Runtime.Django != "6.1" || document.Runtime.SQLite == "" || document.Runtime.Timezone != "UTC" ||
				(vendor == "postgres" && (!strings.HasPrefix(document.Runtime.PostgreSQL, "17.10") || document.Runtime.Psycopg != "3.3.6")) {
				t.Fatal("collection reference runtime differs", vendor, document.Runtime)
			}
			var names []string
			for _, record := range document.Cases {
				if len(record.Error) != 0 {
					t.Fatal("independent observer case failed", vendor, record.Name)
				}
				names = append(names, record.Name)
			}
			if !slices.Equal(names, collectionReferenceCases) {
				t.Fatal("collection reference inventory differs", vendor, names)
			}
			writeGeneratedTestFile(t, root, "consumer/django61-"+vendor+".json", data)
			t.Logf("collection reference %s: sha256=%x observer=%s input=%s sources=%s cases=%d", vendor, sha256.Sum256(data), document.Observer, document.Input, document.Sources, len(names))
		}
	}
	required := []string{"TestCollectionRuntime"}
	if references != "" {
		required = append(required, "TestCollectionReference")
	}
	for _, vendor := range []string{"sqlite", "postgres"} {
		if vendor == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestCollectionRuntime/"+vendor)
		for _, name := range []string{"typed_dynamic_and_nullable_values", "join_multiplicity_filter_scopes_and_parameters", "result_identity_having_page_and_derived", "source_scope_invalid_metadata_and_bounds", "cache_context_and_cursor_failures", "borrowed_session_lifetime", "native_numeric_domains_and_failures", "ordered_field_codecs"} {
			required = append(required, "TestCollectionRuntime/"+vendor+"/"+name)
		}
		if references != "" {
			required = append(required, "TestCollectionReference/"+vendor)
			for _, name := range collectionReferenceCases {
				required = append(required, "TestCollectionReference/"+vendor+"/"+name)
			}
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestCollection(Reference|Runtime)$", "./consumer"))
	if references != "" {
		// Retain the untruncated native child events alongside this owner's
		// independent reference. A later audit can check each run/pass itself.
		file, err := os.OpenFile(filepath.Join(references, "godj-collection-native.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(output)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot preserve collection native events", writeErr, closeErr)
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
			t.Fatal("foreign collection consumer package")
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
		t.Fatal("incomplete collection consumer inventory", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("duplicate collection consumer execution")
		}
	}
	t.Logf("collection generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"foreign_count_model", `_ = orm.Count[records.Ticket](relations.CatalogLabel.Tickets.ID)`, "wrong type for method countAggregateField"},
		{"foreign_sum_model", `_ = orm.Sum[records.Ticket](relations.CatalogLabel.Tickets.Amount)`, "does not match"},
		{"foreign_min_model", `_ = orm.Min[records.Ticket](relations.CatalogLabel.Tickets.Code)`, "does not match"},
		{"wrong_nullable_decoder", `_ = orm.Aggregate1(orm.Min(relations.CatalogLabel.Tickets.Amount),func(value int64)int64{return value})`, "does not match"},
		{"boolean_minimum", `_ = orm.Min(relations.CatalogLabel.Tickets.Enabled)`, "does not match"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			program := "package invalid\nimport(\"example.com/godj-project-bundle/records\";\"example.com/godj-project-bundle/project\";\"github.com/progresshans/godj/orm\")\nfunc reject(){ relations,_:=project.BindRelations(); _=relations; _=records.TicketFields;" + invalid.expression + "\n}\n"
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte(program))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid collection program compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}
