package godj

// Inputs contains product facts that must be collected outside the portable
// observation runner. Values are injected only after their owning boundary has
// verified provenance and integrity; scenario code never opens the checked
// evidence path itself.
type Inputs struct {
	SystemStatePostgreSQLTwoProcess *SystemStateTwoProcessBackendFacts
	ProjectOperatorPostgreSQL       *GDJ0055OperatorBackendFacts
	ProjectOperatorSQLite           *GDJ0055OperatorBackendFacts
}

func (inputs Inputs) snapshot() Inputs {
	var snapshot Inputs
	if inputs.SystemStatePostgreSQLTwoProcess != nil {
		postgresql := *inputs.SystemStatePostgreSQLTwoProcess
		snapshot.SystemStatePostgreSQLTwoProcess = &postgresql
	}
	if inputs.ProjectOperatorPostgreSQL != nil {
		operator := *inputs.ProjectOperatorPostgreSQL
		snapshot.ProjectOperatorPostgreSQL = &operator
	}
	if inputs.ProjectOperatorSQLite != nil {
		operator := *inputs.ProjectOperatorSQLite
		snapshot.ProjectOperatorSQLite = &operator
	}
	return snapshot
}

// SystemStateTwoProcessBackendFacts is the oracle-independent subset of a
// required backend run consumed by SYS-020. Failure facts deliberately remain
// representable so the scenario comparison cannot turn them into success.
type SystemStateTwoProcessBackendFacts struct {
	Backend                      string
	WriterProcesses              int
	BarrierRace                  string
	HolderCallbackInvocations    int
	ContenderCallbackInvocations int
	CleanRestartPreserved        bool
	SameDatabaseOrSchema         bool
	CrossProcessStateDivergence  int
	RestartStateLoss             int
	SchemaDrift                  bool
	SecretValuesSerialized       int
}
