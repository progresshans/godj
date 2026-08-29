//go:build darwin || linux

package godj

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

var migrationWriterExpectedRegistrations = []struct {
	id       string
	scenario string
	phase    protocol.Phase
}{
	{id: "MIG-099", scenario: "django.migration.writer.no_changes_clean", phase: protocol.PhaseConstruction},
	{id: "MIG-100", scenario: "django.migration.writer.fresh_initial", phase: protocol.PhaseConstruction},
	{id: "MIG-101", scenario: "django.migration.writer.repeat_after_initial_noop", phase: protocol.PhaseConstruction},
	{id: "MIG-102", scenario: "godj.migration.writer.deterministic_candidate", phase: protocol.PhaseConstruction},
	{id: "MIG-103", scenario: "django.migration.writer.relation_dependency_topology", phase: protocol.PhaseConstruction},
	{id: "MIG-104", scenario: "django.migration.writer.additive_model_and_field_tail", phase: protocol.PhaseConstruction},
	{id: "MIG-105", scenario: "django.migration.writer.dry_run_no_mutation", phase: protocol.PhaseEnvironment},
	{id: "MIG-106", scenario: "django.migration.writer.check_clean_and_drift", phase: protocol.PhaseEnvironment},
	{id: "MIG-107", scenario: "godj.migration.writer.unsupported_delta_fail_closed", phase: protocol.PhaseConstruction},
	{id: "MIG-108", scenario: "godj.migration.writer.snapshot_and_protocol_boundary", phase: protocol.PhaseEnvironment},
	{id: "MIG-109", scenario: "godj.migration.writer.atomic_concurrent_publication", phase: protocol.PhaseCommit},
	{id: "MIG-110", scenario: "godj.migration.writer.interruption_recovery_and_roundtrip", phase: protocol.PhaseRollback},
}

func TestMigrationWriterRegistryIsExactAndFailsClosed(t *testing.T) {
	if len(migrationWriterScenarioRegistry) != len(migrationWriterExpectedRegistrations) {
		t.Fatalf("migration-writer registry size = %d, want %d", len(migrationWriterScenarioRegistry), len(migrationWriterExpectedRegistrations))
	}
	for _, expected := range migrationWriterExpectedRegistrations {
		registration, ok := migrationWriterScenarioRegistry[expected.scenario]
		if !ok || registration.handler == nil || registration.id != expected.id || registration.phase != expected.phase {
			t.Fatalf("migration-writer registration %q = %#v", expected.scenario, registration)
		}
	}
	handler, ok := migrationWriterScenarioHandler(migrationWriterExpectedRegistrations[0].scenario)
	if !ok {
		t.Fatal("known migration-writer scenario is missing")
	}
	if _, err := handler(context.Background(), protocol.Contract{ID: "MIG-999", Scenario: migrationWriterExpectedRegistrations[0].scenario, Phase: protocol.PhaseConstruction}); err == nil {
		t.Fatal("migration-writer handler accepted a wrong id")
	}
	if _, err := handler(context.Background(), protocol.Contract{ID: "MIG-099", Scenario: "wrong", Phase: protocol.PhaseConstruction}); err == nil {
		t.Fatal("migration-writer handler accepted a wrong scenario")
	}
	if _, err := handler(context.Background(), protocol.Contract{ID: "MIG-099", Scenario: migrationWriterExpectedRegistrations[0].scenario, Phase: protocol.PhaseCommit}); err == nil {
		t.Fatal("migration-writer handler accepted a wrong phase")
	}
	if handler, ok := migrationWriterScenarioHandler("godj.migration.writer.unknown"); ok || handler != nil {
		t.Fatalf("unknown migration-writer handler = %v, %t", handler, ok)
	}
}

func TestMigrationWriterActualSourceIsOracleBlind(t *testing.T) {
	for _, name := range []string{"migration_writer_scenarios.go"} {
		document, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"conformance/oracles/", "migration-writer-oracle.json", "migration-writer-not-implemented.json", "godj-migration-writer-deviation-expected.json", "conformance/contracts/"} {
			if strings.Contains(string(document), forbidden) {
				t.Fatalf("migration-writer actual source %q contains artifact boundary %q", name, forbidden)
			}
		}
	}
}

func TestMigrationWriterScenariosExecuteActualBoundaries(t *testing.T) {
	for _, expected := range migrationWriterExpectedRegistrations {
		expected := expected
		t.Run(expected.id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			handler, ok := migrationWriterScenarioHandler(expected.scenario)
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
			document, err := json.Marshal(observation)
			if err != nil {
				t.Fatal(err)
			}
			switch expected.id {
			case "MIG-103":
				if !strings.Contains(string(document), "PROTECT") || strings.Contains(string(document), "CASCADE") {
					t.Fatalf("MIG-103 did not preserve the actual GoDj delete policy: %s", document)
				}
			case "MIG-107":
				for _, code := range []string{"unsupported_change", "invalid_relation"} {
					if !strings.Contains(string(document), code) {
						t.Fatalf("MIG-107 is missing actual code %q: %s", code, document)
					}
				}
				for _, invented := range []string{"unsupported_delta", "relation_cycle"} {
					if strings.Contains(string(document), invented) {
						t.Fatalf("MIG-107 emitted non-GoDj code %q: %s", invented, document)
					}
				}
			}
		})
	}
}
