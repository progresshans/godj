package godj

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0046SQLiteContentionClassifierRejectsUnrelatedAndContextFailures(t *testing.T) {
	for _, err := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		errors.New("unrelated SQLite probe failure"),
	} {
		if gdj0046IsSQLiteContention(err) {
			t.Fatalf("gdj0046IsSQLiteContention(%v) = true", err)
		}
	}
}

func TestGDJ0046BootstrapPublicationCountIncludesUpdates(t *testing.T) {
	pair := &gdj0046BackendPair{
		holder: &gdj0046MultiRuntimeBackend{
			systemStateObservedBackend: &systemStateObservedBackend{},
		},
		contender: &gdj0046MultiRuntimeBackend{
			systemStateObservedBackend: &systemStateObservedBackend{},
		},
	}
	pair.holder.inserts.Store(1)
	pair.contender.updates.Store(1)
	if got := gdj0046BootstrapPublicationWrites(pair); got != 2 {
		t.Fatalf("bootstrap publication writes = %d, want insert + update = 2", got)
	}
}

func TestGDJ0046SystemStateMultiRuntimeScenariosExecuteProductFacts(t *testing.T) {
	tests := []struct {
		id      string
		phase   protocol.Phase
		handler scenarioHandler
	}{
		{id: "SYS-013", phase: protocol.PhaseCommit, handler: systemStateCoordinatedAtomicFence},
		{id: "SYS-014", phase: protocol.PhaseCommit, handler: systemStateConcurrentAdminBootstrap},
		{id: "SYS-015", phase: protocol.PhaseCommit, handler: systemStateConcurrentSessionCapacity},
		{id: "SYS-016", phase: protocol.PhaseCommit, handler: systemStateConcurrentTouchMonotonicity},
		{id: "SYS-017", phase: protocol.PhaseCommit, handler: systemStateConcurrentSessionRotation},
		{id: "SYS-018", phase: protocol.PhaseRollback, handler: systemStateConcurrentArticleAudit},
		{id: "SYS-019", phase: protocol.PhaseEvaluation, handler: systemStateSharedCSRFKeyRing},
	}
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			observation, err := test.handler(t.Context(), protocol.Contract{ID: test.id, Phase: test.phase})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ID != test.id || observation.Phase != test.phase ||
				observation.Status != protocol.StatusObserved || observation.Result == nil ||
				observation.DBState == nil || observation.Metrics == nil || observation.Error != nil {
				t.Fatalf("observation envelope = %#v", observation)
			}
			for name, value := range map[string]*protocol.Value{
				"result": observation.Result, "db_state": observation.DBState, "metrics": observation.Metrics,
			} {
				if err := value.Validate(); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}

			switch test.id {
			case "SYS-013":
				if !systemStateTestBoolean(t, *observation.Result, "acquire_before_callback") ||
					systemStateTestBoolean(t, *observation.Result, "automatic_retry") {
					t.Fatal("coordinated fence did not preserve acquire-before-callback/no-retry")
				}
				invocations := systemStateTestField(t, *observation.Result, "callback_invocations")
				if systemStateTestInteger(t, invocations, "acquire_cancelled") != 0 ||
					systemStateTestInteger(t, invocations, "acquire_failed") != 0 ||
					systemStateTestInteger(t, invocations, "acquire_succeeded") != 1 {
					t.Fatalf("callback invocation facts = %#v", invocations)
				}
				if got := gdj0046TestString(t, *observation.Result, "commit_failure"); got != "commit_outcome_unknown" {
					t.Fatalf("commit failure classification = %q", got)
				}
				if got := gdj0046TestString(t, *observation.Result, "callback_cancellation"); got != "rolled_back" {
					t.Fatalf("callback cancellation = %q", got)
				}
				if gdj0046TestHasField(*observation.Result, "rollback_uncertainty") {
					t.Fatal("live SYS-013 result claimed package-private rollback uncertainty")
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "coordination_fences"); got != 1 {
					t.Fatalf("coordination fence proofs = %d", got)
				}
			case "SYS-014":
				if got := systemStateTestInteger(t, *observation.DBState, "credential_rows"); got != 1 {
					t.Fatalf("credential rows = %d", got)
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "bootstrap_winners"); got != 1 {
					t.Fatalf("bootstrap winners = %d", got)
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "secret_values_serialized"); got != 0 {
					t.Fatalf("serialized bootstrap secrets = %d", got)
				}
				if got := systemStateTestInteger(t, *observation.DBState, "published_materials"); got != 1 {
					t.Fatalf("published bootstrap materials = %d", got)
				}
				if got := systemStateTestInteger(t, *observation.DBState, "mismatch_writes"); got != 0 {
					t.Fatalf("mismatched bootstrap writes = %d", got)
				}
			case "SYS-015":
				if !systemStateTestBoolean(t, *observation.DBState, "capacity_bound_preserved") ||
					systemStateTestBoolean(t, *observation.DBState, "unbounded_reap") {
					t.Fatal("global capacity or bounded reap fact failed")
				}
				if got := systemStateTestInteger(t, *observation.DBState, "duplicate_digests"); got != 0 {
					t.Fatalf("duplicate digests = %d", got)
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "raw_bearers_observed"); got != 0 {
					t.Fatalf("persisted/serialized raw bearers = %d", got)
				}
			case "SYS-016":
				if !systemStateTestBoolean(t, *observation.Result, "accessed_at_monotonic") ||
					!systemStateTestBoolean(t, *observation.Result, "idle_expiry_monotonic") {
					t.Fatal("out-of-order touch regressed timestamps")
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "touch_winners"); got != 1 {
					t.Fatalf("touch winners = %d", got)
				}
			case "SYS-017":
				if got := systemStateTestInteger(t, *observation.Metrics, "rotation_winners"); got != 1 {
					t.Fatalf("rotation winners = %d", got)
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "resurrection_writes"); got != 0 {
					t.Fatalf("resurrection writes = %d", got)
				}
				if systemStateTestBoolean(t, *observation.Result, "old_bearer_resurrected") {
					t.Fatal("old bearer was resurrected")
				}
			case "SYS-018":
				if !systemStateTestBoolean(t, *observation.Result, "article_and_audit_atomic") ||
					!systemStateTestBoolean(t, *observation.Result, "global_history_bound_preserved") {
					t.Fatal("Article/audit atomicity or global prune bound failed")
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "partial_commits"); got != 0 {
					t.Fatalf("partial commits = %d", got)
				}
				for _, field := range []string{"article_rows_after_fault", "audit_rows_after_fault", "orphan_audit_rows"} {
					if got := systemStateTestInteger(t, *observation.DBState, field); got != 0 {
						t.Fatalf("%s = %d", field, got)
					}
				}
			case "SYS-019":
				if !systemStateTestBoolean(t, *observation.Result, "active_key_signs_new_values") ||
					!systemStateTestBoolean(t, *observation.Metrics, "verification_key_set_bounded") {
					t.Fatal("active-only signing or bounded verification failed")
				}
				if systemStateTestBoolean(t, *observation.DBState, "key_material_persisted") ||
					systemStateTestBoolean(t, *observation.DBState, "provider_state_owned_by_framework") ||
					systemStateTestBoolean(t, *observation.DBState, "ring_mutable") {
					t.Fatal("CSRF ring persisted key material, owned provider state, or retained caller mutability")
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "secret_values_serialized"); got != 0 {
					t.Fatalf("serialized CSRF secrets = %d", got)
				}
			}
		})
	}
}

func gdj0046TestHasField(object protocol.Value, name string) bool {
	if object.Type != protocol.ValueObject {
		return false
	}
	for _, field := range object.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func gdj0046TestString(t *testing.T, object protocol.Value, name string) string {
	t.Helper()
	value := systemStateTestField(t, object, name)
	if value.Type != protocol.ValueString || value.Text == nil {
		t.Fatalf("field %q type = %q, want string", name, value.Type)
	}
	return *value.Text
}
