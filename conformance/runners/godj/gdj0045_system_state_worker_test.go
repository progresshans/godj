//go:build darwin || linux

package godj

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSystemStateWorkerBuildCancellationPreservesCauseAndCleanup(t *testing.T) {
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// Replace only this test process's Go command. An exec'd sleeper owns no
	// descendants and produces no diagnostics, as in the failed Hosted build.
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nprintf started > \"$GODJ_WORKER_BUILD_PROBE\"\nexec /bin/sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(directory, "started")
	t.Setenv("PATH", bin)
	t.Setenv("TMPDIR", directory)
	t.Setenv("GODJ_WORKER_BUILD_PROBE", probe)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error, 1)
	go func() {
		defer cancel()
		limit := time.NewTimer(10 * time.Second)
		defer limit.Stop()
		poll := time.NewTicker(10 * time.Millisecond)
		defer poll.Stop()
		for {
			if _, err := os.Stat(probe); err == nil {
				started <- nil
				return
			} else if !errors.Is(err, os.ErrNotExist) {
				started <- err
				return
			}
			select {
			case <-ctx.Done():
				started <- ctx.Err()
				return
			case <-limit.C:
				started <- errors.New("controlled compiler did not start")
				return
			case <-poll.C:
			}
		}
	}()
	harness, err := newSystemStateWorkerHarness(ctx)
	cancel()
	if startErr := <-started; startErr != nil {
		t.Fatalf("compiler start: %v; build: %v", startErr, err)
	}
	if harness != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "Go build was canceled") {
		t.Fatalf("canceled build published a harness or hid cancellation: harness=%v error=%v", harness, err)
	}
	if strings.Contains(err.Error(), directory) {
		t.Fatal("build error exposed the private workspace")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "godj-system-state-worker-") {
			t.Fatalf("failed build retained its worker directory: %s", entry.Name())
		}
	}
}
