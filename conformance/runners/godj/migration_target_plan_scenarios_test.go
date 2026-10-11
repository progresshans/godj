//go:build darwin || linux

package godj

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

var migrationTargetPlanExpectedRegistrations = []struct {
	id       string
	scenario string
	phase    protocol.Phase
}{
	{id: "MIG-119", scenario: "godj.migration.target_plan.target_argv_and_pre_io_rejection", phase: protocol.PhaseEnvironment},
	{id: "MIG-120", scenario: "django.migration.target_plan.named_forward_closure", phase: protocol.PhaseEvaluation},
	{id: "MIG-121", scenario: "django.migration.target_plan.named_reverse_descendants", phase: protocol.PhaseEvaluation},
	{id: "MIG-122", scenario: "django.migration.target_plan.app_zero_cross_app_dependents", phase: protocol.PhaseEvaluation},
	{id: "MIG-123", scenario: "godj.migration.target_plan.target_noop_and_legacy_zero", phase: protocol.PhaseEvaluation},
	{id: "MIG-124", scenario: "godj.migration.target_plan.plan_exact_and_no_mutation", phase: protocol.PhaseEvaluation},
	{id: "MIG-125", scenario: "godj.migration.target_plan.preview_drift_fresh_execute", phase: protocol.PhaseCommit},
	{id: "MIG-126", scenario: "godj.migration.target_plan.reverse_middle_failure_resume", phase: protocol.PhaseRollback},
	{id: "MIG-127", scenario: "godj.migration.target_plan.reverse_commit_outcomes", phase: protocol.PhaseCommit},
	{id: "MIG-128", scenario: "godj.migration.target_plan.project_protocol_and_ownership", phase: protocol.PhaseEnvironment},
}

func migrationTargetPlanContractTimeout(contractID string) time.Duration {
	switch contractID {
	case "MIG-128":
		// MIG-128 runs four independently built external-project process-owner
		// probes. Each probe retains its own bounded phase timeout; this outer
		// budget only covers their sequential aggregate on slower hosted runners.
		return 10 * time.Minute
	default:
		return 90 * time.Second
	}
}

func TestMigrationTargetPlanContractTimeoutIsNarrow(t *testing.T) {
	for _, test := range []struct {
		contractID string
		want       time.Duration
	}{
		{contractID: "MIG-119", want: 90 * time.Second},
		{contractID: "MIG-127", want: 90 * time.Second},
		{contractID: "MIG-128", want: 10 * time.Minute},
		{contractID: "MIG-999", want: 90 * time.Second},
	} {
		t.Run(test.contractID, func(t *testing.T) {
			if got := migrationTargetPlanContractTimeout(test.contractID); got != test.want {
				t.Fatalf("migration-target-plan contract timeout = %s, want %s", got, test.want)
			}
		})
	}
}

func TestMigrationTargetPlanRegistryIsExactAndFailsClosed(t *testing.T) {
	if len(migrationTargetPlanScenarioRegistry) != len(migrationTargetPlanExpectedRegistrations) {
		t.Fatalf("migration-target-plan registry size = %d, want %d", len(migrationTargetPlanScenarioRegistry), len(migrationTargetPlanExpectedRegistrations))
	}
	for _, expected := range migrationTargetPlanExpectedRegistrations {
		registration, ok := migrationTargetPlanScenarioRegistry[expected.scenario]
		if !ok || registration.handler == nil || registration.id != expected.id || registration.phase != expected.phase {
			t.Fatalf("migration-target-plan registration %q = %#v", expected.scenario, registration)
		}
		handler, ok := lookupScenarioHandler(expected.scenario)
		if !ok || handler == nil {
			t.Fatalf("generic runner lookup omitted %s", expected.id)
		}
	}
	handler, ok := migrationTargetPlanScenarioHandler(migrationTargetPlanExpectedRegistrations[0].scenario)
	if !ok || handler == nil {
		t.Fatal("known migration-target-plan scenario is missing")
	}
	valid := migrationTargetPlanExpectedRegistrations[0]
	for _, test := range []struct {
		name     string
		ctx      context.Context
		contract protocol.Contract
	}{
		{name: "nil_context", contract: protocol.Contract{ID: valid.id, Scenario: valid.scenario, Phase: valid.phase}},
		{name: "wrong_id", ctx: context.Background(), contract: protocol.Contract{ID: "MIG-999", Scenario: valid.scenario, Phase: valid.phase}},
		{name: "wrong_scenario", ctx: context.Background(), contract: protocol.Contract{ID: valid.id, Scenario: "wrong", Phase: valid.phase}},
		{name: "wrong_phase", ctx: context.Background(), contract: protocol.Contract{ID: valid.id, Scenario: valid.scenario, Phase: protocol.PhaseCommit}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := handler(test.ctx, test.contract); err == nil {
				t.Fatal("migration-target-plan handler accepted invalid input")
			}
		})
	}
	if unknown, found := migrationTargetPlanScenarioHandler("godj.migration.target_plan.unknown"); found || unknown != nil {
		t.Fatalf("unknown migration-target-plan handler = %v, %t", unknown, found)
	}
}

func TestMigrationTargetPlanActualSourceIsOracleBlind(t *testing.T) {
	document, err := os.ReadFile("migration_target_plan_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(document)
	for _, forbidden := range []string{
		"conformance/oracles/", "migration-target-plan-oracle.json", "migration-target-plan-not-implemented.json",
		"godj-migration-target-plan-deviation-expected.json", "conformance/contracts/", "runners/django/",
		"protocol.Compare(", "LoadObservationSuite(", "LoadManifest(",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("migration-target-plan actual source contains forbidden artifact shortcut %q", forbidden)
		}
	}
	for _, required := range []string{
		"overflow.report.RunnerStdoutRetainedBytes != migrateprotocol.MaxResponseBytes",
		"!overflow.report.RunnerStdoutTruncated",
		"actual.forcedCancellation.report.GroupSIGKILLAttempts",
		"migrationTargetPlanInt(sigkillAttemptsMaximum)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("migration-target-plan actual source is missing process-owner stdout observation %q", required)
		}
	}
	ownerSource, err := os.ReadFile("../../../internal/projectcheck/migrate_run_unix.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"report.RunnerStdoutRetainedBytes = runner.StdoutScalar.RetainedBytes",
		"report.RunnerStdoutTruncated = runner.StdoutScalar.Truncated",
	} {
		if !strings.Contains(string(ownerSource), required) {
			t.Fatalf("migrate process owner is missing stdout observation %q", required)
		}
	}
	for _, forbidden := range []string{
		`"process_groups_remaining": migrationTargetPlanInt(0)`,
		`"canceled_process_groups_remaining": migrationTargetPlanInt(0)`,
		`"sigint_attempts_maximum": migrationTargetPlanInt(1)`,
		`"sigkill_attempts_maximum": migrationTargetPlanInt(1)`,
		`StdoutScalar: productcheck.StreamScalar{RetainedBytes: migrateprotocol.MaxResponseBytes, Truncated: true}`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("migration-target-plan actual source contains false-green ownership shortcut %q", forbidden)
		}
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "migration_target_plan_scenarios.go", document, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbiddenCalls := map[string]bool{
		"ReadFile": true, "Open": true, "OpenFile": true, "ReadAll": true,
		"LoadManifest": true, "LoadObservationSuite": true, "Compare": true,
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && forbiddenCalls[selector.Sel.Name] {
			t.Errorf("migration-target-plan actual source contains forbidden call %s", selector.Sel.Name)
		}
		return true
	})
	runner, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatal(err)
	}
	runnerText := string(runner)
	targetIndex := strings.Index(runnerText, "migrationTargetPlanScenarioHandler(scenario)")
	fixtureIndex := strings.Index(runnerText, "migrationProjectCheckFixtures[scenario]")
	if targetIndex < 0 || fixtureIndex < 0 || targetIndex > fixtureIndex {
		t.Fatalf("migration-target-plan handler is not registered before generic fixture fallback: target=%d fixture=%d", targetIndex, fixtureIndex)
	}
	for _, required := range []string{
		`pending := filepath.Join(directory, ".godj-marker-"+name+".pending")`,
		`os.WriteFile(pending, []byte(value), 0o600)`,
		`os.Rename(pending, path)`,
	} {
		if !strings.Contains(migrationTargetPlanActualRunnerSource, required) {
			t.Errorf("embedded migration-target-plan runner lacks atomic marker publication fragment %q", required)
		}
	}
	if strings.Contains(migrationTargetPlanActualRunnerSource, `os.WriteFile(path, []byte(value), 0o600)`) {
		t.Error("embedded migration-target-plan runner publishes marker contents directly to the observable path")
	}
}

func TestMigrationTargetPlanExecutionObservationUsesActualTransactionIdentity(t *testing.T) {
	keys := []migrations.MigrationKey{
		{App: "blog", Name: "0001_article"},
		{App: "blog", Name: "0002_editor"},
		{App: "blog", Name: "0003_publish"},
		{App: "blog", Name: "0004_archive"},
	}
	plan := []migrations.PlanStep{
		{Key: keys[3], Direction: migrations.DirectionBackward},
		{Key: keys[2], Direction: migrations.DirectionBackward},
		{Key: keys[1], Direction: migrations.DirectionBackward},
	}
	before := []migrationbackend.AppliedMigration{
		{App: keys[0].App, Name: keys[0].Name},
		{App: keys[1].App, Name: keys[1].Name},
		{App: keys[2].App, Name: keys[2].Name},
		{App: keys[3].App, Name: keys[3].Name},
	}
	after := migrationTargetPlanBackendSnapshot{
		records: []migrationbackend.AppliedMigration{
			{App: keys[0].App, Name: keys[0].Name},
			{App: keys[1].App, Name: keys[1].Name},
			{App: keys[2].App, Name: keys[2].Name},
		},
		beginCalls: 2, commitCalls: 1, rollbackCalls: 1,
		beginSteps: []migrations.PlanStep{plan[0], plan[1]},
	}
	observed, err := migrationTargetPlanObserveExecution(plan, before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed.committed, []migrations.MigrationKey{keys[3]}) ||
		!reflect.DeepEqual(observed.rolledBack, []migrations.MigrationKey{keys[2]}) ||
		!reflect.DeepEqual(observed.unstarted, []migrations.MigrationKey{keys[1]}) {
		t.Fatalf("observed transaction identities = %+v", observed)
	}
	if seeded := migrationTargetPlanRecordKeys(after.records); !reflect.DeepEqual(seeded, keys[:3]) {
		t.Fatalf("fresh-process seed = %+v, want actual durable snapshot %+v", seeded, keys[:3])
	}

	drifted := after
	drifted.beginSteps = []migrations.PlanStep{plan[0], plan[2]}
	if _, err := migrationTargetPlanObserveExecution(plan, before, drifted); err == nil {
		t.Fatal("transaction observation accepted a non-prefix begun-step identity")
	}
	drifted = after
	drifted.records = append([]migrationbackend.AppliedMigration(nil), before...)
	if _, err := migrationTargetPlanObserveExecution(plan, before, drifted); err == nil {
		t.Fatal("transaction observation accepted record outcomes that disagree with commit/rollback counters")
	}
}

func TestMigrationTargetPlanCommitOutcomeObservationIsDeterministic(t *testing.T) {
	expected := migrationTargetPlanExpectedRegistrations[8]
	handler, ok := migrationTargetPlanScenarioHandler(expected.scenario)
	if !ok {
		t.Fatal("MIG-127 handler is not registered")
	}
	var previous []byte
	for iteration := 0; iteration < 5; iteration++ {
		observation, err := handler(context.Background(), protocol.Contract{ID: expected.id, Scenario: expected.scenario, Phase: expected.phase})
		if err != nil {
			t.Fatal(err)
		}
		document, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		if iteration != 0 && !reflect.DeepEqual(document, previous) {
			t.Fatalf("MIG-127 observation iteration %d changed\nprevious=%s\ncurrent=%s", iteration, previous, document)
		}
		previous = append(previous[:0], document...)
	}
}

func TestMigrationTargetPlanScenariosExecuteActualBoundaries(t *testing.T) {
	resultFields := map[string][]string{
		"MIG-119": {"accepted", "exact_public_families", "migration_name_resolution", "option_permutations", "post_discovery_rejections", "rejected", "zero_reserved_spelling"},
		"MIG-120": {"applied", "name", "plan", "targets"},
		"MIG-121": {"applied", "name", "plan", "targets"},
		"MIG-122": {"applied", "name", "plan", "targets"},
		"MIG-123": {"cases", "legacy_zero_unknown_contract", "public_zero_requires_known_app"},
		"MIG-124": {"cases", "plan_is_execution_authority"},
		"MIG-125": {"execute_plan", "preview_plan", "preview_token_accepted", "replanned_from_fresh_history"},
		"MIG-126": {"cases", "unstarted_tail_started"},
		"MIG-127": {"cases", "reconciliation_required_after_unknown"},
		"MIG-128": {"cases", "current_private_protocol_version", "identity_normalization", "legacy_private_reader", "load_before_backend_open", "plan_invariants", "private_argument", "raw_causes_published", "redaction", "resource_limits", "result_union_bound_to_mode", "valid_replacement_rune_preserved", "wire_rejections"},
	}
	dbFields := map[string][]string{
		"MIG-124": {"cases"},
		"MIG-125": {"after_execute_history", "after_preview_history", "after_writer_drift_history", "preview_mutations"},
		"MIG-126": {"after_failure_history", "after_resume_history", "durable_prefix_preserved", "initial_history", "rolled_back_step_preserved", "unstarted_tail_preserved"},
		"MIG-127": {"committed_cleanup_history_preserved", "confirmed_rollback_history_preserved", "unknown_history_guessed"},
		"MIG-128": {"canceled_process_groups_remaining", "failed_plan_published", "partial_response_republished", "plan_mutations", "secret_values_published"},
	}
	metricFields := map[string][]string{
		"MIG-119": {"accepted_forms", "backend_opens_for_rejected", "builds_for_rejected", "post_discovery_target_not_found_cases", "project_discoveries_for_rejected", "rejected_forms"},
		"MIG-123": {"begin_calls", "history_reads", "target_not_found_cases"},
		"MIG-124": {"application_mutations", "history_reads", "migration_begins", "recorder_mutations", "revision_mutations", "schema_mutations", "session_closes"},
		"MIG-125": {"automatic_retries", "execute_history_reads", "execute_migration_begins", "preview_history_reads", "preview_migration_begins"},
		"MIG-126": {"automatic_retries", "first_process_reverse_commits", "first_process_reverse_rollbacks", "first_process_unstarted_steps", "fresh_processes", "fresh_resume_reverse_commits", "fresh_resume_reverse_rollbacks", "reverse_commits", "reverse_rollbacks", "started_steps", "unstarted_steps"},
		"MIG-127": {"automatic_retries", "cases", "unknown_rollbacks"},
		"MIG-128": {"automatic_retries", "cancellation_direct_reaps", "cancellation_process_group_terminations", "legacy_reader_paths", "ownership_cases", "partial_responses_republished", "raw_secret_occurrences", "resource_limit_cases", "strict_wire_rejection_cases", "successful_mode_calls"},
	}
	for _, expected := range migrationTargetPlanExpectedRegistrations {
		expected := expected
		t.Run(expected.id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), migrationTargetPlanContractTimeout(expected.id))
			defer cancel()
			handler, ok := migrationTargetPlanScenarioHandler(expected.scenario)
			if !ok {
				t.Fatal("scenario is not registered")
			}
			observation, err := handler(ctx, protocol.Contract{ID: expected.id, Scenario: expected.scenario, Phase: expected.phase})
			if err != nil {
				t.Fatal(err)
			}
			if err := observation.Validate(); err != nil {
				t.Fatalf("invalid observation: %v", err)
			}
			if observation.Status != protocol.StatusObserved || observation.Result == nil {
				t.Fatalf("observation status/result = %q/%v", observation.Status, observation.Result)
			}
			migrationTargetPlanAssertFields(t, *observation.Result, resultFields[expected.id])
			if fields, ok := dbFields[expected.id]; ok {
				if observation.DBState == nil {
					t.Fatal("missing db_state")
				}
				migrationTargetPlanAssertFields(t, *observation.DBState, fields)
			} else if observation.DBState != nil {
				t.Fatalf("unexpected db_state: %+v", *observation.DBState)
			}
			if fields, ok := metricFields[expected.id]; ok {
				if observation.Metrics == nil {
					t.Fatal("missing metrics")
				}
				migrationTargetPlanAssertFields(t, *observation.Metrics, fields)
			} else if observation.Metrics != nil {
				t.Fatalf("unexpected metrics: %+v", *observation.Metrics)
			}
			document, err := json.Marshal(observation)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(document), migrationTargetPlanSecret) {
				t.Fatalf("serialized %s observation contains private canary", expected.id)
			}
			switch expected.id {
			case "MIG-119":
				if migrationTargetPlanTestText(t, *observation.Result, "exact_public_families") != "8" ||
					migrationTargetPlanTestField(t, *observation.Result, "exact_public_families").Type != protocol.ValueInt {
					t.Fatalf("MIG-119 exact public family count = %+v", migrationTargetPlanTestField(t, *observation.Result, "exact_public_families"))
				}
			case "MIG-123":
				cases := migrationTargetPlanTestField(t, *observation.Result, "cases")
				if cases.Type != protocol.ValueList || len(cases.Items) != 3 {
					t.Fatalf("MIG-123 cases = %+v", cases)
				}
				for _, row := range cases.Items {
					migrationTargetPlanAssertFields(t, row, []string{"begin_calls", "case", "category", "code", "plan"})
					if migrationTargetPlanTestText(t, row, "begin_calls") != "0" {
						t.Fatalf("MIG-123 begin calls = %+v", row)
					}
				}
			case "MIG-126":
				cases := migrationTargetPlanTestField(t, *observation.Result, "cases")
				for _, row := range cases.Items {
					plan := migrationTargetPlanTestField(t, row, "plan")
					for _, identity := range plan.Items {
						if identity.Type != protocol.ValueString {
							t.Fatalf("MIG-126 plan identity = %+v", identity)
						}
					}
				}
			case "MIG-127":
				migrationTargetPlanAssertCommitOutcomes(t, observation)
			case "MIG-128":
				migrationTargetPlanAssertOwnership(t, observation)
			}
		})
	}
}

func migrationTargetPlanAssertCommitOutcomes(t *testing.T, observation protocol.Observation) {
	t.Helper()
	cases := migrationTargetPlanTestField(t, *observation.Result, "cases")
	if cases.Type != protocol.ValueList || len(cases.Items) != 3 {
		t.Fatalf("MIG-127 cases = %+v", cases)
	}
	expected := []struct {
		name, category, code, history string
		rollback                      string
	}{
		{name: "commit_outcome_unknown", category: "migration_transaction_error", code: "commit_outcome_unknown", history: "unknown", rollback: "0"},
		{name: "confirmed_rollback", category: "migration_execution_error", code: "operation_failed", history: "preserved_before_step", rollback: "1"},
		{name: "committed_cleanup_failure", category: "migration_transaction_error", code: "commit_cleanup_failed", history: "committed_successor", rollback: "0"},
	}
	for index, want := range expected {
		row := cases.Items[index]
		migrationTargetPlanAssertFields(t, row, []string{"automatic_retries", "case", "category", "code", "history", "reported_success", "rollback_after_outcome"})
		if migrationTargetPlanTestText(t, row, "case") != want.name || migrationTargetPlanTestText(t, row, "category") != want.category ||
			migrationTargetPlanTestText(t, row, "code") != want.code || migrationTargetPlanTestText(t, row, "history") != want.history ||
			migrationTargetPlanTestText(t, row, "automatic_retries") != "0" || migrationTargetPlanTestText(t, row, "rollback_after_outcome") != want.rollback ||
			migrationTargetPlanTestBool(t, row, "reported_success") {
			t.Fatalf("MIG-127 case %d = %+v", index, row)
		}
	}
	if !migrationTargetPlanTestBool(t, *observation.Result, "reconciliation_required_after_unknown") ||
		migrationTargetPlanTestText(t, *observation.Metrics, "automatic_retries") != "0" ||
		migrationTargetPlanTestText(t, *observation.Metrics, "cases") != "3" ||
		migrationTargetPlanTestText(t, *observation.Metrics, "unknown_rollbacks") != "0" {
		t.Fatalf("MIG-127 result/metrics = result:%+v metrics:%+v", *observation.Result, *observation.Metrics)
	}
	if !migrationTargetPlanTestBool(t, *observation.DBState, "committed_cleanup_history_preserved") ||
		!migrationTargetPlanTestBool(t, *observation.DBState, "confirmed_rollback_history_preserved") ||
		migrationTargetPlanTestBool(t, *observation.DBState, "unknown_history_guessed") {
		t.Fatalf("MIG-127 db_state = %+v", *observation.DBState)
	}
}

func migrationTargetPlanAssertOwnership(t *testing.T, observation protocol.Observation) {
	t.Helper()
	result := *observation.Result
	for field, want := range map[string]string{
		"current_private_protocol_version": "2",
		"identity_normalization":           "none",
		"private_argument":                 "__godj_project_migrate_runner_v2",
	} {
		if got := migrationTargetPlanTestText(t, result, field); got != want {
			t.Fatalf("MIG-128 %s = %q, want %q", field, got, want)
		}
	}
	for _, field := range []string{"load_before_backend_open", "result_union_bound_to_mode", "valid_replacement_rune_preserved"} {
		if !migrationTargetPlanTestBool(t, result, field) {
			t.Fatalf("MIG-128 %s = false", field)
		}
	}
	for _, field := range []string{"legacy_private_reader", "raw_causes_published"} {
		if migrationTargetPlanTestBool(t, result, field) {
			t.Fatalf("MIG-128 %s = true", field)
		}
	}

	cases := migrationTargetPlanTestField(t, result, "cases")
	wantCaseFields := map[string][]string{
		"execute_success":                {"backend_closes", "backend_opens", "case", "category", "code", "lifecycle_calls", "mode", "private_response_writes", "public_result_published"},
		"plan_success":                   {"backend_closes", "backend_opens", "case", "category", "code", "lifecycle_calls", "mode", "private_response_writes", "public_plan_published"},
		"outer_close_failure":            {"backend_closes", "backend_opens", "case", "category", "cleanup_failed", "code", "lifecycle_calls", "mode", "private_response_writes", "public_plan_published"},
		"cancellation_cleanup":           {"case", "category", "child_started", "cleanup_failed", "code", "direct_reaps", "mode", "partial_response_republished", "process_group_terminations", "process_groups_remaining", "public_plan_published", "sigint_attempts_maximum", "sigkill_attempts_maximum"},
		"partial_output_non_publication": {"case", "category", "child_started", "cleanup_failed", "code", "complete_private_documents", "direct_reaps", "mode", "partial_private_chunks", "partial_response_republished", "public_plan_published"},
		"terminal_short_write":           {"backend_closes", "backend_opens", "case", "category", "code", "lifecycle_calls", "mode", "private_response_write_attempts", "private_response_writes_completed", "public_plan_published"},
	}
	wantCaseOrder := []string{"execute_success", "plan_success", "outer_close_failure", "cancellation_cleanup", "partial_output_non_publication", "terminal_short_write"}
	if cases.Type != protocol.ValueList || len(cases.Items) != len(wantCaseOrder) {
		t.Fatalf("MIG-128 ownership cases = %+v", cases)
	}
	for index, name := range wantCaseOrder {
		if got := migrationTargetPlanTestText(t, cases.Items[index], "case"); got != name {
			t.Fatalf("MIG-128 ownership case %d = %q, want %q", index, got, name)
		}
		migrationTargetPlanAssertFields(t, cases.Items[index], wantCaseFields[name])
	}
	if migrationTargetPlanTestText(t, cases.Items[0], "mode") != "execute" ||
		!migrationTargetPlanTestBool(t, cases.Items[0], "public_result_published") ||
		migrationTargetPlanTestText(t, cases.Items[0], "backend_opens") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[0], "backend_closes") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[0], "lifecycle_calls") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[0], "private_response_writes") != "1" {
		t.Fatalf("MIG-128 execute ownership = %+v", cases.Items[0])
	}
	if migrationTargetPlanTestText(t, cases.Items[1], "mode") != "plan" ||
		!migrationTargetPlanTestBool(t, cases.Items[1], "public_plan_published") ||
		migrationTargetPlanTestText(t, cases.Items[1], "backend_opens") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[1], "backend_closes") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[1], "lifecycle_calls") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[1], "private_response_writes") != "1" {
		t.Fatalf("MIG-128 plan ownership = %+v", cases.Items[1])
	}
	if migrationTargetPlanTestText(t, cases.Items[2], "category") != "migration_backend_error" ||
		migrationTargetPlanTestText(t, cases.Items[2], "code") != "backend_close_failed" ||
		!migrationTargetPlanTestBool(t, cases.Items[2], "cleanup_failed") || migrationTargetPlanTestBool(t, cases.Items[2], "public_plan_published") {
		t.Fatalf("MIG-128 close ownership = %+v", cases.Items[2])
	}
	if migrationTargetPlanTestText(t, cases.Items[3], "category") != "migration_project_process_error" ||
		migrationTargetPlanTestText(t, cases.Items[3], "code") != "project_canceled" ||
		!migrationTargetPlanTestBool(t, cases.Items[3], "child_started") || migrationTargetPlanTestBool(t, cases.Items[3], "cleanup_failed") ||
		migrationTargetPlanTestText(t, cases.Items[3], "direct_reaps") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[3], "process_group_terminations") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[3], "process_groups_remaining") != "0" ||
		migrationTargetPlanTestText(t, cases.Items[3], "sigkill_attempts_maximum") != "1" ||
		migrationTargetPlanTestBool(t, cases.Items[3], "partial_response_republished") ||
		migrationTargetPlanTestBool(t, cases.Items[3], "public_plan_published") {
		t.Fatalf("MIG-128 cancellation ownership = %+v", cases.Items[3])
	}
	if migrationTargetPlanTestText(t, cases.Items[4], "category") != "migration_project_protocol_error" ||
		migrationTargetPlanTestText(t, cases.Items[4], "code") != "invalid_project_migrate_runner_response" ||
		!migrationTargetPlanTestBool(t, cases.Items[4], "child_started") || migrationTargetPlanTestBool(t, cases.Items[4], "cleanup_failed") ||
		migrationTargetPlanTestText(t, cases.Items[4], "complete_private_documents") != "0" ||
		migrationTargetPlanTestText(t, cases.Items[4], "partial_private_chunks") != "1" ||
		migrationTargetPlanTestBool(t, cases.Items[4], "partial_response_republished") || migrationTargetPlanTestBool(t, cases.Items[4], "public_plan_published") {
		t.Fatalf("MIG-128 partial ownership = %+v", cases.Items[4])
	}
	if migrationTargetPlanTestText(t, cases.Items[5], "category") != "migration_project_internal_error" ||
		migrationTargetPlanTestText(t, cases.Items[5], "code") != "project_internal_error" ||
		migrationTargetPlanTestText(t, cases.Items[5], "private_response_write_attempts") != "1" ||
		migrationTargetPlanTestText(t, cases.Items[5], "private_response_writes_completed") != "0" ||
		migrationTargetPlanTestBool(t, cases.Items[5], "public_plan_published") {
		t.Fatalf("MIG-128 short-write ownership = %+v", cases.Items[5])
	}

	wire := migrationTargetPlanTestField(t, result, "wire_rejections")
	wantWire := []struct{ name, boundary, code string }{
		{name: "request_duplicate_key", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "request_unknown_key", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "request_trailing_bytes", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "request_noncanonical_number", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "request_invalid_utf8", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "request_unpaired_utf16_surrogate", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "response_duplicate_key", boundary: "response", code: "invalid_project_migrate_runner_response"},
		{name: "response_unknown_key", boundary: "response", code: "invalid_project_migrate_runner_response"},
		{name: "response_trailing_bytes", boundary: "response", code: "invalid_project_migrate_runner_response"},
		{name: "response_noncanonical_number", boundary: "response", code: "invalid_project_migrate_runner_response"},
		{name: "response_invalid_utf8", boundary: "response", code: "invalid_project_migrate_runner_response"},
		{name: "response_unpaired_utf16_surrogate", boundary: "response", code: "invalid_project_migrate_runner_response"},
		{name: "request_retired_protocol_version", boundary: "request", code: "project_migrate_protocol_incompatible"},
		{name: "response_mode_result_mismatch", boundary: "response", code: "invalid_project_migrate_runner_response"},
		{name: "request_invalid_mode", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "request_invalid_target_kind", boundary: "request", code: "invalid_project_migrate_runner_request"},
		{name: "response_invalid_direction", boundary: "response", code: "invalid_project_migrate_runner_response"},
	}
	if wire.Type != protocol.ValueList || len(wire.Items) != len(wantWire) {
		t.Fatalf("MIG-128 wire cases = %+v", wire)
	}
	for index, want := range wantWire {
		row := wire.Items[index]
		migrationTargetPlanAssertFields(t, row, []string{"accepted", "boundary", "case", "category", "code"})
		if migrationTargetPlanTestText(t, row, "case") != want.name || migrationTargetPlanTestText(t, row, "boundary") != want.boundary ||
			migrationTargetPlanTestText(t, row, "category") != "migration_project_protocol_error" || migrationTargetPlanTestText(t, row, "code") != want.code ||
			migrationTargetPlanTestBool(t, row, "accepted") {
			t.Fatalf("MIG-128 wire case %d = %+v", index, row)
		}
	}

	resources := migrationTargetPlanTestField(t, result, "resource_limits")
	wantResources := []struct{ name, boundary, maximum, unit string }{
		{name: "request_bytes", boundary: "request", maximum: "16777216", unit: "bytes"},
		{name: "response_bytes", boundary: "response", maximum: "105906176", unit: "bytes"},
		{name: "identity_bytes", boundary: "request_and_response", maximum: "1048576", unit: "bytes"},
		{name: "identity_aggregate_bytes", boundary: "request_and_response", maximum: "16777216", unit: "bytes"},
		{name: "plan_rows", boundary: "response", maximum: "2048", unit: "rows"},
	}
	if resources.Type != protocol.ValueList || len(resources.Items) != len(wantResources) {
		t.Fatalf("MIG-128 resource cases = %+v", resources)
	}
	for index, want := range wantResources {
		row := resources.Items[index]
		migrationTargetPlanAssertFields(t, row, []string{"boundary", "case", "maximum", "overflow", "unit"})
		if migrationTargetPlanTestText(t, row, "case") != want.name || migrationTargetPlanTestText(t, row, "boundary") != want.boundary ||
			migrationTargetPlanTestText(t, row, "maximum") != want.maximum || migrationTargetPlanTestText(t, row, "unit") != want.unit ||
			migrationTargetPlanTestText(t, row, "overflow") != "rejected" {
			t.Fatalf("MIG-128 resource case %d = %+v", index, row)
		}
	}
	planInvariants := migrationTargetPlanTestField(t, result, "plan_invariants")
	migrationTargetPlanAssertFields(t, planInvariants, []string{"closed_directions", "duplicate_identity", "maximum_unique_rows", "mixed_direction", "row_order"})
	closedDirections := migrationTargetPlanTestField(t, planInvariants, "closed_directions")
	if closedDirections.Type != protocol.ValueList || len(closedDirections.Items) != 2 || closedDirections.Items[0].Text == nil || closedDirections.Items[1].Text == nil ||
		*closedDirections.Items[0].Text != "forward" || *closedDirections.Items[1].Text != "backward" ||
		migrationTargetPlanTestText(t, planInvariants, "duplicate_identity") != "rejected" ||
		migrationTargetPlanTestText(t, planInvariants, "maximum_unique_rows") != "2048" ||
		migrationTargetPlanTestText(t, planInvariants, "mixed_direction") != "rejected" ||
		migrationTargetPlanTestText(t, planInvariants, "row_order") != "preserved" {
		t.Fatalf("MIG-128 plan invariants = %+v", planInvariants)
	}
	redaction := migrationTargetPlanTestField(t, result, "redaction")
	migrationTargetPlanAssertFields(t, redaction, []string{"published_raw_causes", "published_secret_values", "sensitive_classes"})
	if migrationTargetPlanTestText(t, redaction, "published_raw_causes") != "0" || migrationTargetPlanTestText(t, redaction, "published_secret_values") != "0" {
		t.Fatalf("MIG-128 redaction = %+v", redaction)
	}
	metrics := map[string]string{
		"automatic_retries": "0", "cancellation_direct_reaps": "1", "cancellation_process_group_terminations": "1",
		"legacy_reader_paths": "0", "ownership_cases": "6", "partial_responses_republished": "0", "raw_secret_occurrences": "0",
		"resource_limit_cases": "5", "strict_wire_rejection_cases": "17", "successful_mode_calls": "2",
	}
	for field, want := range metrics {
		if got := migrationTargetPlanTestText(t, *observation.Metrics, field); got != want {
			t.Fatalf("MIG-128 metric %s = %q, want %q", field, got, want)
		}
	}
	if migrationTargetPlanTestText(t, *observation.DBState, "canceled_process_groups_remaining") != "0" ||
		migrationTargetPlanTestBool(t, *observation.DBState, "failed_plan_published") ||
		migrationTargetPlanTestBool(t, *observation.DBState, "partial_response_republished") ||
		migrationTargetPlanTestText(t, *observation.DBState, "plan_mutations") != "0" ||
		migrationTargetPlanTestText(t, *observation.DBState, "secret_values_published") != "0" {
		t.Fatalf("MIG-128 db_state = %+v", *observation.DBState)
	}
}

func migrationTargetPlanAssertFields(t *testing.T, value protocol.Value, names []string) {
	t.Helper()
	if value.Type != protocol.ValueObject {
		t.Fatalf("value type = %q, want object", value.Type)
	}
	got := make([]string, len(value.Fields))
	for index := range value.Fields {
		got[index] = value.Fields[index].Name
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("object fields = %v, want %v", got, names)
	}
}

func migrationTargetPlanTestField(t *testing.T, value protocol.Value, name string) protocol.Value {
	t.Helper()
	if value.Type != protocol.ValueObject {
		t.Fatalf("field %q parent type = %q, want object", name, value.Type)
	}
	for _, field := range value.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	t.Fatalf("object has no field %q", name)
	return protocol.Value{}
}

func migrationTargetPlanTestText(t *testing.T, value protocol.Value, name string) string {
	t.Helper()
	field := migrationTargetPlanTestField(t, value, name)
	if field.Text == nil {
		t.Fatalf("field %q has no text: %+v", name, field)
	}
	return *field.Text
}

func migrationTargetPlanTestBool(t *testing.T, value protocol.Value, name string) bool {
	t.Helper()
	field := migrationTargetPlanTestField(t, value, name)
	if field.Type != protocol.ValueBool || field.Bool == nil {
		t.Fatalf("field %q is not bool: %+v", name, field)
	}
	return *field.Bool
}
