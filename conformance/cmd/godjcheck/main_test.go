package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestRunMatchesLockedOracleAndWritesActualSuite(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked Django oracle for 11 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatalf("LoadObservationSuite(actual) error = %v", err)
	}
	if len(actual.Contracts) != 11 {
		t.Fatalf("actual contract count = %d, want 11", len(actual.Contracts))
	}
	if info, err := os.Stat(actualPath); err != nil {
		t.Fatalf("Stat(actual) error = %v", err)
	} else if info.Mode().Perm() != 0o644 {
		t.Fatalf("actual mode = %o, want 644", info.Mode().Perm())
	}
}

func TestRunMatchesLockedWriteMigrationOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "write-migration-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "write-migration-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "write-migration-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked Django oracle for 11 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatalf("LoadObservationSuite(actual) error = %v", err)
	}
	if len(actual.Contracts) != 11 || actual.Contracts[0].ID != "MOD-001" || actual.Contracts[10].ID != "MIG-004" {
		t.Fatalf("actual write/migration contracts = %#v", actual.Contracts)
	}
}

func TestRunMatchesLockedSaveLifecycleOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "save-lifecycle-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "save-lifecycle-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "save-lifecycle-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked Django oracle for 12 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatalf("LoadObservationSuite(actual) error = %v", err)
	}
	if len(actual.Contracts) != 12 || actual.Contracts[0].ID != "MOD-008" || actual.Contracts[11].ID != "MOD-019" {
		t.Fatalf("actual save lifecycle contracts = %#v", actual.Contracts)
	}
}

func TestRunMatchesLockedQueryCacheOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "query-cache-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-cache-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked Django oracle for 11 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatalf("LoadObservationSuite(actual) error = %v", err)
	}
	if len(actual.Contracts) != 11 || actual.Contracts[0].ID != "QRY-011" || actual.Contracts[10].ID != "QRY-021" {
		t.Fatalf("actual query-cache contracts = %#v", actual.Contracts)
	}
}

func TestRunQueryCacheActualOutputIsDeterministic(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "query-cache-first.json")
	secondPath := filepath.Join(directory, "query-cache-second.json")
	baseArguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-cache-oracle.json"),
	}
	for _, outputPath := range []string{firstPath, secondPath} {
		arguments := append(append([]string(nil), baseArguments...), "-actual-output", outputPath)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%s) code = %d, stderr = %s", filepath.Base(outputPath), code, stderr.String())
		}
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("independent godjcheck query-cache actual outputs differ")
	}
}

func TestRunRejectsUnknownScenarioWithoutWritingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Contracts[0].Scenario = "django.query.cache.unknown_sentinel"
	contents, err := protocol.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "unknown-scenario-manifest.json")
	if err := os.WriteFile(manifestPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-cache-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `unsupported scenario "django.query.cache.unknown_sentinel"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunMatchesLockedMigrationPlanningOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-planning-manifest.json")
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "migration-planning-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-planning-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked Django oracle for 12 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 12 || actual.Contracts[0].ID != "MIG-005" || actual.Contracts[11].ID != "MIG-016" {
		t.Fatalf("actual migration-planning contracts = %#v", actual.Contracts)
	}
}

func TestRunMigrationPlanningActualOutputIsDeterministic(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "migration-planning-first.json")
	secondPath := filepath.Join(directory, "migration-planning-second.json")
	baseArguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "migration-planning-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-planning-oracle.json"),
	}
	for _, outputPath := range []string{firstPath, secondPath} {
		arguments := append(append([]string(nil), baseArguments...), "-actual-output", outputPath)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%s) code = %d, stderr = %s", filepath.Base(outputPath), code, stderr.String())
		}
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("independent godjcheck migration-planning actual outputs differ")
	}
}

func TestRunRejectsUnknownMigrationPlanningScenarioWithoutWritingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-planning-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Contracts[0].Scenario = "django.migration.plan.unknown_sentinel"
	contents, err := protocol.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "unknown-migration-planning-scenario-manifest.json")
	if err := os.WriteFile(manifestPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-planning-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `unsupported scenario "django.migration.plan.unknown_sentinel"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunMigrationExecutionMatchesReviewedProductExpectation(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-execution-manifest.json")
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "migration-execution-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-execution-oracle.json"),
		"-deviation-expected", filepath.Join(root, "conformance", "fixtures", "godj-migration-execution-deviation-expected.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the reviewed product expectation for 10 contracts under DEV-0001") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 10 || actual.Contracts[0].ID != "MIG-017" || actual.Contracts[9].ID != "MIG-026" {
		t.Fatalf("actual migration execution contracts = %#v", actual.Contracts)
	}
}

func TestRunRejectsUnknownMigrationExecutionScenarioWithoutWritingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-execution-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	unknownScenario := manifest.Contracts[0].Scenario + ".unknown_sentinel"
	manifest.Contracts[0].Scenario = unknownScenario
	contents, err := protocol.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "unknown-migration-execution-scenario-manifest.json")
	if err := os.WriteFile(manifestPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-execution-oracle.json"),
		"-deviation-expected", filepath.Join(root, "conformance", "fixtures", "godj-migration-execution-deviation-expected.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("unsupported scenario %q", unknownScenario)) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunMatchesLockedMigrationRestartOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "migration-restart-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "migration-restart-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-restart-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked Django oracle for 10 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 10 || actual.Contracts[0].ID != "MIG-027" || actual.Contracts[9].ID != "MIG-036" {
		t.Fatalf("actual migration restart contracts = %#v", actual.Contracts)
	}
}

func TestRunMigrationRestartActualOutputIsDeterministic(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "migration-restart-first.json")
	secondPath := filepath.Join(directory, "migration-restart-second.json")
	baseArguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "migration-restart-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-restart-oracle.json"),
	}
	for _, outputPath := range []string{firstPath, secondPath} {
		arguments := append(append([]string(nil), baseArguments...), "-actual-output", outputPath)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%s) code = %d, stderr = %s", filepath.Base(outputPath), code, stderr.String())
		}
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("independent godjcheck migration-restart actual outputs differ")
	}
}

func TestRunRejectsUnknownMigrationRestartScenarioWithoutWritingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-restart-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	unknownScenario := manifest.Contracts[0].Scenario + ".unknown_sentinel"
	manifest.Contracts[0].Scenario = unknownScenario
	contents, err := protocol.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "unknown-migration-restart-scenario-manifest.json")
	if err := os.WriteFile(manifestPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-restart-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("unsupported scenario %q", unknownScenario)) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunMatchesLockedMigrationStateReconstructionOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-state-reconstruction-manifest.json")
	actualPath := filepath.Join(t.TempDir(), "migration-state-reconstruction-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-state-reconstruction-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked Django oracle for 10 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 10 || actual.Contracts[0].ID != "MIG-037" || actual.Contracts[9].ID != "MIG-046" {
		t.Fatalf("actual migration-state-reconstruction contracts = %#v", actual.Contracts)
	}
}

func TestRunMigrationStateReconstructionActualOutputIsDeterministic(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "migration-state-reconstruction-first.json")
	secondPath := filepath.Join(directory, "migration-state-reconstruction-second.json")
	baseArguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "migration-state-reconstruction-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-state-reconstruction-oracle.json"),
	}
	for _, outputPath := range []string{firstPath, secondPath} {
		arguments := append(append([]string(nil), baseArguments...), "-actual-output", outputPath)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%s) code = %d, stderr = %s", filepath.Base(outputPath), code, stderr.String())
		}
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("independent godjcheck migration-state-reconstruction actual outputs differ")
	}
}

func TestRunRejectsUnknownMigrationStateReconstructionScenarioWithoutWritingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-state-reconstruction-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	unknownScenario := manifest.Contracts[0].Scenario + ".unknown_sentinel"
	manifest.Contracts[0].Scenario = unknownScenario
	contents, err := protocol.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "unknown-migration-state-reconstruction-scenario-manifest.json")
	if err := os.WriteFile(manifestPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-state-reconstruction-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("unsupported scenario %q", unknownScenario)) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunMatchesReviewedMigrationLifecycleExpectationAndWritesActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-lifecycle-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-lifecycle-oracle.json")
	deviationPath := filepath.Join(root, "conformance", "fixtures", "godj-migration-lifecycle-deviation-expected.json")
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != 10 {
		t.Fatalf("migration lifecycle manifest has %d contracts, want 10", len(manifest.Contracts))
	}
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "migration-lifecycle-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-deviation-expected", deviationPath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the reviewed product expectation for 10 contracts under DEV-0002") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := protocol.LoadObservationSuite(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	expectation, err := protocol.LoadDeviationExpectation(deviationPath)
	if err != nil {
		t.Fatal(err)
	}
	effective, product, err := protocol.PrepareDeviationExpectation(profile, manifest, oracle, expectation, migrationLifecycleDeviationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	differences, err := protocol.Compare(profile, effective, product, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 0 {
		t.Fatalf("migration lifecycle actual differs from reviewed product expectation: %#v", differences)
	}
}

func TestRunRejectsMigrationDefinitionSourceSetWithoutWritingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-definition-source-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-definition-source-oracle.json")
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != 8 || manifest.Contracts[0].ID != "MIG-057" || manifest.Contracts[7].ID != "MIG-064" {
		t.Fatalf("migration definition source contracts = %#v", manifest.Contracts)
	}
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("unsupported scenario %q", manifest.Contracts[0].Scenario)) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunRejectsMissingRequiredPaths(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: godjcheck") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRequiresDeviationExpectationBeforeActualGeneration(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest := loadApprovedMigrationExecutionManifestForMainTest(t, root)
	directory := t.TempDir()
	manifestPath := writeCanonicalMainTestArtifact(t, directory, "approved-manifest.json", manifest)
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-execution-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "manifest contains deviation contracts but -deviation-expected is missing") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunRejectsUnregisteredDeviationExpectationBeforeActualGeneration(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest := loadApprovedMigrationExecutionManifestForMainTest(t, root)
	expectation, err := protocol.LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-migration-execution-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	expectation.Contracts[0].Changes[0].Path = "steps[0].status"
	directory := t.TempDir()
	manifestPath := writeCanonicalMainTestArtifact(t, directory, "approved-manifest.json", manifest)
	expectationPath := writeCanonicalMainTestArtifact(t, directory, "invalid-deviation.json", expectation)
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-execution-oracle.json"),
		"-deviation-expected", expectationPath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "does not match policy") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestDeviationPolicyDispatchKeepsReviewedScopesSeparate(t *testing.T) {
	t.Parallel()

	execution, err := deviationPolicyForDecision("DEV-0001")
	if err != nil {
		t.Fatal(err)
	}
	if execution.Decision != "DEV-0001" || len(execution.Contracts) != 4 {
		t.Fatalf("DEV-0001 policy = %#v", execution)
	}
	lifecycle, err := deviationPolicyForDecision("DEV-0002")
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle.Decision != "DEV-0002" || len(lifecycle.Contracts) != 1 || lifecycle.Contracts[0].ID != "MIG-052" {
		t.Fatalf("DEV-0002 policy = %#v", lifecycle)
	}
	wantPaths := []string{"plan[0]", "plan[1]", "plan[2]", "steps[0]", "steps[1]", "steps[2]"}
	if len(lifecycle.Contracts[0].Changes) != len(wantPaths) {
		t.Fatalf("DEV-0002 change count = %d, want %d", len(lifecycle.Contracts[0].Changes), len(wantPaths))
	}
	for index, change := range lifecycle.Contracts[0].Changes {
		wantDimension := protocol.DeviationResult
		if index >= 3 {
			wantDimension = protocol.DeviationMetrics
		}
		if change.Dimension != wantDimension || change.Path != wantPaths[index] || change.Operation != protocol.DeviationReplace {
			t.Fatalf("DEV-0002 change %d = %#v", index, change)
		}
	}
	if _, err := deviationPolicyForDecision("DEV-9999"); err == nil || !strings.Contains(err.Error(), "unsupported deviation decision") {
		t.Fatalf("unknown decision error = %v", err)
	}
}

func TestRunRejectsUnknownDeviationDecisionBeforeActualGeneration(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	expectation, err := protocol.LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-migration-lifecycle-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	expectation.Decision = "DEV-9999"
	directory := t.TempDir()
	expectationPath := writeCanonicalMainTestArtifact(t, directory, "unknown-decision.json", expectation)
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "migration-lifecycle-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-lifecycle-oracle.json"),
		"-deviation-expected", expectationPath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `unsupported deviation decision "DEV-9999"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunValidatesLockedReferenceBeforeActualGeneration(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	oracle, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-execution-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle.Contracts[0].Phase = protocol.PhaseRollback
	directory := t.TempDir()
	oraclePath := writeCanonicalMainTestArtifact(t, directory, "wrong-phase-oracle.json", oracle)
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "migration-execution-manifest.json"),
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "locked Django oracle") || !strings.Contains(stderr.String(), "phase") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func loadApprovedMigrationExecutionManifestForMainTest(t *testing.T, root string) protocol.Manifest {
	t.Helper()
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-execution-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	deviations := map[string]bool{
		"MIG-018": true,
		"MIG-020": true,
		"MIG-022": true,
		"MIG-024": true,
	}
	for index := range manifest.Contracts {
		contract := &manifest.Contracts[index]
		provenance := make([]protocol.Provenance, 0, len(contract.Provenance))
		for _, entry := range contract.Provenance {
			if entry.Kind != "decision" {
				provenance = append(provenance, entry)
			}
		}
		contract.Provenance = provenance
		if !deviations[contract.ID] {
			contract.Status = protocol.ContractPassing
			continue
		}
		contract.Status = protocol.ContractDeviation
		derived := false
		contract.Provenance = append(contract.Provenance, protocol.Provenance{
			Kind:      "decision",
			Reference: "DEV-0001",
			Derived:   &derived,
		})
	}
	return manifest
}

func writeCanonicalMainTestArtifact(t *testing.T, directory, name string, value any) string {
	t.Helper()
	contents, err := protocol.MarshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
