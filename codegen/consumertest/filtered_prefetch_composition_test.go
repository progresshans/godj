package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedFilteredPrefetchComposition(t *testing.T) {
	spec := manyCollectionSpec()
	spec.Apps[1].Schema.Models[0].Fields = append(spec.Apps[1].Schema.Models[0].Fields, ir.Field{Name: "featured_owner", GoName: "FeaturedOwnerID", Kind: ir.FieldForeignKey, Nullable: true, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "labels", ModelName: "featured_owner"}, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteSetNull, Reverse: ir.ReverseRelation{Disabled: true}}})
	spec.Apps[1].Schema.Models = append([]ir.Model{{Name: "featured_owner", GoName: "FeaturedOwner", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldText}}}}, spec.Apps[1].Schema.Models...)
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "sessions_test.go", "queries_test.go", "prefetch_test.go", "prefetch_tree_test.go", "prefetch_filtered_test.go", "prefetch_composition_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestFilteredPrefetchComposition/sqlite/snapshot_model_derivation", "TestFilteredPrefetchComposition/sqlite/many_target_eager", "TestFilteredPrefetchComposition/sqlite/single_child", "TestFilteredPrefetchComposition/sqlite", "TestFilteredPrefetchComposition/sqlite/eager_and_children", "TestFilteredPrefetchComposition/sqlite/failure_retry", "TestFilteredPrefetchComposition/sqlite/session"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" || os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
		for _, name := range append([]string(nil), required...) {
			required = append(required, strings.Replace(name, "sqlite", "postgres", 1))
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-run", "^TestFilteredPrefetchComposition$", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
