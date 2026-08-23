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
	godjrunner "github.com/progresshans/godj/conformance/runners/godj"
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

func TestRunMatchesPinnedQueryBreadthOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	profilePath := filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json")
	manifestPath := filepath.Join(root, "conformance", "contracts", "query-breadth-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-breadth-oracle.json")
	actualPath := filepath.Join(t.TempDir(), "query-breadth-actual.json")
	arguments := []string{
		"-profile", profilePath,
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "GoDj observations match the locked reference oracle for 12 contracts\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	required, err := godjrunner.RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 12 || required[0] != "QRY-022" || required[11] != "QRY-033" {
		t.Fatalf("query-breadth required IDs = %#v", required)
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 12 || actual.Contracts[0].ID != "QRY-022" || actual.Contracts[11].ID != "QRY-033" {
		t.Fatalf("query-breadth actual contracts = %#v", actual.Contracts)
	}
	for _, observation := range actual.Contracts {
		if observation.Status != protocol.StatusObserved {
			t.Fatalf("query-breadth contract %s status = %q, want observed", observation.ID, observation.Status)
		}
	}
}

func TestRunMatchesPinnedQueryExpressionOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	profilePath := filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json")
	manifestPath := filepath.Join(root, "conformance", "contracts", "query-expression-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-expression-oracle.json")
	actualPath := filepath.Join(t.TempDir(), "query-expression-actual.json")

	profile, err := protocol.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != 20 || manifest.Contracts[0].ID != "QRY-034" || manifest.Contracts[19].ID != "QRY-053" {
		t.Fatalf("query-expression contracts = %#v", manifest.Contracts)
	}
	required, err := godjrunner.RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatalf("query-expression handler registry: %v", err)
	}
	if len(required) != 20 || required[0] != "QRY-034" || required[19] != "QRY-053" {
		t.Fatalf("query-expression required product handlers = %#v", required)
	}

	arguments := []string{
		"-profile", profilePath,
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "GoDj observations match the locked reference oracle for 20 contracts\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := protocol.LoadObservationSuite(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 20 {
		t.Fatalf("query-expression actual contract count = %d, want 20", len(actual.Contracts))
	}
	for index, observation := range actual.Contracts {
		contract := manifest.Contracts[index]
		if observation.ID != contract.ID || observation.Status != protocol.StatusObserved || observation.Phase != contract.Phase {
			t.Fatalf("actual contract %d = %#v, want %s observed/%s", index, observation, contract.ID, contract.Phase)
		}
		if observation.Result == nil || observation.Error != nil || observation.DBState == nil || observation.Metrics == nil {
			t.Fatalf("observed contract %s does not contain exact result/db_state/metrics payloads: %#v", observation.ID, observation)
		}
	}
	differences, err := protocol.Compare(profile, manifest, oracle, actual)
	if err != nil || len(differences) != 0 {
		t.Fatalf("query-expression actual comparison differences=%#v error=%v", differences, err)
	}
}

func TestRunRejectsUnregisteredQueryExpressionScenarioBeforePublishingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-expression-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Contracts = append([]protocol.Contract(nil), manifest.Contracts[:10]...)
	manifest.Contracts[0].Scenario = "django.query.expression.unregistered_sentinel"
	if _, err := godjrunner.RequiredObservedContractIDs(manifest); err == nil || !strings.Contains(err.Error(), `unregistered scenario "django.query.expression.unregistered_sentinel" contract QRY-034 has status "passing"; want oracle_locked`) {
		t.Fatalf("handler registry error = %v", err)
	}
	directory := t.TempDir()
	manifestPath := writeCanonicalMainTestArtifact(t, directory, "false-green-query-expression-manifest.json", manifest)
	oracle, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-expression-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle.Contracts = append([]protocol.Observation(nil), oracle.Contracts[:10]...)
	expectedPath := writeCanonicalMainTestArtifact(t, directory, "false-green-query-expression-oracle.json", oracle)
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", expectedPath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `actual handler registry: unregistered scenario "django.query.expression.unregistered_sentinel" contract QRY-034 has status "passing"; want oracle_locked`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
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
	if !strings.Contains(stderr.String(), `unregistered scenario "django.query.cache.unknown_sentinel"`) {
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
	if !strings.Contains(stderr.String(), `unregistered scenario "django.migration.plan.unknown_sentinel"`) {
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
	if !strings.Contains(stderr.String(), fmt.Sprintf("unregistered scenario %q", unknownScenario)) {
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
	if !strings.Contains(stderr.String(), fmt.Sprintf("unregistered scenario %q", unknownScenario)) {
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
	if !strings.Contains(stderr.String(), fmt.Sprintf("unregistered scenario %q", unknownScenario)) {
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

func TestRunAcceptsMigrationDefinitionSourceSetAndWritesMatchingActualOutput(t *testing.T) {
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
	actualPath := filepath.Join(directory, "actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked reference oracle for 8 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatalf("load actual output: %v", err)
	}
	expected, err := protocol.LoadObservationSuite(oraclePath)
	if err != nil {
		t.Fatalf("load expected output: %v", err)
	}
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	if differences, compareErr := protocol.Compare(profile, manifest, expected, actual); compareErr != nil || len(differences) != 0 {
		t.Fatalf("written migration definition actual differs: differences=%#v error=%v", differences, compareErr)
	}
}

func TestRunWritesMigrationProjectCheckActualThatMatchesLockedOracle(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-project-check-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-project-check-oracle.json")
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != 10 || manifest.Contracts[0].ID != "MIG-065" || manifest.Contracts[9].ID != "MIG-074" {
		t.Fatalf("migration project-check contracts = %#v", manifest.Contracts)
	}
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "match the locked reference oracle for 10 contracts") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatalf("load actual output: %v", err)
	}
	expected, err := protocol.LoadObservationSuite(oraclePath)
	if err != nil {
		t.Fatalf("load expected output: %v", err)
	}
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	if differences, compareErr := protocol.Compare(profile, manifest, expected, actual); compareErr != nil || len(differences) != 0 {
		t.Fatalf("written migration project-check actual differs: differences=%#v error=%v", differences, compareErr)
	}
}

func TestRunMatchesTwelveContractRelationProductBeforePublishingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "relation-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "relation-oracle.json")
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "relation-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "GoDj observations match the locked Django oracle for 12 contracts\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != 12 || manifest.Contracts[0].ID != "REL-001" || manifest.Contracts[11].ID != "REL-012" {
		t.Fatalf("relation contracts = %#v", manifest.Contracts)
	}
	oracle, err := protocol.LoadObservationSuite(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(oracle.Contracts) != len(manifest.Contracts) {
		t.Fatalf("relation oracle contracts = %d, want %d", len(oracle.Contracts), len(manifest.Contracts))
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range actual.Contracts {
		if actual.Contracts[index].Status != protocol.StatusObserved {
			t.Fatalf("required relation contract %s status = %q, want observed", actual.Contracts[index].ID, actual.Contracts[index].Status)
		}
	}
	differences, err := protocol.Compare(profile, manifest, oracle, actual)
	if err != nil || len(differences) != 0 {
		t.Fatalf("strict 12/12 relation comparison differences=%#v error=%v", differences, err)
	}
}

func TestRunLeavesMigrationRelationReferenceNotImplementedWithoutProductHandlers(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	profilePath := filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-relation-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-relation-oracle.json")
	staticPath := filepath.Join(root, "conformance", "fixtures", "godj-migration-relation-not-implemented.json")
	actualPath := filepath.Join(t.TempDir(), "migration-relation-actual.json")

	profile, err := protocol.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != 12 || manifest.Contracts[0].ID != "MIG-075" || manifest.Contracts[11].ID != "MIG-086" {
		t.Fatalf("migration-relation contracts = %#v", manifest.Contracts)
	}
	required, err := godjrunner.RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatalf("migration-relation handler registry: %v", err)
	}
	if len(required) != 0 {
		t.Fatalf("migration-relation required product handlers = %#v, want none", required)
	}

	arguments := []string{
		"-profile", profilePath,
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "GoDj product observations match 0 required contracts; 12 remain not implemented\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	actualBytes, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	static, err := protocol.LoadObservationSuite(staticPath)
	if err != nil {
		t.Fatal(err)
	}
	staticCanonical, err := protocol.MarshalCanonical(static)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualBytes, staticCanonical) {
		t.Fatal("zero-handler migration-relation output differs from the canonical checked-in not-implemented fixture")
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := protocol.LoadObservationSuite(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	for index, observation := range actual.Contracts {
		contract := manifest.Contracts[index]
		if observation.ID != contract.ID || observation.Status != protocol.StatusNotImplemented || observation.Phase != contract.Phase {
			t.Fatalf("actual contract %d = %#v, want %s not_implemented/%s", index, observation, contract.ID, contract.Phase)
		}
		if observation.Result != nil || observation.Error != nil || observation.DBState != nil || observation.Metrics != nil {
			t.Fatalf("not-implemented contract %s contains product payloads: %#v", observation.ID, observation)
		}
	}
	differences, err := protocol.CompareProduct(profile, manifest, oracle, actual, required)
	if err != nil || len(differences) != 0 {
		t.Fatalf("migration-relation zero-handler comparison differences=%#v error=%v", differences, err)
	}
}

func TestRunRejectsMigrationRelationFalseGreenBeforePublishingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-relation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Contracts[0].Status = protocol.ContractPassing
	if _, err := godjrunner.RequiredObservedContractIDs(manifest); err == nil || !strings.Contains(err.Error(), `unregistered scenario "godj.migration.relation.current_abi" contract MIG-075 has status "passing"; want oracle_locked`) {
		t.Fatalf("handler registry error = %v", err)
	}
	contents, err := protocol.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "false-green-migration-relation-manifest.json")
	if err := os.WriteFile(manifestPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-relation-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `actual handler registry: unregistered scenario "godj.migration.relation.current_abi" contract MIG-075 has status "passing"; want oracle_locked`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}

func TestRunDoesNotPublishRelationActualBeforePayloadComparison(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name     string
		index    int
		mutate   func(*protocol.Observation)
		contains string
	}{
		{name: "REL-001 result", mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-001 result.type:"},
		{name: "REL-002 error", index: 1, mutate: func(observation *protocol.Observation) {
			observation.Error.Code = "expected-replay-sentinel"
		}, contains: "REL-002 error.code:"},
		{name: "REL-002 database state", index: 1, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.DBState = &changed
		}, contains: "REL-002 db_state.type:"},
		{name: "REL-002 metrics", index: 1, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-002 metrics.fields[0].name:"},
		{name: "REL-003 result", index: 2, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-003 result.type:"},
		{name: "REL-003 database state", index: 2, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.DBState = &changed
		}, contains: "REL-003 db_state.type:"},
		{name: "REL-003 metrics", index: 2, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-003 metrics.fields[0].name:"},
		{name: "REL-004 result", index: 3, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-004 result.type:"},
		{name: "REL-004 database state", index: 3, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.DBState = &changed
		}, contains: "REL-004 db_state.type:"},
		{name: "REL-004 metrics", index: 3, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-004 metrics.fields[0].name:"},
		{name: "REL-005 result", index: 4, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-005 result.type:"},
		{name: "REL-005 database state", index: 4, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.DBState = &changed
		}, contains: "REL-005 db_state.type:"},
		{name: "REL-005 metrics", index: 4, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-005 metrics.fields[0].name:"},
		{name: "REL-006 result", index: 5, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-006 result.type:"},
		{name: "REL-006 database state", index: 5, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.DBState = &changed
		}, contains: "REL-006 db_state.type:"},
		{name: "REL-006 metrics", index: 5, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-006 metrics.fields[0].name:"},
		{name: "REL-007 error", index: 6, mutate: func(observation *protocol.Observation) {
			observation.Error.Code = "expected-replay-sentinel"
		}, contains: "REL-007 error.code:"},
		{name: "REL-007 database state", index: 6, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.DBState = &changed
		}, contains: "REL-007 db_state.type:"},
		{name: "REL-007 metrics", index: 6, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-007 metrics.fields[0].name:"},
		{name: "REL-008 result", index: 7, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-008 result.type:"},
		{name: "REL-008 database state", index: 7, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.DBState = &changed
		}, contains: "REL-008 db_state.type:"},
		{name: "REL-008 metrics", index: 7, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-008 metrics.fields[0].name:"},
		{name: "REL-009 result", index: 8, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-009 result.type:"},
		{name: "REL-009 metrics", index: 8, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-009 metrics.fields[0].name:"},
		{name: "REL-010 result", index: 9, mutate: func(observation *protocol.Observation) {
			changed := protocol.String("expected-replay-sentinel")
			observation.Result = &changed
		}, contains: "REL-010 result.type:"},
		{name: "REL-010 metrics", index: 9, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-010 metrics.fields[0].name:"},
		{name: "REL-011 error", index: 10, mutate: func(observation *protocol.Observation) {
			observation.Error.Code = "expected-replay-sentinel"
		}, contains: "REL-011 error.code:"},
		{name: "REL-011 metrics", index: 10, mutate: func(observation *protocol.Observation) {
			changed := protocol.Object(map[string]protocol.Value{
				"expected_replay_sentinel": protocol.String("changed"),
			})
			observation.Metrics = &changed
		}, contains: "REL-011 metrics.fields[0].name:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oracle, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "relation-oracle.json"))
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&oracle.Contracts[test.index])
			directory := t.TempDir()
			oraclePath := writeCanonicalMainTestArtifact(t, directory, "changed-oracle.json", oracle)
			actualPath := filepath.Join(directory, "must-not-exist.json")
			arguments := []string{
				"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
				"-manifest", filepath.Join(root, "conformance", "contracts", "relation-manifest.json"),
				"-expected", oraclePath,
				"-actual-output", actualPath,
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(context.Background(), arguments, &stdout, &stderr); code != 1 {
				t.Fatalf("run() code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.contains) {
				t.Fatalf("stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), test.contains)
			}
			if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
				t.Fatalf("actual output Stat() error = %v, want not-exist", err)
			}
		})
	}
}

func TestRunRejectsRelationRegistryStatusFalseGreensBeforeActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name     string
		index    int
		status   protocol.ContractStatus
		contains string
	}{
		{name: "registered handler hidden as locked", index: 0, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-002 handler hidden as locked", index: 1, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-003 handler hidden as locked", index: 2, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-004 handler hidden as locked", index: 3, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-005 handler hidden as locked", index: 4, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-006 handler hidden as locked", index: 5, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-007 handler hidden as locked", index: 6, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-008 handler hidden as locked", index: 7, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-009 handler hidden as locked", index: 8, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-010 handler hidden as locked", index: 9, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-011 handler hidden as locked", index: 10, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered REL-012 handler hidden as locked", index: 11, status: protocol.ContractOracleLocked, contains: "registered scenario"},
		{name: "registered handler marked red", index: 1, status: protocol.ContractRed, contains: "registered scenario"},
		{name: "draft contract", index: 1, status: protocol.ContractDraft, contains: "locked-or-later"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "relation-manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			manifest.Contracts[test.index].Status = test.status
			directory := t.TempDir()
			manifestPath := writeCanonicalMainTestArtifact(t, directory, "changed-manifest.json", manifest)
			actualPath := filepath.Join(directory, "must-not-exist.json")
			arguments := []string{
				"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
				"-manifest", manifestPath,
				"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "relation-oracle.json"),
				"-actual-output", actualPath,
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
				t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.contains) {
				t.Fatalf("stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), test.contains)
			}
			if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
				t.Fatalf("actual output Stat() error = %v, want not-exist", err)
			}
		})
	}
}

func TestRunRejectsUnknownRelationStatusBeforeActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	contents, err := os.ReadFile(filepath.Join(root, "conformance", "contracts", "relation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	contents = bytes.Replace(contents, []byte(`"status": "passing"`), []byte(`"status": "unknown"`), 1)
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "unknown-status-manifest.json")
	if err := os.WriteFile(manifestPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "relation-oracle.json"),
		"-actual-output", actualPath,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `unknown status "unknown"`) {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
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
	type reviewedContract struct {
		id    string
		paths []string
	}
	for _, test := range []struct {
		decision  string
		contracts []reviewedContract
	}{
		{decision: "DEV-0003", contracts: []reviewedContract{
			{id: "WEB-022", paths: []string{"attribute_fallback_shadowed", "object_dictionary_lookups"}},
			{id: "WEB-027", paths: []string{"auto_called", "rendered_return_category", "callable_invocations"}},
		}},
		{decision: "DEV-0004", contracts: []reviewedContract{
			{id: "AUT-004", paths: []string{"redirect"}},
			{id: "AUT-005", paths: []string{"delete.http_only", "login.expires_present", "login.max_age"}},
		}},
		{decision: "DEV-0005", contracts: []reviewedContract{
			{id: "ADM-002", paths: []string{"actions", "registered_models"}},
		}},
	} {
		policy, err := deviationPolicyForDecision(test.decision)
		if err != nil {
			t.Fatal(err)
		}
		if policy.Decision != test.decision || len(policy.Contracts) != len(test.contracts) {
			t.Fatalf("%s policy = %#v", test.decision, policy)
		}
		for contractIndex, contract := range test.contracts {
			got := policy.Contracts[contractIndex]
			if got.ID != contract.id || len(got.Changes) != len(contract.paths) {
				t.Fatalf("%s contract %d = %#v, want %s/%#v", test.decision, contractIndex, got, contract.id, contract.paths)
			}
			for changeIndex, change := range got.Changes {
				if change.Path != contract.paths[changeIndex] || change.Operation != protocol.DeviationReplace {
					t.Fatalf("%s contract %s change %d = %#v", test.decision, contract.id, changeIndex, change)
				}
			}
		}
	}
	if _, err := deviationPolicyForDecision("DEV-9999"); err == nil || !strings.Contains(err.Error(), "unsupported deviation decision") {
		t.Fatalf("unknown decision error = %v", err)
	}
}

func TestRunGDJ0043ReviewedProductExpectations(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name      string
		manifest  string
		oracle    string
		fixture   string
		decision  string
		contracts int
	}{
		{name: "template-form", manifest: "template-form-manifest.json", oracle: "template-form-oracle.json", fixture: "godj-template-form-deviation-expected.json", decision: "DEV-0003", contracts: 12},
		{name: "auth-session", manifest: "auth-session-manifest.json", oracle: "auth-session-oracle.json", fixture: "godj-auth-session-deviation-expected.json", decision: "DEV-0004", contracts: 8},
		{name: "article-admin", manifest: "article-admin-manifest.json", oracle: "article-admin-oracle.json", fixture: "godj-article-admin-deviation-expected.json", decision: "DEV-0005", contracts: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualPath := filepath.Join(t.TempDir(), test.name+"-actual.json")
			arguments := []string{
				"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
				"-manifest", filepath.Join(root, "conformance", "contracts", test.manifest),
				"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", test.oracle),
				"-deviation-expected", filepath.Join(root, "conformance", "fixtures", test.fixture),
				"-actual-output", actualPath,
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
				t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			want := fmt.Sprintf("match the reviewed product expectation for %d contracts under %s", test.contracts, test.decision)
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), want)
			}
			if _, err := protocol.LoadObservationSuite(actualPath); err != nil {
				t.Fatalf("load actual output: %v", err)
			}
		})
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
	if !strings.Contains(stderr.String(), "locked reference oracle") || !strings.Contains(stderr.String(), "phase") {
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
