package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func customSinglePrefetchSpec() codegen.ProjectSpec {
	spec := manyCollectionSpec()
	owner := ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}
	field := ir.Field{Name: "owner", GoName: "OwnerID", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{Target: owner, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}}
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, ir.Model{Name: "required_owner", GoName: "RequiredOwner", Fields: []ir.Field{field, {Name: "amount", GoName: "Amount", Kind: ir.FieldInteger}}})
	field = field.Clone()
	field.Relation.Cardinality = ir.RelationOneToOne
	field.Relation.Reverse = ir.ReverseRelation{Name: "badge"}
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, ir.Model{Name: "badge", GoName: "Badge", Fields: []ir.Field{field, {Name: "name", GoName: "Name", Kind: ir.FieldText}}})
	return spec
}

func TestGeneratedCustomSinglePrefetch(t *testing.T) {
	bundle, err := codegen.GenerateProject(customSinglePrefetchSpec())
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "sessions_test.go", "queries_test.go", "prefetch_test.go", "prefetch_tree_test.go", "prefetch_filtered_test.go", "prefetch_custom_single_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "many-to-many-django61-sqlite.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/django-query.json", data)
	var required []string
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, name := range []string{"required_filtered", "required_eager_custom_children", "required_eager_explicit_children", "required_join_duplicates", "nullable_filtered", "reverse_filtered", "target_eager", "validation", "refinement_and_independent_cache", "failure_cancel_retry", "foreign_and_cardinality", "loaded_absence_preserves_foreign_key", "native_required_absence", "concurrent_warm", "session_lifetime"} {
			required = append(required, "TestCustomSinglePrefetch/"+backend+"/"+name)
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-run", "^TestCustomSinglePrefetch$", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
