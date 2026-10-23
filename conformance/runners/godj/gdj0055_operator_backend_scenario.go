//go:build darwin || linux

package godj

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func gdj0055OperatorBackendLoginRestart(
	ctx context.Context,
	contract protocol.Contract,
	inputs GDJ0055Inputs,
) (protocol.Observation, error) {
	if inputs.PostgreSQLOperatorBackend == nil {
		return protocol.Observation{}, errors.New("GDJ-0055 operator backend scenario: verified PostgreSQL evidence is missing")
	}
	if inputs.SQLiteOperatorBackend == nil {
		return protocol.Observation{}, errors.New("GDJ-0055 operator backend scenario: verified SQLite evidence is missing")
	}
	postgres := *inputs.PostgreSQLOperatorBackend
	if postgres.Backend != "postgresql_17_10" || gdj0055ValidateOperatorBackendFacts(postgres) != nil {
		return protocol.Observation{}, errors.New("GDJ-0055 operator backend scenario: PostgreSQL evidence is malformed")
	}
	sqliteFacts := *inputs.SQLiteOperatorBackend
	if sqliteFacts.Backend != "sqlite" || gdj0055ValidateOperatorBackendFacts(sqliteFacts) != nil {
		return protocol.Observation{}, errors.New("GDJ-0055 operator backend scenario: SQLite evidence is malformed")
	}
	return gdj0055OperatorBackendObservation(contract, sqliteFacts, postgres)
}

func gdj0055OperatorBackendObservation(
	contract protocol.Contract,
	sqliteFacts, postgres GDJ0055OperatorBackendFacts,
) (protocol.Observation, error) {
	if err := gdj0055ValidateOperatorBackendFacts(sqliteFacts); err != nil {
		return protocol.Observation{}, fmt.Errorf("GDJ-0055 SQLite operator facts: %w", err)
	}
	if err := gdj0055ValidateOperatorBackendFacts(postgres); err != nil {
		return protocol.Observation{}, fmt.Errorf("GDJ-0055 PostgreSQL operator facts: %w", err)
	}
	backendCases := []GDJ0055OperatorBackendFacts{postgres, sqliteFacts}
	values := make([]protocol.Value, len(backendCases))
	var rawSecrets, losses int
	var drift bool
	for index, facts := range backendCases {
		values[index] = protocol.Object(map[string]protocol.Value{
			"admin_authenticated":                     protocol.Boolean(facts.AdminAuthenticated),
			"api_authenticated":                       protocol.Boolean(facts.APIAuthenticated),
			"backend":                                 protocol.String(facts.Backend),
			"distinct_process_restart":                protocol.Boolean(facts.DistinctProcessRestart),
			"provision_process_distinct_from_runtime": protocol.Boolean(facts.ProvisionProcessDistinctFromRuntime),
			"provisioned":                             protocol.Boolean(facts.Provisioned),
		})
		rawSecrets += facts.RawSecretOccurrences
		losses += facts.RestartStateLoss
		drift = drift || facts.SchemaDrift
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"backend_cases": protocol.List(values...),
			"django_login_semantics_reused": protocol.Boolean(
				sqliteFacts.AdminAuthenticated && sqliteFacts.APIAuthenticated &&
					postgres.AdminAuthenticated && postgres.APIAuthenticated,
			),
			"restart_raw_secret_input": protocol.Boolean(postgres.RestartRawSecretInput || sqliteFacts.RestartRawSecretInput),
		}),
		protocol.Object(map[string]protocol.Value{
			"credential_rows_per_backend": systemStateInt(gdj0055CombineOperatorCount(sqliteFacts.CredentialRows, postgres.CredentialRows)),
			"restart_state_loss":          systemStateInt(losses),
			"schema_drift":                protocol.Boolean(drift),
		}),
		protocol.Object(map[string]protocol.Value{
			"distinct_processes_per_backend":  systemStateInt(gdj0055CombineOperatorCount(sqliteFacts.DistinctProcesses, postgres.DistinctProcesses)),
			"provision_calls_per_backend":     systemStateInt(gdj0055CombineOperatorCount(sqliteFacts.ProvisionCalls, postgres.ProvisionCalls)),
			"provision_processes_per_backend": systemStateInt(gdj0055CombineOperatorCount(sqliteFacts.ProvisionProcesses, postgres.ProvisionProcesses)),
			"raw_secret_occurrences":          systemStateInt(rawSecrets),
			"required_backend_cases":          systemStateInt(len(backendCases)),
			"runtime_processes_per_backend":   systemStateInt(gdj0055CombineOperatorCount(sqliteFacts.RuntimeProcesses, postgres.RuntimeProcesses)),
			"skipped_required_cases":          systemStateInt(0),
		}),
	)
}

func gdj0055ValidateOperatorBackendFacts(facts GDJ0055OperatorBackendFacts) error {
	if facts.Backend == "" {
		return errors.New("backend is empty")
	}
	for name, count := range map[string]int{
		"provision processes":    facts.ProvisionProcesses,
		"runtime processes":      facts.RuntimeProcesses,
		"distinct processes":     facts.DistinctProcesses,
		"provision calls":        facts.ProvisionCalls,
		"credential rows":        facts.CredentialRows,
		"restart state loss":     facts.RestartStateLoss,
		"raw secret occurrences": facts.RawSecretOccurrences,
	} {
		if count < 0 {
			return fmt.Errorf("%s count %d is negative", name, count)
		}
	}
	return nil
}

func gdj0055CombineOperatorCount(sqlite, postgres int) int {
	if sqlite == postgres {
		return sqlite
	}
	return -1
}
