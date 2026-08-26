package godj

import "testing"

func TestInputsSnapshotCopiesPostgreSQLFacts(t *testing.T) {
	t.Parallel()

	facts := SystemStateTwoProcessBackendFacts{
		Backend:                      "postgresql_17_10",
		WriterProcesses:              2,
		BarrierRace:                  "linearized",
		HolderCallbackInvocations:    1,
		ContenderCallbackInvocations: 1,
		CleanRestartPreserved:        true,
		SameDatabaseOrSchema:         true,
	}
	got := (Inputs{SystemStatePostgreSQLTwoProcess: &facts}).snapshot()
	if got.SystemStatePostgreSQLTwoProcess == nil || got.SystemStatePostgreSQLTwoProcess == &facts {
		t.Fatalf("snapshot PostgreSQL facts = %#v, want independent value", got.SystemStatePostgreSQLTwoProcess)
	}
	facts.Backend = "mutated"
	if got.SystemStatePostgreSQLTwoProcess.Backend != "postgresql_17_10" {
		t.Fatalf("snapshot backend = %q after caller mutation", got.SystemStatePostgreSQLTwoProcess.Backend)
	}

	zero := (Inputs{}).snapshot()
	if zero.SystemStatePostgreSQLTwoProcess != nil {
		t.Fatalf("zero snapshot = %#v", zero)
	}
}
