//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	"golang.org/x/sys/unix"
)

func TestMigrateOwnedProcessUsesProtocolResponseBound(t *testing.T) {
	policy, ok := ownedResponseProcessPolicyForStage(MigrateRunnerStage)
	if !ok || policy.stdoutMaximum != migrateprotocol.MaxResponseBytes ||
		policy.stderrMaximum != maxDiagnosticBytes || policy.grace != migrateOwnedProcessGrace {
		t.Fatalf("migrate process policy = (%+v, %t)", policy, ok)
	}

	const runtimeProbeLimit = 4 << 10
	command := helperCommand("emit", map[string]string{
		"GODJ_HELPER_STDOUT_BYTES": strconv.Itoa(runtimeProbeLimit + 1),
		"GODJ_HELPER_STDERR_BYTES": "0",
		"GODJ_HELPER_EXIT":         "0",
	})
	result := executeOwnedMigrateProcess(context.Background(), nil, command, runtimeProbeLimit, maxDiagnosticBytes, time.Second)
	if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.StdoutScalar.RetainedBytes != runtimeProbeLimit || !result.StdoutScalar.Truncated || len(result.Stdout) != runtimeProbeLimit {
		t.Fatalf("bounded migrate process = %+v stdout=%d", result, len(result.Stdout))
	}
}

func TestShowMigrationsOwnedProcessUsesProtocolResponseBound(t *testing.T) {
	command := helperCommand("emit", map[string]string{
		"GODJ_HELPER_STDOUT_BYTES": strconv.Itoa(showmigrationsprotocol.MaxResponseBytes + 1),
		"GODJ_HELPER_STDERR_BYTES": "0",
		"GODJ_HELPER_EXIT":         "0",
	})
	result := processBackend{}.Execute(context.Background(), nil, ShowMigrationsRunnerStage, command)
	if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.StdoutScalar.RetainedBytes != showmigrationsprotocol.MaxResponseBytes || !result.StdoutScalar.Truncated || len(result.Stdout) != showmigrationsprotocol.MaxResponseBytes {
		t.Fatalf("bounded showmigrations process = %+v stdout=%d", result, len(result.Stdout))
	}
}

func TestSQLMigrateOwnedProcessUsesExactProtocolResponseBound(t *testing.T) {
	command := helperCommand("emit", map[string]string{
		"GODJ_HELPER_STDOUT_BYTES": strconv.Itoa(sqlmigrateprotocol.MaxResponseBytes + 1),
		"GODJ_HELPER_STDERR_BYTES": "0",
		"GODJ_HELPER_EXIT":         "0",
	})
	result := processBackend{}.Execute(context.Background(), nil, SQLMigrateRunnerStage, command)
	if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 ||
		result.StdoutScalar.RetainedBytes != sqlmigrateprotocol.MaxResponseBytes ||
		!result.StdoutScalar.Truncated || len(result.Stdout) != sqlmigrateprotocol.MaxResponseBytes {
		t.Fatalf("bounded sqlmigrate process = %+v stdout=%d", result, len(result.Stdout))
	}
}

func TestMigrateOwnedProcessObservesCooperativeExitDuringGrace(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := helperCommand("graceful", map[string]string{
		"GODJ_HELPER_READY":      ready,
		"GODJ_HELPER_EXIT_DELAY": "40ms",
	})
	interrupt := make(chan struct{})
	done := make(chan ProcessResult, 1)
	grace := 5 * time.Second
	go func() {
		done <- executeOwnedMigrateProcess(context.Background(), interrupt, command, maxResponseBytes, maxDiagnosticBytes, grace)
	}()
	waitForFile(t, ready)
	started := time.Now()
	close(interrupt)
	result := <-done
	if result.Failure == nil || result.Failure.Code != protocol.CodeProjectInterrupted || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 0 || result.DirectReaps != 1 || !result.Started || result.ExitCode != 0 {
		t.Fatalf("cooperative migrate process = %+v", result)
	}
	if elapsed := time.Since(started); elapsed >= grace {
		t.Fatalf("cooperative exit waited through grace: %s", elapsed)
	}
}

func TestMigrateOwnedProcessEscalatesOnlyAfterGraceAndReaps(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := helperCommand("ignore", map[string]string{"GODJ_HELPER_READY": ready})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan ProcessResult, 1)
	grace := 75 * time.Millisecond
	go func() {
		done <- executeOwnedMigrateProcess(ctx, nil, command, maxResponseBytes, maxDiagnosticBytes, grace)
	}()
	waitForFile(t, ready)
	started := time.Now()
	cancel()
	result := <-done
	if result.Failure == nil || result.Failure.Code != protocol.CodeProjectCanceled || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 1 || result.DirectReaps != 1 || !result.Started {
		t.Fatalf("forced migrate process = %+v", result)
	}
	if elapsed := time.Since(started); elapsed < grace {
		t.Fatalf("forced exit skipped grace: %s, want >= %s", elapsed, grace)
	}
}

func TestMigrateOwnedProcessAlreadyCanceledDoesNotStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := processBackend{}.Execute(ctx, nil, MigrateRunnerStage, helperCommand("ignore", nil))
	if result.Failure == nil || result.Failure.Code != protocol.CodeProjectCanceled || result.Started || result.DirectReaps != 0 || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 {
		t.Fatalf("pre-canceled migrate process = %+v", result)
	}
}

func TestMigrateOwnedProcessCompletedResponsePrecedesConcurrentCancellation(t *testing.T) {
	logical := migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: "commit_outcome_unknown"}
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{Failure: logical})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		context   func() (context.Context, func())
		interrupt func() (<-chan struct{}, func())
	}{
		{
			name: "context",
			context: func() (context.Context, func()) {
				ctx, cancel := context.WithCancel(context.Background())
				return ctx, cancel
			},
			interrupt: func() (<-chan struct{}, func()) { return nil, func() {} },
		},
		{
			name:    "interrupt",
			context: func() (context.Context, func()) { return context.Background(), func() {} },
			interrupt: func() (<-chan struct{}, func()) {
				ready := make(chan struct{})
				return ready, func() { close(ready) }
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.context()
			defer cancel()
			interrupt, triggerInterrupt := test.interrupt()
			terminalReady := make(chan struct{})
			releaseTerminal := make(chan struct{})
			command := helperCommand("wire-stderr", map[string]string{
				"GODJ_HELPER_WIRE":         string(wire),
				"GODJ_HELPER_STDERR_BYTES": "0",
			})
			done := make(chan ProcessResult, 1)
			go func() {
				done <- executeOwnedMigrateProcessWithHooks(
					ctx,
					interrupt,
					command,
					migrateprotocol.MaxResponseBytes,
					maxDiagnosticBytes,
					time.Second,
					migrateOwnedProcessHooks{beforeTerminalReturn: func() {
						close(terminalReady)
						<-releaseTerminal
					}},
				)
			}()

			select {
			case <-terminalReady:
			case <-time.After(5 * time.Second):
				t.Fatal("migrate process did not reach completed terminal boundary")
			}
			cancel()
			triggerInterrupt()
			close(releaseTerminal)

			select {
			case result := <-done:
				if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.Failure != nil || result.CleanupFailed || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 || !bytes.Equal(result.Stdout, wire) || result.StdoutScalar != (StreamScalar{RetainedBytes: len(wire)}) || result.StderrScalar != (StreamScalar{}) {
					t.Fatalf("completed migrate response = %+v stdout=%q", result, result.Stdout)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("completed migrate response remained blocked after terminal release")
			}
		})
	}
}

func TestMigrateOwnedProcessBoundsDescendantHeldPipesAfterDirectExit(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "descendant")
	holderReady := filepath.Join(t.TempDir(), "holder-ready")
	command := helperCommand("spawn-holder", map[string]string{
		"GODJ_HELPER_READY":        ready,
		"GODJ_HELPER_HOLDER_READY": holderReady,
	})
	done := make(chan ProcessResult, 1)
	grace := 75 * time.Millisecond
	go func() {
		done <- executeOwnedMigrateProcess(context.Background(), nil, command, maxResponseBytes, maxDiagnosticBytes, grace)
	}()
	groupPID, descendantPID := migrateHelperProcessPair(t, ready)
	waitForFile(t, holderReady)
	t.Cleanup(func() {
		if runserverProcessGroupExists(groupPID) {
			_ = unix.Kill(-groupPID, unix.SIGKILL)
		}
		_ = unix.Kill(descendantPID, unix.SIGKILL)
	})

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.Failure != nil || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 1 || result.CleanupFailed {
			t.Fatalf("descendant-held migrate pipes = %+v", result)
		}
		if runserverProcessGroupExists(groupPID) {
			t.Fatalf("migrate process group %d remains after bounded cleanup", groupPID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("descendant-held migrate pipes exceeded bounded cleanup")
	}
}

func TestShowMigrationsOwnedProcessBoundsDescendantHeldPipesAfterDirectExit(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "descendant")
	holderReady := filepath.Join(t.TempDir(), "holder-ready")
	command := helperCommand("spawn-holder", map[string]string{
		"GODJ_HELPER_READY":        ready,
		"GODJ_HELPER_HOLDER_READY": holderReady,
	})
	done := make(chan ProcessResult, 1)
	started := time.Now()
	go func() {
		done <- processBackend{}.Execute(context.Background(), nil, ShowMigrationsRunnerStage, command)
	}()
	groupPID, descendantPID := migrateHelperProcessPair(t, ready)
	waitForFile(t, holderReady)
	t.Cleanup(func() {
		if runserverProcessGroupExists(groupPID) {
			_ = unix.Kill(-groupPID, unix.SIGKILL)
		}
		_ = unix.Kill(descendantPID, unix.SIGKILL)
	})

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.Failure != nil || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 1 || result.CleanupFailed {
			t.Fatalf("descendant-held showmigrations pipes = %+v", result)
		}
		if elapsed := time.Since(started); elapsed < ownedProcessGrace || elapsed > 5*time.Second {
			t.Fatalf("showmigrations descendant cleanup duration = %s", elapsed)
		}
		if runserverProcessGroupExists(groupPID) {
			t.Fatalf("showmigrations process group %d remains after bounded cleanup", groupPID)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("descendant-held showmigrations pipes exceeded bounded cleanup")
	}
}

func TestMigrateOwnedProcessCancellationAfterDirectExitCleansDescendantGroup(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "descendant")
	holderReady := filepath.Join(t.TempDir(), "holder-ready")
	command := helperCommand("spawn-holder", map[string]string{
		"GODJ_HELPER_READY":        ready,
		"GODJ_HELPER_HOLDER_READY": holderReady,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan ProcessResult, 1)
	grace := 75 * time.Millisecond
	go func() {
		done <- executeOwnedMigrateProcess(ctx, nil, command, maxResponseBytes, maxDiagnosticBytes, grace)
	}()
	groupPID, descendantPID := migrateHelperProcessPair(t, ready)
	waitForFile(t, holderReady)
	t.Cleanup(func() {
		if runserverProcessGroupExists(groupPID) {
			_ = unix.Kill(-groupPID, unix.SIGKILL)
		}
		_ = unix.Kill(descendantPID, unix.SIGKILL)
	})
	waitForProcessExit(t, groupPID)
	cancel()

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.Failure == nil || result.Failure.Code != protocol.CodeProjectCanceled || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 1 || result.CleanupFailed {
			t.Fatalf("post-exit migrate cancellation = %+v", result)
		}
		if runserverProcessGroupExists(groupPID) {
			t.Fatalf("migrate process group %d remains after cancellation cleanup", groupPID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("post-exit migrate cancellation left descendant-held pipes blocked")
	}
}

func TestMigrateOwnedProcessBoundsQuietSameGroupDescendant(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "quiet-descendant")
	holderReady := filepath.Join(t.TempDir(), "quiet-holder-ready")
	command := helperCommand("spawn-quiet-holder", map[string]string{
		"GODJ_HELPER_READY":        ready,
		"GODJ_HELPER_HOLDER_READY": holderReady,
	})
	done := make(chan ProcessResult, 1)
	grace := 75 * time.Millisecond
	started := time.Now()
	go func() {
		done <- executeOwnedMigrateProcess(context.Background(), nil, command, maxResponseBytes, maxDiagnosticBytes, grace)
	}()
	groupPID, descendantPID := migrateHelperProcessPair(t, ready)
	waitForFile(t, holderReady)
	t.Cleanup(func() {
		if runserverProcessGroupExists(groupPID) {
			_ = unix.Kill(-groupPID, unix.SIGKILL)
		}
		_ = unix.Kill(descendantPID, unix.SIGKILL)
	})

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.Failure != nil || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 1 || result.CleanupFailed {
			t.Fatalf("quiet descendant migrate process = %+v", result)
		}
		if elapsed := time.Since(started); elapsed < grace || elapsed > 2*time.Second {
			t.Fatalf("quiet descendant cleanup duration = %s", elapsed)
		}
		if runserverProcessGroupExists(groupPID) {
			t.Fatalf("quiet migrate process group %d remains after bounded cleanup", groupPID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("quiet migrate descendant exceeded bounded cleanup")
	}
}

func migrateHelperProcessPair(t *testing.T, ready string) (int, int) {
	t.Helper()
	waitForFile(t, ready)
	payload, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(payload), ",")
	if len(parts) != 2 {
		t.Fatalf("invalid migrate helper process pair %q", payload)
	}
	groupPID, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return groupPID, descendantPID
}
