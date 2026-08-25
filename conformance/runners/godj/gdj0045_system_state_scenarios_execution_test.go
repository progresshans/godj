package godj

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestSystemStateExpectedSessionDigestLocksDomain(t *testing.T) {
	const want = "4159c5d401af09c2a95c8c36a664299a4eae8676e8c406cdfb44fc5511bc2b4d"
	if got := systemStateExpectedSessionDigest(strings.Repeat("A", 43)); got != want {
		t.Fatalf("domain-separated session digest = %q, want %q", got, want)
	}
}

func TestSystemStateActualHandlersExecute(t *testing.T) {
	distinctProcesses := map[string]int{
		"SYS-003": 2,
		"SYS-004": 3,
		"SYS-008": 4,
		"SYS-009": 7,
		"SYS-010": 3,
		"SYS-011": 4,
	}
	tests := []struct {
		id       string
		scenario string
		phase    protocol.Phase
	}{
		{"SYS-001", "godj.system_state.explicit_migration_gate", protocol.PhaseEnvironment},
		{"SYS-002", "godj.system_state.admin_bootstrap_gate", protocol.PhaseCommit},
		{"SYS-003", "django.system_state.credential_permission_restart", protocol.PhaseEvaluation},
		{"SYS-004", "django.system_state.rotated_session_restart", protocol.PhaseCommit},
		{"SYS-005", "godj.system_state.session_expiry_and_touch", protocol.PhaseCommit},
		{"SYS-006", "godj.system_state.capacity_reap_and_rotate_rollback", protocol.PhaseRollback},
		{"SYS-007", "godj.system_state.digest_only_current_codec", protocol.PhaseEvaluation},
		{"SYS-008", "django.system_state.logout_restart_denial", protocol.PhaseCommit},
		{"SYS-009", "django.system_state.csrf_restart", protocol.PhaseCommit},
		{"SYS-010", "django.system_state.admin_audit_fault_rollback", protocol.PhaseRollback},
		{"SYS-011", "django.system_state.audit_history_restart", protocol.PhaseEvaluation},
		{"SYS-012", "godj.system_state.commit_outcome_unknown", protocol.PhaseCommit},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			handler := systemStateScenarioRegistry[test.scenario]
			observation, err := handler(t.Context(), protocol.Contract{ID: test.id, Phase: test.phase})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ID != test.id || observation.Phase != test.phase || observation.Status != protocol.StatusObserved {
				t.Fatalf("observation identity/status = (%q,%q,%q)", observation.ID, observation.Phase, observation.Status)
			}
			if want, ok := distinctProcesses[test.id]; ok {
				if observation.Metrics == nil || systemStateTestInteger(t, *observation.Metrics, "distinct_processes") != want {
					t.Fatalf("%s did not report %d actually observed child processes", test.id, want)
				}
			}
			switch test.id {
			case "SYS-003":
				if got := systemStateTestInteger(t, *observation.Metrics, "secret_values_serialized"); got != 0 {
					t.Fatalf("SYS-003 serialized secret occurrences = %d", got)
				}
			case "SYS-004":
				if !systemStateTestBoolean(t, *observation.Result, "same_cookie_handoff") {
					t.Fatal("SYS-004 did not preserve the exact cookie handoff")
				}
			case "SYS-008":
				if got := systemStateTestInteger(t, *observation.Result, "api_status"); got != 403 {
					t.Fatalf("SYS-008 API denial status = %d, want 403", got)
				}
				if got := systemStateTestInteger(t, *observation.Metrics, "resurrection_writes"); got != 0 {
					t.Fatalf("SYS-008 resurrection writes = %d", got)
				}
			case "SYS-009":
				fresh := systemStateTestField(t, *observation.Result, "fresh")
				stale := systemStateTestField(t, *observation.Result, "pre_restart")
				if got := systemStateTestInteger(t, fresh, "status"); got != 201 || !systemStateTestBoolean(t, fresh, "accepted") {
					t.Fatalf("SYS-009 fresh lane = status %d accepted false", got)
				}
				if got := systemStateTestInteger(t, stale, "status"); got != 403 || systemStateTestBoolean(t, stale, "accepted") {
					t.Fatalf("SYS-009 stale lane = status %d or accepted true", got)
				}
				for field, want := range map[string]int{
					"fresh_mutations":             1,
					"pre_restart_mutations":       0,
					"pre_restart_setup_mutations": 1,
					"secret_values_serialized":    0,
				} {
					if got := systemStateTestInteger(t, *observation.Metrics, field); got != want {
						t.Fatalf("SYS-009 metric %s = %d, want %d", field, got, want)
					}
				}
			case "SYS-010":
				if got := systemStateTestInteger(t, *observation.Result, "status"); got != 500 {
					t.Fatalf("SYS-010 fault status = %d, want 500", got)
				}
			case "SYS-011":
				if got := len(systemStateTestField(t, *observation.Result, "all_events").Items); got != 3 {
					t.Fatalf("SYS-011 all events = %d, want 3", got)
				}
				if got := len(systemStateTestField(t, *observation.Result, "newest_bounded").Items); got != 2 {
					t.Fatalf("SYS-011 newest bounded events = %d, want 2", got)
				}
			}
		})
	}
}

func TestSystemStateDistinctProcessHandlersAreAvailable(t *testing.T) {
	scenarios := []string{
		"django.system_state.credential_permission_restart",
		"django.system_state.rotated_session_restart",
		"django.system_state.logout_restart_denial",
		"django.system_state.csrf_restart",
		"django.system_state.admin_audit_fault_rollback",
		"django.system_state.audit_history_restart",
	}
	for _, scenario := range scenarios {
		if handler, ok := systemStateScenarioHandler(scenario); !ok || handler == nil {
			t.Fatalf("distinct-process scenario %q is unavailable", scenario)
		}
	}
}

func TestSystemStateManifestIsGloballyPublishedInOrder(t *testing.T) {
	t.Parallel()

	wantIdentifiers := []string{
		"SYS-001",
		"SYS-002",
		"SYS-003",
		"SYS-004",
		"SYS-005",
		"SYS-006",
		"SYS-007",
		"SYS-008",
		"SYS-009",
		"SYS-010",
		"SYS-011",
		"SYS-012",
	}
	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "system-state-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	identifiers, err := RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(identifiers) != len(wantIdentifiers) {
		t.Fatalf("global system-state registry count = %d, want %d", len(identifiers), len(wantIdentifiers))
	}
	for index, identifier := range identifiers {
		if identifier != wantIdentifiers[index] {
			t.Fatalf("global system-state registry %d = %q, want %q", index, identifier, wantIdentifiers[index])
		}
	}
}

func systemStateTestField(t *testing.T, object protocol.Value, name string) protocol.Value {
	t.Helper()
	if object.Type != protocol.ValueObject {
		t.Fatalf("value type = %q, want object for field %q", object.Type, name)
	}
	for _, field := range object.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	t.Fatalf("object omitted field %q", name)
	return protocol.Value{}
}

func systemStateTestInteger(t *testing.T, object protocol.Value, name string) int {
	t.Helper()
	value := systemStateTestField(t, object, name)
	if value.Type != protocol.ValueInt || value.Text == nil {
		t.Fatalf("field %q type = %q, want int", name, value.Type)
	}
	parsed, err := strconv.Atoi(*value.Text)
	if err != nil {
		t.Fatalf("field %q integer = %q: %v", name, *value.Text, err)
	}
	return parsed
}

func systemStateTestBoolean(t *testing.T, object protocol.Value, name string) bool {
	t.Helper()
	value := systemStateTestField(t, object, name)
	if value.Type != protocol.ValueBool || value.Bool == nil {
		t.Fatalf("field %q type = %q, want bool", name, value.Type)
	}
	return *value.Bool
}
