//go:build darwin || linux

package godj

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

var migrationStatusExpectedRegistrations = []struct {
	id       string
	scenario string
	phase    protocol.Phase
}{
	{id: "MIG-111", scenario: "godj.migration.status.empty_catalog", phase: protocol.PhaseEvaluation},
	{id: "MIG-112", scenario: "django.migration.status.fresh_unapplied", phase: protocol.PhaseEvaluation},
	{id: "MIG-113", scenario: "django.migration.status.applied_prefix", phase: protocol.PhaseEvaluation},
	{id: "MIG-114", scenario: "django.migration.status.fully_applied_restart", phase: protocol.PhaseEvaluation},
	{id: "MIG-115", scenario: "django.migration.status.cross_app_branch_order", phase: protocol.PhaseEvaluation},
	{id: "MIG-116", scenario: "godj.migration.status.unknown_record_visible", phase: protocol.PhaseEvaluation},
	{id: "MIG-117", scenario: "godj.migration.status.inconsistent_known_history", phase: protocol.PhaseEvaluation},
	{id: "MIG-118", scenario: "godj.migration.status.project_boundary", phase: protocol.PhaseEnvironment},
}

func TestMigrationStatusRegistryIsExactAndFailsClosed(t *testing.T) {
	if len(migrationStatusScenarioRegistry) != len(migrationStatusExpectedRegistrations) {
		t.Fatalf("migration-status registry size = %d, want %d", len(migrationStatusScenarioRegistry), len(migrationStatusExpectedRegistrations))
	}
	for _, expected := range migrationStatusExpectedRegistrations {
		registration, ok := migrationStatusScenarioRegistry[expected.scenario]
		if !ok || registration.handler == nil || registration.id != expected.id || registration.phase != expected.phase {
			t.Fatalf("migration-status registration %q = %#v", expected.scenario, registration)
		}
	}
	handler, ok := migrationStatusScenarioHandler(migrationStatusExpectedRegistrations[0].scenario)
	if !ok {
		t.Fatal("known migration-status scenario is missing")
	}
	if _, err := handler(context.Background(), protocol.Contract{
		ID: "MIG-999", Scenario: migrationStatusExpectedRegistrations[0].scenario, Phase: protocol.PhaseEvaluation,
	}); err == nil {
		t.Fatal("migration-status handler accepted a wrong id")
	}
	if _, err := handler(context.Background(), protocol.Contract{
		ID: "MIG-111", Scenario: "wrong", Phase: protocol.PhaseEvaluation,
	}); err == nil {
		t.Fatal("migration-status handler accepted a wrong scenario")
	}
	if _, err := handler(context.Background(), protocol.Contract{
		ID: "MIG-111", Scenario: migrationStatusExpectedRegistrations[0].scenario, Phase: protocol.PhaseCommit,
	}); err == nil {
		t.Fatal("migration-status handler accepted a wrong phase")
	}
	if handler, ok := migrationStatusScenarioHandler("godj.migration.status.unknown"); ok || handler != nil {
		t.Fatalf("unknown migration-status handler = %v, %t", handler, ok)
	}
}

func TestMigrationStatusActualSourceIsOracleBlindAndCounterDerived(t *testing.T) {
	document, err := os.ReadFile("migration_status_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(document)
	for _, forbidden := range []string{
		"conformance/oracles/", "migration-status-oracle.json", "migration-status-not-implemented.json",
		"conformance/contracts/", "runners/django/", "protocol.Compare(", "LoadObservationSuite(",
		"migrationStatusInt(16)", `protocol.Integer("16")`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("migration-status actual source contains forbidden shortcut %q", forbidden)
		}
	}
	for _, required := range []string{
		"json.Marshal(struct {", "productcheck.ShowMigrationsReport", "linked.Report",
		"bytes.Count(reportArtifact", "migrationStatusInt(len(cases))",
		"migrationStatusInt(applicationMutations)", "migrationStatusInt(recorderMutations)",
		"migrationStatusInt(revisionMutations)", "migrationStatusInt(schemaMutations)",
		"trace.events", `record("revision_session_close")`, `record("backend_close")`,
		`lastEvent(execution) != "stderr_publication"`,
		"closed-snapshot cancellation callback did not run",
		"session.database.schemaMutations++", "session.database.recorderMutations++",
		"session.database.revisionMutations++", "session.database.applicationMutations++",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("migration-status actual source is missing derived evidence boundary %q", required)
		}
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "migration_status_scenarios.go", document, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbiddenCalls := map[string]bool{
		"ReadFile": true, "Open": true, "OpenFile": true, "ReadAll": true,
		"LoadManifest": true, "LoadObservationSuite": true, "Compare": true,
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && forbiddenCalls[selector.Sel.Name] {
			t.Errorf("migration-status actual source contains forbidden call %s", selector.Sel.Name)
		}
		return true
	})
}

func TestMigrationStatusScenariosExecuteActualBoundaries(t *testing.T) {
	for _, expected := range migrationStatusExpectedRegistrations {
		expected := expected
		t.Run(expected.id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			handler, ok := migrationStatusScenarioHandler(expected.scenario)
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
			if bytes := strings.Count(string(document), migrationStatusSecretCanary); bytes != 0 {
				t.Fatalf("%s serialized actual observation contains %d private canaries", expected.id, bytes)
			}
			switch expected.id {
			case "MIG-116":
				for _, fragment := range []string{`"9999_removed"`, `"0000_removed"`, `"0001_gone"`, `"unknown"`} {
					if !strings.Contains(string(document), fragment) {
						t.Fatalf("MIG-116 is missing actual unknown-history fact %s", fragment)
					}
				}
			case "MIG-118":
				if strings.Count(string(document), `"name","value":{"type":"string","value":`) < len(migrationStatusExpectedRegistrations) {
					t.Fatalf("MIG-118 did not serialize its actual case reports: %s", document)
				}
			}
		})
	}
}
