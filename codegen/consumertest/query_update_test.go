package codegen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/schema"
)

func queryUpdateProjectSpec(t *testing.T) codegen.ProjectSpec {
	t.Helper()
	spec := bulkCreateProjectSpec(t)
	reference, err := schema.Build(schema.Definition{AppLabel: "owners", Models: []schema.Model{
		{Name: "query_update_group", GoName: "QueryUpdateGroup", DBTable: "gdj_query_update_group", Fields: []schema.Field{schema.CharField("name", "Name", 32)}},
		{Name: "query_update_item", GoName: "QueryUpdateItem", DBTable: "gdj_query_update_item", Fields: []schema.Field{
			schema.CharField("name", "Name", 32, schema.Unique()), schema.IntegerField("amount", "Amount", schema.Default(int64(0))),
			schema.IntegerField("other", "Other", schema.Default(int64(0))), schema.IntegerField("rank", "Rank", schema.Nullable()),
			schema.CharField("note", "Note", 32, schema.Nullable()), schema.BooleanField("enabled", "Enabled", schema.Default(true)),
			schema.FloatField("score", "Score", schema.Default(float64(0))), schema.DecimalField("price", "Price", 9, 2, schema.Default(decimal.Decimal{})),
			schema.ForeignKey("group", "GroupID", schema.Target("owners", "query_update_group"), schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("peers", "Peers", schema.Target("owners", "query_update_item"), schema.NoReverse(), schema.Directed(), schema.Through(schema.Target("owners", "query_update_peer"), "source", "target"))}},
		{Name: "query_update_peer", GoName: "QueryUpdatePeer", DBTable: "gdj_query_update_peer", Fields: []schema.Field{
			schema.ForeignKey("source", "SourceID", schema.Target("owners", "query_update_item"), schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("target", "TargetID", schema.Target("owners", "query_update_item"), schema.NoReverse(), schema.Cascade),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, reference.Models...)
	return spec
}

func TestGeneratedQueryUpdate(t *testing.T) {
	bundle, err := codegen.GenerateProject(queryUpdateProjectSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "single_object_test.go", "query_update_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestQueryUpdate"}
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		required = append(required, "TestQueryUpdate/"+backend)
		for _, name := range []string{"typed_dynamic_and_native_count", "cache_and_eager_ownership", "atomic_failure_and_parent_savepoint", "primary_key_and_empty_shapes", "all_concrete_field_references"} {
			required = append(required, "TestQueryUpdate/"+backend+"/"+name)
		}
	}
	output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^TestQueryUpdate$", "./consumer"))
	assertGeneratedConsumerTests(t, output, required...)
	checkQueryUpdateEvents(t, output, required)
	for _, invalid := range []struct{ name, expression, diagnostic string }{
		{"wrong_value_kind", `_,_ = source.Update(ctx,orm.Assign(owners.QueryUpdateItemFields.Amount,"wrong"))`, "as int64 value in argument to orm.Assign"},
		{"foreign_assignment", `_,_ = source.Update(ctx,orm.Assign(owners.QueryUpdateGroupFields.Name,"foreign"))`, "UpdateAssignment"},
		{"foreign_expression", `_,_ = source.Update(ctx,orm.AssignExpression(owners.QueryUpdateItemFields.Name,orm.F(owners.QueryUpdateGroupFields.Name)))`, "does not match"},
		{"null_nonnullable", `_,_ = source.Update(ctx,orm.AssignNull(owners.QueryUpdateItemFields.Amount))`, "does not match orm.NullableReferenceField"},
		{"foreign_key_object", `_,_ = source.Update(ctx,orm.Assign(owners.QueryUpdateItemFields.GroupID,owners.QueryUpdateGroup{}))`, "does not match"},
		{"foreign_key_required_null", `_ = orm.AssignNull(owners.QueryUpdatePeerFields.SourceID)`, "does not match orm.NullableReferenceField"},
		{"boolean_arithmetic", `_ = orm.Add(orm.F(owners.QueryUpdateItemFields.Enabled),true)`, "ArithmeticNumber"},
		{"decimal_arithmetic", `_ = orm.Add(orm.F(owners.QueryUpdateItemFields.Price),decimal.Decimal{})`, "ArithmeticNumber"},
		{"float_remainder", `_ = orm.Remainder(orm.F(owners.QueryUpdateItemFields.Score),int64(2))`, "does not match"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, invalid.name+"/invalid.go", []byte("package invalid\nimport (\"context\";\"example.com/godj-project-bundle/owners\";\"example.com/godj-project-bundle/project\";\"github.com/progresshans/godj/orm\";\"github.com/progresshans/godj/decimal\")\nfunc invalidInput(){ctx:=context.Background();api,_:=project.Using(nil);source:=api.OwnersQueryUpdateItem;_=ctx;_=source;_=owners.QueryUpdateItem{};_=orm.Value[owners.QueryUpdateItem](int64(1));_=decimal.Decimal{};"+invalid.expression+"}\n"))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./"+invalid.name).CombinedOutput()
			if err == nil || !strings.Contains(string(output), invalid.diagnostic) {
				t.Fatalf("invalid expression compiled or failed elsewhere: %v\n%s", err, output)
			}
		})
	}
}

func checkQueryUpdateEvents(t *testing.T, output []byte, required []string) {
	t.Helper()
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
			t.Fatal("foreign generated query update package", event)
		}
		if event.Test != "" && event.Action == "run" {
			runs[event.Test]++
		}
		if event.Test != "" && event.Action == "pass" {
			passes[event.Test]++
		}
	}
	if !maps.Equal(runs, passes) || len(runs) != len(required) {
		t.Fatal("query update consumer inventory did not finish exactly", len(runs), len(required))
	}
	for _, count := range runs {
		if count != 1 {
			t.Fatal("query update consumer repeated a test")
		}
	}
	t.Logf("query update generated consumer: runs=%d passes=%d required=%d skips=0 log_sha256=%x", len(runs), len(passes), len(required), sha256.Sum256(output))
}
