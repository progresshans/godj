package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	godjrunner "github.com/progresshans/godj/conformance/runners/godj"
)

func TestRunGDJ0051StrictProductExpectationWritesEightActuals(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-status-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-status-oracle.json")
	actualPath := filepath.Join(t.TempDir(), "migration-status-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if want := "GoDj observations match the locked Django oracle for 8 contracts"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	document, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"migration-status-oracle.json", "godj-migration-status-not-implemented.json", migrationStatusPolicySecretCanary,
	} {
		if bytes.Contains(document, []byte(forbidden)) {
			t.Fatalf("GDJ-0051 actual artifact contains forbidden expected/private boundary %q", forbidden)
		}
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 8 || actual.Contracts[0].ID != "MIG-111" || actual.Contracts[7].ID != "MIG-118" {
		t.Fatalf("GDJ-0051 actual contracts = %#v", actual.Contracts)
	}
	for _, observation := range actual.Contracts {
		if observation.Status != protocol.StatusObserved {
			t.Fatalf("%s actual status = %q, want observed", observation.ID, observation.Status)
		}
	}
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	required, err := godjrunner.RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 8 || required[0] != "MIG-111" || required[7] != "MIG-118" {
		t.Fatalf("GDJ-0051 required product contracts = %v", required)
	}
	assertGDJ0051BoundaryCounters(t, actual.Contracts[7])
}

const migrationStatusPolicySecretCanary = "godj-migration-status-private-secret-canary"

func assertGDJ0051BoundaryCounters(t *testing.T, observation protocol.Observation) {
	t.Helper()
	if observation.Result == nil || observation.DBState == nil || observation.Metrics == nil {
		t.Fatalf("MIG-118 payload shape = %#v", observation)
	}
	casesValue := gdj0051Field(t, *observation.Result, "cases")
	if casesValue.Type != protocol.ValueList || len(casesValue.Items) != 16 {
		t.Fatalf("MIG-118 cases = %#v", casesValue)
	}
	cleanupFailures, fenceFailures, successes, terminalFailures := 0, 0, 0, 0
	wantNames := []string{
		"invalid_arguments", "invalid_definition", "pre_acquisition_cancel", "success",
		"partial_backend_acquisition", "partial_session_acquisition", "history_read_failure",
		"revision_fence_adoption_required", "stale_history_revision", "history_revision_contended",
		"history_revision_integrity", "session_close_failure", "outer_close_failure",
		"closed_snapshot_then_cancel", "terminal_stdout_short_write", "terminal_stdout_error",
	}
	for index, current := range casesValue.Items {
		if got := gdj0051String(t, gdj0051Field(t, current, "name")); got != wantNames[index] {
			t.Fatalf("MIG-118 case %d name = %q, want %q", index, got, wantNames[index])
		}
		if gdj0051Bool(t, gdj0051Field(t, current, "cleanup_failed")) {
			cleanupFailures++
		}
		category := gdj0051Field(t, current, "category")
		if category.Type == protocol.ValueString {
			value := gdj0051String(t, category)
			if value == "migration_capability_error" || value == "migration_conflict_error" ||
				value == "migration_transaction_error" ||
				(value == "migration_history_error" && gdj0051String(t, gdj0051Field(t, current, "code")) == "history_revision_integrity") {
				fenceFailures++
			}
		}
		if gdj0051Bool(t, gdj0051Field(t, current, "snapshot_published")) {
			successes++
		}
		if gdj0051Int(t, gdj0051Field(t, current, "stdout_write_attempts")) == 1 &&
			gdj0051String(t, gdj0051Field(t, current, "outcome")) == "error" &&
			gdj0051String(t, gdj0051Field(t, current, "category")) == "migration_project_internal_error" {
			terminalFailures++
		}
	}
	metrics := map[string]int{
		"cases": len(casesValue.Items), "cleanup_failure_cases": cleanupFailures,
		"revision_fence_failure_cases": fenceFailures, "successful_snapshot_cases": successes,
		"terminal_publication_failure_cases": terminalFailures,
	}
	for name, want := range metrics {
		if got := gdj0051Int(t, gdj0051Field(t, *observation.Metrics, name)); got != want {
			t.Fatalf("MIG-118 metric %s = %d, want derived %d", name, got, want)
		}
	}
	for _, name := range []string{
		"artifact_secret_occurrences", "protocol_secret_occurrences", "stderr_secret_occurrences", "stdout_secret_occurrences",
	} {
		if got := gdj0051Int(t, gdj0051Field(t, *observation.Metrics, name)); got != 0 {
			t.Fatalf("MIG-118 metric %s = %d, want 0", name, got)
		}
	}
	for _, name := range []string{"application_mutations", "recorder_mutations", "revision_mutations", "schema_mutations"} {
		if got := gdj0051Int(t, gdj0051Field(t, *observation.DBState, name)); got != 0 {
			t.Fatalf("MIG-118 db_state %s = %d, want actual sum 0", name, got)
		}
	}
}

func gdj0051Field(t *testing.T, value protocol.Value, name string) protocol.Value {
	t.Helper()
	if value.Type != protocol.ValueObject {
		t.Fatalf("value type = %q, want object for field %q", value.Type, name)
	}
	for _, field := range value.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	t.Fatalf("object is missing field %q", name)
	return protocol.Value{}
}

func gdj0051Int(t *testing.T, value protocol.Value) int {
	t.Helper()
	if value.Type != protocol.ValueInt || value.Text == nil {
		t.Fatalf("value = %#v, want int", value)
	}
	parsed, err := strconv.Atoi(*value.Text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func gdj0051String(t *testing.T, value protocol.Value) string {
	t.Helper()
	if value.Type != protocol.ValueString || value.Text == nil {
		t.Fatalf("value = %#v, want string", value)
	}
	return *value.Text
}

func gdj0051Bool(t *testing.T, value protocol.Value) bool {
	t.Helper()
	if value.Type != protocol.ValueBool || value.Bool == nil {
		t.Fatalf("value = %#v, want bool", value)
	}
	return *value.Bool
}

func TestGDJ0051HistoricalNotImplementedArtifactRemainsReferenceOnly(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	baseline, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-status-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Contracts) != 8 {
		t.Fatalf("historical migration-status baseline count = %d", len(baseline.Contracts))
	}
	for _, observation := range baseline.Contracts {
		if observation.Status != protocol.StatusNotImplemented || observation.Result != nil || observation.Error != nil || observation.DBState != nil || observation.Metrics != nil {
			t.Fatalf("historical migration-status baseline contract = %#v", observation)
		}
	}
}
