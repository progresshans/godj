package godj

import (
	"fmt"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func systemStateTwoProcessObservation(
	contract protocol.Contract,
	sqliteFacts SystemStateTwoProcessBackendFacts,
	postgresqlFacts SystemStateTwoProcessBackendFacts,
) (protocol.Observation, error) {
	if err := validateSystemStateTwoProcessFacts(sqliteFacts); err != nil {
		return protocol.Observation{}, fmt.Errorf("SQLite two-process facts: %w", err)
	}
	if err := validateSystemStateTwoProcessFacts(postgresqlFacts); err != nil {
		return protocol.Observation{}, fmt.Errorf("PostgreSQL two-process facts: %w", err)
	}

	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"backend_cases": protocol.List(
				protocol.Object(map[string]protocol.Value{
					"backend":                 protocol.String(postgresqlFacts.Backend),
					"clean_restart_preserved": protocol.Boolean(postgresqlFacts.CleanRestartPreserved),
					"same_schema":             protocol.Boolean(postgresqlFacts.SameDatabaseOrSchema),
				}),
				protocol.Object(map[string]protocol.Value{
					"backend":                 protocol.String(sqliteFacts.Backend),
					"clean_restart_preserved": protocol.Boolean(sqliteFacts.CleanRestartPreserved),
					"same_database":           protocol.Boolean(sqliteFacts.SameDatabaseOrSchema),
				}),
			),
			"barrier_race": protocol.String(combineSystemStateBarrierRace(sqliteFacts.BarrierRace, postgresqlFacts.BarrierRace)),
		}),
		protocol.Object(map[string]protocol.Value{
			"cross_process_state_divergence": systemStateInt(sqliteFacts.CrossProcessStateDivergence + postgresqlFacts.CrossProcessStateDivergence),
			"restart_state_loss":             systemStateInt(sqliteFacts.RestartStateLoss + postgresqlFacts.RestartStateLoss),
			"schema_drift":                   protocol.Boolean(sqliteFacts.SchemaDrift || postgresqlFacts.SchemaDrift),
		}),
		protocol.Object(map[string]protocol.Value{
			"distinct_processes":       systemStateInt(combineSystemStateWriterProcesses(sqliteFacts.WriterProcesses, postgresqlFacts.WriterProcesses)),
			"required_backend_cases":   systemStateInt(2),
			"secret_values_serialized": systemStateInt(sqliteFacts.SecretValuesSerialized + postgresqlFacts.SecretValuesSerialized),
			"skipped_required_cases":   systemStateInt(0),
		}),
	)
}

func validateSystemStateTwoProcessFacts(facts SystemStateTwoProcessBackendFacts) error {
	if facts.Backend == "" {
		return fmt.Errorf("backend is empty")
	}
	if facts.WriterProcesses < 0 {
		return fmt.Errorf("writer process count %d is negative", facts.WriterProcesses)
	}
	if facts.BarrierRace == "" {
		return fmt.Errorf("barrier race is empty")
	}
	for name, count := range map[string]int{
		"cross-process divergence": facts.CrossProcessStateDivergence,
		"restart loss":             facts.RestartStateLoss,
		"secret serialization":     facts.SecretValuesSerialized,
	} {
		if count < 0 {
			return fmt.Errorf("%s count %d is negative", name, count)
		}
	}
	return nil
}

func combineSystemStateBarrierRace(sqlite, postgresql string) string {
	if sqlite == postgresql {
		return sqlite
	}
	return "backend_disagreement"
}

func combineSystemStateWriterProcesses(sqlite, postgresql int) int {
	if sqlite == postgresql {
		return sqlite
	}
	return -1
}
