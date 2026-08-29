package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestRunGDJ0049MigrationCommandWritesTwelveActuals(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "migration-command-actual.json")
	arguments := gdj0049MigrationCommandArguments(root, filepath.Join(root, "conformance", "contracts", "migration-command-manifest.json"), actualPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if code := run(ctx, arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "GoDj observations match the locked reference oracle for 12 contracts\n" || stderr.Len() != 0 {
		t.Fatalf("migration-command output = stdout:%q stderr:%q", stdout.String(), stderr.String())
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 12 || actual.Contracts[0].ID != "MIG-087" || actual.Contracts[11].ID != "MIG-098" {
		t.Fatalf("migration-command actual contracts = %#v", actual.Contracts)
	}
	for _, observation := range actual.Contracts {
		if observation.Status != protocol.StatusObserved {
			t.Fatalf("migration-command actual %s status = %q", observation.ID, observation.Status)
		}
	}
	contents, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"migration-command-definition-secret-canary",
		"migration-command-backend-secret-canary-4c2a",
		"migration-command synthetic ambiguous commit",
		"migration-command injected create failure",
	} {
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("migration-command product artifact contains private value %q", forbidden)
		}
	}
}

func TestRunGDJ0049MigrationCommandRejectsHiddenHandlerBeforeActualOutput(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-command-manifest.json")
	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Contracts[0].Status = protocol.ContractOracleLocked
	directory := t.TempDir()
	mutatedPath := writeCanonicalMainTestArtifact(t, directory, "hidden-migration-command.json", manifest)
	actualPath := filepath.Join(directory, "must-not-exist.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(ctx, gdj0049MigrationCommandArguments(root, mutatedPath, actualPath), &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "registered scenario") || strings.Contains(stderr.String(), context.Canceled.Error()) {
		t.Fatalf("hidden migration-command handler output = stdout:%q stderr:%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("hidden migration-command actual Stat() error = %v, want not-exist", err)
	}
}

func gdj0049MigrationCommandArguments(root, manifest, actual string) []string {
	return []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifest,
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-command-oracle.json"),
		"-actual-output", actual,
	}
}
