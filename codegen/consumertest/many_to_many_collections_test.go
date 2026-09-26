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
		result.Fields[0].Relation.Reverse = ir.ReverseRelation{Name: name + "_rows"}
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
		{Alias: "labels", Package: codegen.PackageSpec{PackageName: "labels", ImportPath: "example.com/godj-project-bundle/labels", Directory: "labels"}, Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "labels", Models: []ir.Model{{Name: "label", GoName: "Label", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldText}, {Name: "note", GoName: "Note", Kind: ir.FieldText, Nullable: true}}}}}},
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
	queries, err := os.ReadFile(filepath.Join("testdata", "manytomany", "queries_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/queries_test.go", queries)
	prefetch, err := os.ReadFile(filepath.Join("testdata", "manytomany", "prefetch_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/prefetch_test.go", prefetch)
	tree, err := os.ReadFile(filepath.Join("testdata", "manytomany", "prefetch_tree_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/prefetch_tree_test.go", tree)
	filtered, err := os.ReadFile(filepath.Join("testdata", "manytomany", "prefetch_filtered_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/prefetch_filtered_test.go", filtered)
	single, err := os.ReadFile(filepath.Join("testdata", "manytomany", "prefetch_single_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/prefetch_single_test.go", single)
	reverse, err := os.ReadFile(filepath.Join("testdata", "manytomany", "prefetch_reverse_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/prefetch_reverse_test.go", reverse)
	oracle, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "many-to-many-django61-sqlite.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "consumer/django-query.json", oracle)

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
		required["TestCollectionQueries/"+backend] = false
		required["TestCollectionPrefetchOwnerPlans/"+backend] = false
		required["TestCollectionPrefetchTree/"+backend] = false
		required["TestSinglePrefetchComposition/"+backend] = false
		required["TestReverseCollectionPrefetch/"+backend] = false
		for _, name := range []string{"native_indexed_target", "reference_typed", "reference_path_merge", "reference_single_child", "held_query_composition", "lookup_origin_budget", "failure_foreign_cancel_retry", "independent_reset_and_concurrent", "session_lifetime"} {
			required["TestReverseCollectionPrefetch/"+backend+"/"+name] = false
		}

		for _, name := range []string{"reference_typed", "reference_path", "reference_prefetch_first", "reference_prefetch_first_path", "reference_cold_typed", "reference_cold_path", "first_refinement_count", "validation", "failure_retry", "independent_and_concurrent", "nullable_and_session"} {
			required["TestSinglePrefetchComposition/"+backend+"/"+name] = false
		}
		required["TestCollectionPrefetchFiltered/"+backend] = false
		for _, name := range []string{"reference_and_cache", "lookup_order", "refinement_first_and_fresh", "failure_membership_and_retry", "nullable_duplicates_and_distinct", "session_lifetime", "native_terminals"} {
			required["TestCollectionPrefetchFiltered/"+backend+"/"+name] = false
		}
		for _, name := range []string{"reference_typed", "reference_path", "reference_merged", "validation", "failure_retry", "independent_snapshots", "concurrent_warm", "multiplicity_and_copy", "session_lifetime"} {
			required["TestCollectionPrefetchTree/"+backend+"/"+name] = false
		}
		for _, name := range []string{"owner_second", "owner_either", "successive_owners", "successive_only_first", "successive_only_second", "distinct_owners", "excluded_owner"} {
			required["TestCollectionPrefetchOwnerPlans/"+backend+"/"+name] = false
		}
		for _, group := range []string{"query_filter_scopes", "query_boolean_presence", "query_boolean_order", "query_manager_scopes", "query_nullable_links", "binding_authority"} {
			required["TestCollectionQueries/"+backend+"/"+group] = false
		}
		for _, commit := range []string{"false", "true"} {
			for _, coordinated := range []string{"false", "true"} {
				required["TestCollectionFacadeSessions/"+backend+"/borrowed_composition/coordinated_"+coordinated+"_commit_"+commit] = false
			}
			for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
				required["TestCollectionFacadeSessions/"+backend+"/query_and_model_lifetime/"+mode+"_commit_"+commit] = false
			}
		}

		for _, name := range []string{"reference", "nullable_and_self", "facade_query", "batch_and_failures", "concurrent_query", "ordinary_session_reads", "session"} {
			required["TestCollectionPrefetch/"+backend+"/"+name] = false
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
	for _, kind := range []string{"method", "promoted", "prefetch_type", "reverse_method", "reverse_type"} {
		t.Run(kind, func(t *testing.T) {
			spec := manyCollectionSpec()
			if kind == "method" {
				spec.Apps[0].Schema.Models[0].ManyToMany[0].GoName = "Save"
			} else if kind == "promoted" {
				spec.Apps[0].Schema.Models[0].Fields[0].GoName = "Labels"
			} else if kind == "reverse_method" {
				spec.Apps[0].Schema.Models[0].Fields[0].GoName = "RankedLinkRows"
			} else if kind == "reverse_type" {
				spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, ir.Model{Name: "owner_ranked_link_rows_collection", GoName: "OwnerRankedLinkRowsCollection", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldText}}})
			} else {
				spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, ir.Model{Name: "owner_prefetch_query", GoName: "OwnerPrefetchQuery", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldText}}})
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
	writeGeneratedTestFile(t, directory, "consumer/wrong.go", []byte(`package consumer
import "example.com/godj-project-bundle/project"
func wrong(api project.Models){_ = api.OwnersOwner.PrefetchRelated(api.LabelsLabel.Prefetch.Owners)}
`))
	output, err = generatedGoCommand(t.Context(), directory, "test", "-run", "^$", "./...").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("cannot use")) || !bytes.Contains(output, []byte("PrefetchSelector")) {
		t.Fatalf("cross-owner prefetch selector was not rejected: %v\n%s", err, output)
	}
	writeGeneratedTestFile(t, directory, "consumer/wrong.go", []byte(`package consumer
import "example.com/godj-project-bundle/project"
func wrong(api project.Models){_ = api.OwnersOwner.Prefetch.Labels.WithChildren(api.OwnersOwner.Prefetch.Labels)}
`))
	output, err = generatedGoCommand(t.Context(), directory, "test", "-run", "^$", "./...").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("cannot use")) || !bytes.Contains(output, []byte("WithChildren")) {
		t.Fatalf("wrong-target child prefetch was not rejected: %v\n%s", err, output)
	}
	writeGeneratedTestFile(t, directory, "consumer/wrong.go", []byte(`package consumer
import("example.com/godj-project-bundle/project";"example.com/godj-project-bundle/owners")
func wrong(api project.Models){_ = api.OwnersOwner.Prefetch.Labels.Filter(owners.OwnerFields.Name.Exact("wrong"))}
`))
	output, err = generatedGoCommand(t.Context(), directory, "test", "-run", "^$", "./...").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("cannot use")) || !bytes.Contains(output, []byte("Filter")) {
		t.Fatalf("wrong-model target predicate was not rejected: %v\n%s", err, output)
	}
	for _, expression := range []string{
		"api.OwnersOwner.Prefetch.RankedLinkRows.WithChildren(api.OwnersOwner.Prefetch.Labels)",
		"api.OwnersOwner.Prefetch.RankedLinkRows.SelectRelated(api.OwnersOptional.Related.Link)",
		"api.OwnersOwner.Prefetch.Labels.SelectRelated(api.OwnersRankedLink.Related.Owner)",
	} {
		writeGeneratedTestFile(t, directory, "consumer/wrong.go", []byte("package consumer\nimport \"example.com/godj-project-bundle/project\"\nfunc wrong(api project.Models){_ = "+expression+"}\n"))
		output, err = generatedGoCommand(t.Context(), directory, "test", "-run", "^$", "./...").CombinedOutput()
		if err == nil || !bytes.Contains(output, []byte("cannot use")) {
			t.Fatalf("wrong reverse/eager target accepted: %v\n%s", err, output)
		}
	}
}
