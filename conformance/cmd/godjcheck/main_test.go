package main

import (
	"bytes"
	"context"
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

func TestRunRejectsOracleLockedMigrationPlanningWithoutWritingActualOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-planning-manifest.json")
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) == 0 {
		t.Fatal("migration-planning manifest has no contracts")
	}
	directory := t.TempDir()
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
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unsupported scenario "`+manifest.Contracts[0].Scenario+`"`) {
		t.Fatalf("stderr = %q", stderr.String())
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
