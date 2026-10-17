//go:build darwin || linux

package godj

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

var migrationCommandExpectedRegistrations = []struct {
	id       string
	scenario string
	phase    protocol.Phase
}{
	{id: "MIG-087", scenario: "godj.migration.command.fresh_latest", phase: protocol.PhaseCommit},
	{id: "MIG-088", scenario: "godj.migration.command.applied_prefix_tail", phase: protocol.PhaseCommit},
	{id: "MIG-089", scenario: "godj.migration.command.fully_applied_fresh_noop", phase: protocol.PhaseCommit},
	{id: "MIG-090", scenario: "godj.migration.command.definition_preflight_before_backend", phase: protocol.PhaseEvaluation},
	{id: "MIG-091", scenario: "godj.migration.command.inconsistent_history_preflight", phase: protocol.PhaseEvaluation},
	{id: "MIG-092", scenario: "godj.migration.command.capability_preflight_before_begin", phase: protocol.PhaseEvaluation},
	{id: "MIG-093", scenario: "godj.migration.command.middle_failure_durable_prefix", phase: protocol.PhaseRollback},
	{id: "MIG-094", scenario: "godj.migration.command.fresh_resume_after_failure", phase: protocol.PhaseCommit},
	{id: "MIG-095", scenario: "godj.migration.command.commit_outcome_unknown", phase: protocol.PhaseCommit},
	{id: "MIG-096", scenario: "godj.migration.command.concurrent_latest_fenced", phase: protocol.PhaseCommit},
	{id: "MIG-097", scenario: "godj.migration.command.backend_configuration_secret_boundary", phase: protocol.PhaseEnvironment},
	{id: "MIG-098", scenario: "godj.migration.command.interrupt_rollback_cleanup", phase: protocol.PhaseRollback},
}

func TestMigrationCommandRegistryMatchesExpectedContracts(t *testing.T) {
	if len(migrationCommandScenarioRegistry) != len(migrationCommandExpectedRegistrations) {
		t.Fatalf("migration-command registry size = %d, want %d", len(migrationCommandScenarioRegistry), len(migrationCommandExpectedRegistrations))
	}
	for _, expected := range migrationCommandExpectedRegistrations {
		registration, ok := migrationCommandScenarioRegistry[expected.scenario]
		if !ok || registration.handler == nil {
			t.Fatalf("scenario %q is not registered with a handler", expected.scenario)
		}
		if registration.id != expected.id || registration.phase != expected.phase {
			t.Fatalf(
				"scenario %q registration = id:%q phase:%q, want id:%q phase:%q",
				expected.scenario, registration.id, registration.phase, expected.id, expected.phase,
			)
		}
	}
}

func TestMigrationCommandRegistryFailsClosedOnIdentityAndPhase(t *testing.T) {
	handler, ok := migrationCommandScenarioHandler(migrationCommandExpectedRegistrations[0].scenario)
	if !ok {
		t.Fatal("known migration-command scenario is missing")
	}
	if _, err := handler(context.Background(), protocol.Contract{ID: "MIG-999", Scenario: migrationCommandExpectedRegistrations[0].scenario, Phase: protocol.PhaseCommit}); err == nil {
		t.Fatal("migration-command handler accepted a wrong contract id")
	}
	if _, err := handler(context.Background(), protocol.Contract{ID: "MIG-087", Scenario: "godj.migration.command.wrong", Phase: protocol.PhaseCommit}); err == nil {
		t.Fatal("migration-command handler accepted a wrong contract scenario")
	}
	if _, err := handler(context.Background(), protocol.Contract{ID: "MIG-087", Scenario: migrationCommandExpectedRegistrations[0].scenario, Phase: protocol.PhaseEvaluation}); err == nil {
		t.Fatal("migration-command handler accepted a wrong phase")
	}
	if unknown, found := migrationCommandScenarioHandler("godj.migration.command.unknown"); found || unknown != nil {
		t.Fatalf("unknown migration-command handler = %v, %t", unknown, found)
	}
}

func TestMigrationCommandActualSourceIsOracleBlind(t *testing.T) {
	for _, path := range []string{
		"migration_command_scenarios.go",
		"migration_command_actual_process.go",
		"migration_command_exact_snapshot.go",
		"migrationcommandworker/worker_unix.go",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"conformance/oracles/", "migration-command-oracle.json",
			"migration-command-not-implemented.json", "conformance/contracts/",
		} {
			if strings.Contains(string(contents), forbidden) {
				t.Fatalf("migration-command actual source %q contains locked artifact boundary %q", path, forbidden)
			}
		}
	}
}

func TestMigrationCommandGlobalCLIDoesNotOwnBackendSecretConfiguration(t *testing.T) {
	repository, err := systemStateRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationCommandAssertGlobalSourceOwnership(repository); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationCommandGlobalCLISourceOwnershipLockRejectsForbiddenInputs(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "worker secret key literal",
			source: `package main; const secret = "GODJ_MIGRATION_COMMAND_SECRET"`,
		},
		{
			name:   "article database key literal",
			source: `package main; const database = "GODJ_ARTICLE_SQLITE_DATABASE"`,
		},
		{
			name:   "sqlite backend import",
			source: `package main; import _ "github.com/progresshans/godj/db/sqlite"`,
		},
		{
			name:   "article database config import",
			source: `package main; import _ "github.com/progresshans/godj/examples/article/databaseconfig"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, relative := range []string{"cmd/godj", "internal/projectcheck"} {
				directory := filepath.Join(root, filepath.FromSlash(relative))
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "safe.go"), []byte("package fixture\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "cmd", "godj", "forbidden.go"), []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := migrationCommandAssertGlobalSourceOwnership(root); err == nil {
				t.Fatal("global CLI source ownership lock accepted a forbidden input")
			}
		})
	}
}
