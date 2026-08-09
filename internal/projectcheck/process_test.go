//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"golang.org/x/sys/unix"
)

func TestOwnedProcessHelper(t *testing.T) {
	mode := os.Getenv("GODJ_OWNED_PROCESS_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "emit":
		stdoutBytes, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_STDOUT_BYTES"))
		stderrBytes, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_STDERR_BYTES"))
		_, _ = io.CopyN(os.Stdout, strings.NewReader(strings.Repeat("x", stdoutBytes)), int64(stdoutBytes))
		_, _ = io.CopyN(os.Stderr, strings.NewReader(strings.Repeat("y", stderrBytes)), int64(stderrBytes))
		code, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_EXIT"))
		os.Exit(code)
	case "wire-stderr":
		_, _ = io.WriteString(os.Stdout, os.Getenv("GODJ_HELPER_WIRE"))
		stderrBytes, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_STDERR_BYTES"))
		_, _ = io.CopyN(os.Stderr, strings.NewReader(strings.Repeat("y", stderrBytes)), int64(stderrBytes))
		os.Exit(0)
	case "ignore":
		signal.Ignore(os.Interrupt)
		writeHelperReady()
		for {
			time.Sleep(time.Second)
		}
	case "spawn-holder":
		environment := environmentValues(os.Environ())
		environment["GODJ_OWNED_PROCESS_HELPER"] = "hold"
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedProcessHelper$")
		child.Env = sortedEnvironment(environment)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(97)
		}
		if ready := os.Getenv("GODJ_HELPER_READY"); ready != "" {
			payload := strconv.Itoa(os.Getpid()) + "," + strconv.Itoa(child.Process.Pid)
			_ = os.WriteFile(ready, []byte(payload), 0o600)
		}
		os.Exit(0)
	case "hold":
		signal.Ignore(os.Interrupt)
		for {
			time.Sleep(time.Second)
		}
	default:
		os.Exit(98)
	}
}

func TestOwnedProcessDrainsBeyondCapsBeforeNonzeroTransport(t *testing.T) {
	t.Parallel()
	command := helperCommand("emit", map[string]string{
		"GODJ_HELPER_STDOUT_BYTES": strconv.Itoa(protocol.MaxResponseBytes + 257),
		"GODJ_HELPER_STDERR_BYTES": strconv.Itoa(maxDiagnosticBytes + 257),
		"GODJ_HELPER_EXIT":         "7",
	})
	result := processBackend{}.Execute(context.Background(), nil, RunnerStage, command)
	if !result.Started || result.ExitCode != 7 || result.DirectReaps != 1 || result.StdoutScalar.RetainedBytes != protocol.MaxResponseBytes || !result.StdoutScalar.Truncated || result.StderrScalar.RetainedBytes != maxDiagnosticBytes || !result.StderrScalar.Truncated || len(result.Stdout) != protocol.MaxResponseBytes {
		t.Fatalf("oversized nonzero process = %+v stdout=%d", result, len(result.Stdout))
	}
}

func TestOwnedProcessAlreadyCanceledDoesNotStart(t *testing.T) {
	for _, test := range []struct {
		name      string
		context   func() context.Context
		interrupt func() <-chan struct{}
		wantCode  string
	}{
		{
			name: "context",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			interrupt: func() <-chan struct{} { return nil },
			wantCode:  protocol.CodeProjectCanceled,
		},
		{
			name:    "interrupt",
			context: context.Background,
			interrupt: func() <-chan struct{} {
				ready := make(chan struct{})
				close(ready)
				return ready
			},
			wantCode: protocol.CodeProjectInterrupted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "must-not-start")
			command := helperCommand("ignore", map[string]string{"GODJ_HELPER_READY": ready})
			result := processBackend{}.Execute(test.context(), test.interrupt(), RunnerStage, command)
			if result.Failure == nil || result.Failure.Code != test.wantCode || result.Started || result.DirectReaps != 0 || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 {
				t.Fatalf("pre-canceled process = %+v", result)
			}
			if _, err := os.Stat(ready); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pre-canceled helper started: %v", err)
			}
		})
	}
}

func TestQueuedDirectReapIsReconciledBeforeGroupSignals(t *testing.T) {
	waited := make(chan error, 1)
	want := errors.New("queued wait result")
	waited <- want
	got, complete := reconcileQueuedWait(waited, nil)
	if !complete || !errors.Is(got, want) {
		t.Fatalf("queued wait reconciliation = %v, %v", got, complete)
	}
}

func TestOwnedProcessCancellationSignalsGroupEscalatesAndReaps(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := helperCommand("ignore", map[string]string{"GODJ_HELPER_READY": ready})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan ProcessResult, 1)
	go func() { done <- processBackend{}.Execute(ctx, nil, RunnerStage, command) }()
	waitForFile(t, ready)
	started := time.Now()
	cancel()
	result := <-done
	if result.Failure == nil || result.Failure.Code != protocol.CodeProjectCanceled || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 1 || result.DirectReaps != 1 || !result.Started {
		t.Fatalf("canceled process = %+v", result)
	}
	if elapsed := time.Since(started); elapsed < ownedProcessGrace {
		t.Fatalf("escalation grace = %s, want >= %s", elapsed, ownedProcessGrace)
	}
}

func TestOwnedProcessCancelAfterDirectReapClosesDescendantHeldPipes(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "descendant")
	command := helperCommand("spawn-holder", map[string]string{"GODJ_HELPER_READY": ready})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan ProcessResult, 1)
	go func() { done <- processBackend{}.Execute(ctx, nil, RunnerStage, command) }()
	waitForFile(t, ready)
	descendantBytes, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(descendantBytes), ",")
	if len(parts) != 2 {
		t.Fatalf("invalid helper process pair %q", descendantBytes)
	}
	parentPID, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Kill(descendantPID, unix.SIGKILL) })
	waitForProcessExit(t, parentPID)
	cancel()
	select {
	case result := <-done:
		if result.Failure == nil || result.Failure.Code != protocol.CodeProjectCanceled || result.DirectReaps != 1 || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 {
			t.Fatalf("post-reap cancellation = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned process remained blocked on descendant-held pipes")
	}
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d was not reaped", pid)
}

func helperCommand(mode string, extra map[string]string) Command {
	environment := environmentValues(os.Environ())
	environment["GODJ_OWNED_PROCESS_HELPER"] = mode
	for key, value := range extra {
		environment[key] = value
	}
	return Command{Argv: []string{os.Args[0], "-test.run=^TestOwnedProcessHelper$"}, Env: sortedEnvironment(environment)}
}

func writeHelperReady() {
	if ready := os.Getenv("GODJ_HELPER_READY"); ready != "" {
		_ = os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
