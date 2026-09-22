package codegen_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func manyCollectionSpec() codegen.ProjectSpec {
	owner := ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}
	label := ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}
	fk := func(name, goName string, target ir.ModelIdentity, nullable bool, policy ir.DeletePolicy) ir.Field {
		return ir.Field{Name: name, GoName: goName, Kind: ir.FieldForeignKey, Nullable: nullable, Relation: &ir.ForeignKeyRelation{Target: target, Cardinality: ir.RelationManyToOne, OnDelete: policy, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	through := func(name, goName string, unique bool) ir.Model {
		result := ir.Model{Name: name, GoName: goName, Fields: []ir.Field{fk("owner", "OwnerID", owner, true, ir.DeleteCascade), fk("label", "LabelID", label, true, ir.DeleteCascade), {Name: "amount", GoName: "Amount", Kind: ir.FieldInteger, Unique: unique}}}
		if unique {
			result.UniqueConstraints = []ir.UniqueConstraint{{Name: "pair", Fields: []string{"owner", "label"}}}
		}
		return result
	}
	source := ir.Model{Name: "owner", GoName: "Owner", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldText}}, ManyToMany: []ir.ManyToManyField{
		{Name: "labels", GoName: "Labels", Target: label, Reverse: ir.ReverseRelation{Name: "owners"}},
		{Name: "ranked", GoName: "Ranked", Target: label, Reverse: ir.ReverseRelation{Name: "ranked_owners"}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "owners", ModelName: "ranked_link"}, SourceField: "owner", TargetField: "label"}},
		{Name: "loose", GoName: "Loose", Target: label, Reverse: ir.ReverseRelation{Name: "loose_owners"}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "owners", ModelName: "loose_link"}, SourceField: "owner", TargetField: "label"}},
		{Name: "friends", GoName: "Friends", Target: owner},
		{Name: "follows", GoName: "Follows", Target: owner, Symmetry: ir.ManyToManyDirected, Reverse: ir.ReverseRelation{Name: "followers"}},
	}}
	models := []ir.Model{source, through("ranked_link", "RankedLink", true), through("loose_link", "LooseLink", false)}
	for _, policy := range []struct {
		name, goName string
		policy       ir.DeletePolicy
		nullable     bool
	}{{"protected", "Protected", ir.DeleteProtect, false}, {"cascade", "Cascade", ir.DeleteCascade, false}, {"optional", "Optional", ir.DeleteSetNull, true}} {
		models = append(models, ir.Model{Name: policy.name, GoName: policy.goName, Fields: []ir.Field{fk("link", "LinkID", ir.ModelIdentity{AppLabel: "owners", ModelName: "ranked_link"}, policy.nullable, policy.policy)}})
	}
	return codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"}, Apps: []codegen.AppSpec{
		{Alias: "owners", Package: codegen.PackageSpec{PackageName: "owners", ImportPath: "example.com/godj-project-bundle/owners", Directory: "owners"}, Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "owners", Models: models}},
		{Alias: "labels", Package: codegen.PackageSpec{PackageName: "labels", ImportPath: "example.com/godj-project-bundle/labels", Directory: "labels"}, Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "labels", Models: []ir.Model{{Name: "label", GoName: "Label", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldText}}}}}},
	}}
}

func TestGeneratedManyToManyCollections(t *testing.T) {
	bundle, err := codegen.GenerateProject(manyCollectionSpec())
	if err != nil {
		t.Fatal(err)
	}
	directory := writeProjectBundleModule(t, bundle)
	source, err := os.ReadFile(filepath.Join("testdata", "manytomany", "consumer_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/collections_test.go", source)
	sessions, err := os.ReadFile(filepath.Join("testdata", "manytomany", "sessions_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/sessions_test.go", sessions)

	output, err := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "-json", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("actual generated collection consumer failed: %v\n%s", err, output)
	}
	required := map[string]bool{"TestCollections/sqlite": false, "TestCollectionBindingAndCallbackOwnership": false}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" || os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
		required["TestCollections/postgres"] = false
	}
	for _, backend := range []string{"sqlite", "postgres"} {
		if _, enabled := required["TestCollections/"+backend]; !enabled {
			continue
		}
		for _, name := range []string{"add_reverse_cache_presence", "retained_set_clear_failure_rollback", "nullable_payload_and_incoming_policy", "nonunique_multiplicity", "symmetry_and_directed_reverse", "concurrent_duplicate_and_cache", "trigger_noop_and_cancel_rollback"} {
			required["TestCollections/"+backend+"/"+name] = false
		}
	}
	for _, mode := range []string{"zero", "twice", "swallow", "late"} {
		required["TestCollectionBindingAndCallbackOwnership/"+mode] = false
	}
	for _, backend := range []string{"sqlite", "postgres"} {
		if _, enabled := required["TestCollections/"+backend]; !enabled {
			continue
		}
		required["TestCollectionFacadeSessions/"+backend] = false
		for _, commit := range []string{"false", "true"} {
			for _, coordinated := range []string{"false", "true"} {
				required["TestCollectionFacadeSessions/"+backend+"/borrowed_composition/coordinated_"+coordinated+"_commit_"+commit] = false
			}
			for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
				required["TestCollectionFacadeSessions/"+backend+"/query_and_model_lifetime/"+mode+"_commit_"+commit] = false
			}
		}

		for _, name := range []string{"facade_cache_and_origin", "borrowed_composition", "query_and_model_lifetime"} {
			required["TestCollectionFacadeSessions/"+backend+"/"+name] = false
		}
	}
	runs := map[string]int{}
	passes := map[string]int{}
	packages := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		var e struct{ Action, Test, Package string }
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("invalid child event %s", line)
		}
		if e.Action == "fail" || e.Action == "skip" && e.Test != "" {
			t.Fatalf("non-passing child event %s", line)
		}
		if e.Action == "run" {
			runs[e.Test]++
		}
		if e.Action == "pass" && e.Test != "" {
			passes[e.Test]++
		}
		if e.Test == "" && (e.Action == "pass" || e.Action == "skip") {
			packages[e.Package] = true
		}
	}
	for name := range required {
		if runs[name] != 1 || passes[name] != 1 {
			t.Fatal("missing required generated execution", name, runs[name], passes[name])
		}
	}
	for name, count := range runs {
		if count != 1 || passes[name] != 1 {
			t.Fatal("incomplete child execution", name)
		}
	}
	for _, name := range []string{"owners", "labels", "project", "consumer"} {
		if !packages["example.com/godj-project-bundle/"+name] {
			t.Fatal("missing child package", name)
		}
	}
	t.Logf("actual generated consumer: %d run = pass, no test skips", len(runs))
}

func TestGeneratedCollectionFacadeRejectsNamespaceAndCrossModelInputs(t *testing.T) {
	for _, kind := range []string{"method", "promoted"} {
		t.Run(kind, func(t *testing.T) {
			spec := manyCollectionSpec()
			if kind == "method" {
				spec.Apps[0].Schema.Models[0].ManyToMany[0].GoName = "Save"
			} else {
				spec.Apps[0].Schema.Models[0].Fields[0].GoName = "Labels"
			}
			bundle, err := codegen.GenerateProject(spec)
			if err == nil || len(bundle.Files()) != 0 {
				t.Fatal("collection namespace collision published bytes", err)
			}
		})
	}
	bundle, err := codegen.GenerateProject(manyCollectionSpec())
	if err != nil {
		t.Fatal(err)
	}
	directory := writeProjectBundleModule(t, bundle)
	writeGeneratedTestFile(t, directory, "consumer/wrong.go", []byte(`package consumer
import("context";"example.com/godj-project-bundle/project")
func wrong(source *project.OwnersOwnerLabelsCollection, owner *project.OwnersOwner)error{return source.Add(context.Background(),[]*project.OwnersOwner{owner})}
`))
	output, err := generatedGoCommand(t.Context(), directory, "test", "-run", "^$", "./...").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("cannot use")) || !bytes.Contains(output, []byte("LabelsLabel")) {
		t.Fatalf("cross-model collection input was not rejected: %v\n%s", err, output)
	}
}
