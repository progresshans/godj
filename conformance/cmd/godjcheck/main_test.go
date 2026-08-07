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
