//go:build darwin || linux

package multiruntimeworker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteDistinctProcessCoordinationAndCleanReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	repository := repositoryRoot(t)
	executable := filepath.Join(t.TempDir(), "system-state-multiruntime-worker")
	build := exec.CommandContext(ctx, "go", "build", "-o", executable, "./conformance/systemstate/multiruntimeworker/cmd")
	build.Dir = repository
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build multi-runtime worker: %v; output length=%d", err, len(output))
	}

	database, err := NewSQLiteDatabase(filepath.Join(t.TempDir(), "private-database-marker.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := RunScenario(ctx, executable, database)
	if err != nil {
		t.Fatalf("RunScenario(SQLite): %v", err)
	}
	if facts.WriterProcesses != 2 || !facts.SameSchema || !facts.BarrierLinearized ||
		facts.HolderCallbackInvocations != 1 || facts.ContenderCallbackInvocations != 1 ||
		!facts.RestartPreserved || facts.DurableEvents != 2 || facts.Divergence || facts.Loss ||
		facts.Drift || facts.SecretOccurrences != 0 {
		t.Fatalf("SQLite distinct-process facts = %+v", facts)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("cannot locate repository root")
		}
		directory = parent
	}
}
