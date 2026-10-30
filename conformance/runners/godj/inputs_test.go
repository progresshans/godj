package godj

import "testing"

func TestInputsSnapshotCopiesExternalBackendFacts(t *testing.T) {
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
	operator := GDJ0055OperatorBackendFacts{
		Backend:                             "postgresql_17_10",
		ProvisionProcesses:                  1,
		RuntimeProcesses:                    2,
		DistinctProcesses:                   3,
		ProvisionCalls:                      4,
		CredentialRows:                      5,
		Provisioned:                         false,
		AdminAuthenticated:                  true,
		APIAuthenticated:                    false,
		DistinctProcessRestart:              true,
		ProvisionProcessDistinctFromRuntime: false,
		RestartRawSecretInput:               true,
		RestartStateLoss:                    6,
		SchemaDrift:                         true,
		RawSecretOccurrences:                7,
	}
	sqliteOperator := operator
	sqliteOperator.Backend = "sqlite"
	wantOperator := operator
	wantSQLiteOperator := sqliteOperator
	got := (Inputs{
		SystemStatePostgreSQLTwoProcess: &facts,
		ProjectOperatorPostgreSQL:       &operator,
		ProjectOperatorSQLite:           &sqliteOperator,
	}).snapshot()
	if got.SystemStatePostgreSQLTwoProcess == nil || got.SystemStatePostgreSQLTwoProcess == &facts {
		t.Fatalf("snapshot PostgreSQL facts = %#v, want independent value", got.SystemStatePostgreSQLTwoProcess)
	}
	facts.Backend = "mutated"
	if got.SystemStatePostgreSQLTwoProcess.Backend != "postgresql_17_10" {
		t.Fatalf("snapshot backend = %q after caller mutation", got.SystemStatePostgreSQLTwoProcess.Backend)
	}
	if got.ProjectOperatorPostgreSQL == nil || got.ProjectOperatorPostgreSQL == &operator {
		t.Fatalf("snapshot operator PostgreSQL facts = %#v, want independent value", got.ProjectOperatorPostgreSQL)
	}
	operator = GDJ0055OperatorBackendFacts{Backend: "mutated"}
	if *got.ProjectOperatorPostgreSQL != wantOperator {
		t.Fatalf("snapshot operator facts = %#v after caller mutation, want %#v", *got.ProjectOperatorPostgreSQL, wantOperator)
	}
	if got.ProjectOperatorSQLite == nil || got.ProjectOperatorSQLite == &sqliteOperator {
		t.Fatalf("snapshot operator SQLite facts = %#v, want independent value", got.ProjectOperatorSQLite)
	}
	sqliteOperator = GDJ0055OperatorBackendFacts{Backend: "mutated"}
	if *got.ProjectOperatorSQLite != wantSQLiteOperator {
		t.Fatalf("snapshot operator SQLite facts = %#v after caller mutation, want %#v", *got.ProjectOperatorSQLite, wantSQLiteOperator)
	}

	onlyOperator := (Inputs{ProjectOperatorPostgreSQL: &operator}).snapshot()
	if onlyOperator.SystemStatePostgreSQLTwoProcess != nil || onlyOperator.ProjectOperatorPostgreSQL == nil || onlyOperator.ProjectOperatorSQLite != nil {
		t.Fatalf("operator-only snapshot = %#v", onlyOperator)
	}

	zero := (Inputs{}).snapshot()
	if zero.SystemStatePostgreSQLTwoProcess != nil || zero.ProjectOperatorPostgreSQL != nil || zero.ProjectOperatorSQLite != nil {
		t.Fatalf("zero snapshot = %#v", zero)
	}
}
