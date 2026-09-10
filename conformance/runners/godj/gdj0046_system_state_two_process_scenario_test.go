package godj

import (
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestSystemStateTwoProcessObservationPreservesBackendFacts(t *testing.T) {
	t.Parallel()

	contract := protocol.Contract{ID: "SYS-020", Phase: protocol.PhaseEnvironment}
	sqliteFacts := systemStateTestTwoProcessFacts("sqlite")
	postgresqlFacts := systemStateTestTwoProcessFacts("postgresql_17_10")
	observation, err := systemStateTwoProcessObservation(contract, sqliteFacts, postgresqlFacts)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status != protocol.StatusObserved || observation.Result == nil || observation.DBState == nil || observation.Metrics == nil {
		t.Fatalf("SYS-020 observation = %#v", observation)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "distinct_processes"); got != 2 {
		t.Fatalf("distinct processes = %d, want 2", got)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "required_backend_cases"); got != 2 {
		t.Fatalf("required backend cases = %d, want 2", got)
	}
	if got := systemStateTestField(t, *observation.Result, "barrier_race"); got.Text == nil || *got.Text != "linearized" {
		t.Fatalf("barrier race = %#v", got)
	}
}

func TestSystemStateTwoProcessObservationDoesNotCoerceFailureFacts(t *testing.T) {
	t.Parallel()

	contract := protocol.Contract{ID: "SYS-020", Phase: protocol.PhaseEnvironment}
	sqliteFacts := systemStateTestTwoProcessFacts("sqlite")
	postgresqlFacts := systemStateTestTwoProcessFacts("postgresql_17_10")
	postgresqlFacts.WriterProcesses = 1
	postgresqlFacts.BarrierRace = "not_linearized"
	postgresqlFacts.CleanRestartPreserved = false
	postgresqlFacts.SameDatabaseOrSchema = false
	postgresqlFacts.CrossProcessStateDivergence = 2
	postgresqlFacts.RestartStateLoss = 3
	postgresqlFacts.SchemaDrift = true
	postgresqlFacts.SecretValuesSerialized = 4

	observation, err := systemStateTwoProcessObservation(contract, sqliteFacts, postgresqlFacts)
	if err != nil {
		t.Fatal(err)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "distinct_processes"); got != -1 {
		t.Fatalf("writer disagreement = %d, want -1", got)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "secret_values_serialized"); got != 4 {
		t.Fatalf("secret serialization = %d, want 4", got)
	}
	if got := systemStateTestInteger(t, *observation.DBState, "cross_process_state_divergence"); got != 2 {
		t.Fatalf("divergence = %d, want 2", got)
	}
	if got := systemStateTestInteger(t, *observation.DBState, "restart_state_loss"); got != 3 {
		t.Fatalf("restart loss = %d, want 3", got)
	}
	if !systemStateTestBoolean(t, *observation.DBState, "schema_drift") {
		t.Fatal("schema drift failure fact was lost")
	}
	if got := systemStateTestField(t, *observation.Result, "barrier_race"); got.Text == nil || *got.Text != "backend_disagreement" {
		t.Fatalf("barrier disagreement = %#v", got)
	}
	backendCases := systemStateTestField(t, *observation.Result, "backend_cases")
	if len(backendCases.Items) != 2 {
		t.Fatalf("backend cases = %#v", backendCases)
	}
	postgresql := backendCases.Items[0]
	if systemStateTestBoolean(t, postgresql, "clean_restart_preserved") || systemStateTestBoolean(t, postgresql, "same_schema") {
		t.Fatalf("PostgreSQL failure facts were coerced: %#v", postgresql)
	}
}

func systemStateTestTwoProcessFacts(backend string) SystemStateTwoProcessBackendFacts {
	return SystemStateTwoProcessBackendFacts{
		Backend:                      backend,
		WriterProcesses:              2,
		BarrierRace:                  "linearized",
		HolderCallbackInvocations:    1,
		ContenderCallbackInvocations: 1,
		CleanRestartPreserved:        true,
		SameDatabaseOrSchema:         true,
	}
}
