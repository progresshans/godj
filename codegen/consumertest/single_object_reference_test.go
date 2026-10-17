package codegen_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedSingleObjectReference(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "singleobject", Models: []schema.Model{
		{Name: "category", GoName: "Category", DBTable: "gdj_single_object_category", Fields: []schema.Field{schema.CharField("name", "Name", 32)}},
		{Name: "label", GoName: "Label", DBTable: "gdj_single_object_label", Fields: []schema.Field{
			schema.CharField("name", "Name", 32),
			schema.ForeignKey("category", "CategoryID", schema.Target("singleobject", "category"), schema.RelatedName("labels"), schema.Cascade),
			schema.CharField("detail", "Detail", 64, schema.Default("")),
			schema.CharField("external", "External", 32, schema.Unique(), schema.Nullable()),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "single_object_category_name", Fields: []string{"category", "name"}}}},
		{Name: "race_label", GoName: "RaceLabel", DBTable: "gdj_single_object_race_label", Fields: []schema.Field{schema.CharField("name", "Name", 32, schema.Unique()), schema.CharField("detail", "Detail", 32)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-single-object-reference"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"},
		Apps:    []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	observer, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/single_object_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(observer)
	getCases := []string{"single", "missing", "multiple", "warm_get_refresh", "warm_get_after_delete", "unsliced_order_removed", "slice_one", "slice_offset", "empty_slice", "empty_predicate", "many_matches"}
	creationCases := []string{"create", "existing", "invalid_create_default", "unrelated_unique_conflict", "nested_unique_failure", "release_is_not_commit", "multiple_matches", "create_outside_filter", "final_rows"}
	for _, name := range []string{"consumer_test.go", "race_test.go", "django61-sqlite.json", "django61-postgres.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", "singleobject", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".json") {
			var reference struct {
				Observer      string `json:"observer_sha256"`
				Get, Creation map[string]json.RawMessage
			}
			if err := json.Unmarshal(data, &reference); err != nil || reference.Observer != hex.EncodeToString(digest[:]) {
				t.Fatal("reference observer binding", name, err)
			}
			for _, inventory := range []struct {
				names []string
				cases map[string]json.RawMessage
			}{{getCases, reference.Get}, {creationCases, reference.Creation}} {
				if len(inventory.names) != len(inventory.cases) {
					t.Fatal("reference inventory changed", name)
				}
				for _, required := range inventory.names {
					if _, exists := inventory.cases[required]; !exists {
						t.Fatal("reference case missing", required)
					}
				}
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestSingleObjectReference", "TestSingleObjectCreationRace"}
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, item := range []struct {
			group string
			names []string
		}{{"get", getCases}, {"creation", creationCases}} {
			for _, name := range item.names {
				required = append(required, "TestSingleObjectReference/"+backend+"/"+item.group+"/"+name)
			}
		}
		required = append(required, "TestSingleObjectCreationRace/"+backend)
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
