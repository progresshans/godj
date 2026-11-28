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

func TestGeneratedUpdateOrCreate(t *testing.T) {
	spec := customSinglePrefetchSpec()
	spec.Apps[1].Schema.Models[0].Fields[0].Unique = true
	reference, err := schema.Build(schema.Definition{AppLabel: "owners", Models: []schema.Model{
		{Name: "upsert_region", GoName: "UpsertRegion", DBTable: "gdj_upsert_region", Fields: []schema.Field{schema.CharField("name", "Name", 32)}},
		{Name: "upsert_category", GoName: "UpsertCategory", DBTable: "gdj_upsert_category", Fields: []schema.Field{
			schema.CharField("name", "Name", 32),
			schema.ForeignKey("region", "RegionID", schema.Target("owners", "upsert_region"), schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}},
		{Name: "upsert_item", GoName: "UpsertItem", DBTable: "gdj_upsert_item", Fields: []schema.Field{
			schema.CharField("name", "Name", 32, schema.Unique()),
			schema.CharField("detail", "Detail", 32, schema.Default("")),
			schema.IntegerField("revision", "Revision", schema.Default(int64(0))),
			schema.ForeignKey("category", "CategoryID", schema.Target("owners", "upsert_category"), schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}},
		{Name: "upsert_counter", GoName: "UpsertCounter", DBTable: "gdj_upsert_counter", Fields: []schema.Field{
			schema.CharField("name", "Name", 32, schema.Unique()),
			schema.IntegerField("counter", "Counter", schema.Default(int64(0))),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, reference.Models...)
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	for _, name := range []string{"consumer_test.go", "single_object_test.go", "update_or_create_test.go", "update_or_create_race_test.go", "update_or_create_reference_test.go", "row_lock_reference_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manytomany", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	referenceCases := []string{"separate_create_defaults", "existing_uses_update_defaults_only", "omitted_create_defaults_uses_update_defaults", "existing_empty_defaults", "existing_update_unique_failure", "outer_rollback_undoes_update_and_creation", "filtered_create_can_leave_predicate", "ambiguous_never_resolves_defaults", "for_update_outside_transaction", "nowait_and_skip_locked_rejected_at_construction", "no_key_skip_locked_and_of_self", "nullable_join_unqualified_lock", "nullable_join_locks_only_self", "native_upsert_resets_explicit_lock_options"}
	observer, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/update_or_create_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(observer)
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "update_or_create-django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "updateorcreate", name))
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Observer   string `json:"observer_sha256"`
			Cases      []struct{ Case string }
			Contention []json.RawMessage
		}
		if err := json.Unmarshal(data, &fixture); err != nil || fixture.Observer != hex.EncodeToString(digest[:]) || len(fixture.Cases) != len(referenceCases) || len(fixture.Contention) != 2 {
			t.Fatal("native observer or inventory binding", backend, err)
		}
		for index, name := range referenceCases {
			if fixture.Cases[index].Case != name {
				t.Fatal("native case order changed", backend, name)
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	rowLockCases := map[string][]string{
		"terminals":  {"count_outside_transaction", "max_outside_transaction", "exists_outside_transaction", "ordered_first_outside_transaction", "empty_in_get_outside_transaction", "empty_in_exists_outside_transaction", "empty_in_aggregate_outside_transaction", "selected_values_lock_self_inside_transaction", "distinct_selected_values_inside_transaction", "locking_clone_does_not_reuse_warm_cache"},
		"targets":    {"lock_selected_related_model", "filter_join_does_not_make_lock_target_selected", "unknown_selected_lock_target", "relation_value_projection_lock_target", "self_lock_when_projection_omits_self", "self_lock_when_projection_includes_self", "nested_selected_lock_target", "no_key_update_for_two_selected_models"},
		"contention": {"default_selection_locks_root_and_related", "self_only_leaves_related_available", "related_only_leaves_root_available", "nested_target_only_locks_region", "projection_omitting_self_actual_locks", "projection_including_self_actual_locks", "foreign_key_insert_with_update", "foreign_key_insert_with_no_key_update"},
	}
	observer, err = os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/row_lock_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	digest = sha256.Sum256(observer)
	for _, backend := range []string{"sqlite", "postgres"} {
		name := "row_lock-django61-" + backend + ".json"
		data, err := os.ReadFile(filepath.Join("testdata", "updateorcreate", name))
		if err != nil {
			t.Fatal(err)
		}
		var fixture map[string]json.RawMessage
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		var observerHash string
		if err := json.Unmarshal(fixture["observer_sha256"], &observerHash); err != nil || observerHash != hex.EncodeToString(digest[:]) {
			t.Fatal("row lock observer binding", err)
		}
		for group, names := range rowLockCases {
			if backend == "sqlite" && group == "contention" {
				names = nil
			}
			var cases []struct{ Case string }
			if err := json.Unmarshal(fixture[group], &cases); err != nil || len(cases) != len(names) {
				t.Fatal("row lock inventory changed", backend, group, err)
			}
			for index, name := range names {
				if cases[index].Case != name {
					t.Fatal("row lock case order changed", name)
				}
			}
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, data)
	}
	required := []string{"TestUpdateOrCreate", "TestUpdateOrCreateContention", "TestUpdateOrCreateReference", "TestRowLockReference"}
	for _, backend := range []string{"sqlite", "postgres"} {
		if backend == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" && os.Getenv("GODJ_REQUIRE_POSTGRES") != "1" {
			continue
		}
		for _, name := range []string{"branches_cache_and_dynamic", "selected_graph_after_fk_change", "created_selected_graph", "graph_failure_rolls_back", "borrowed_scopes", "post_commit_cancellation", "capability_and_lock_options", "root_batch_affinity", "relation_query_scope"} {
			required = append(required, "TestUpdateOrCreate/"+backend+"/"+name)
		}
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			for _, finish := range []string{"commit", "rollback"} {
				required = append(required, "TestUpdateOrCreate/"+backend+"/borrowed_scopes/"+mode+"/"+finish)
			}
		}
		for _, name := range []string{"existing_row", "concurrent_create"} {
			required = append(required, "TestUpdateOrCreateContention/"+backend+"/"+name)
		}
		for _, name := range append(referenceCases, "final_rows") {
			required = append(required, "TestUpdateOrCreateReference/"+backend+"/"+name)
		}
		for group, names := range rowLockCases {
			if backend == "sqlite" && group == "contention" {
				continue
			}
			for _, name := range names {
				required = append(required, "TestRowLockReference/"+backend+"/"+group+"/"+name)
			}
		}
		required = append(required, "TestRowLockReference/"+backend+"/final_rows")
	}
	command := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=5m", "-run", "^Test(UpdateOrCreate(Contention|Reference)?|RowLockReference)$", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
	writeGeneratedTestFile(t, root, "wrongupsert/compile.go", []byte(`package wrongupsert
import (
 "context"
 "example.com/godj-project-bundle/project"
 "example.com/godj-project-bundle/labels"
)
func reject() {
 api, _ := project.Using(nil)
 _,_,_ = api.OwnersOwner.UpdateOrCreate(context.Background(), nil, labels.LabelPatch{})
}
`))
	output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-run", "^$", "./wrongupsert").CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot use") || !strings.Contains(string(output), "PatchInput") {
		t.Fatalf("foreign patch compiled or failed elsewhere: %v\n%s", err, output)
	}
}
