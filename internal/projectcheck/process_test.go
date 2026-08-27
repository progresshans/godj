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
	case "generation-wire":
		wire := os.Getenv("GODJ_HELPER_WIRE")
		stdoutBytes, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_STDOUT_BYTES"))
		_, _ = io.WriteString(os.Stdout, wire)
		remaining := stdoutBytes - len(wire)
		if remaining > 0 {
			_, _ = io.CopyN(os.Stdout, strings.NewReader(strings.Repeat(" ", remaining)), int64(remaining))
		}
		os.Exit(0)
	case "ignore":
		signal.Ignore(os.Interrupt)
		if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(96)
		}
		for {
			time.Sleep(time.Second)
		}
	case "graceful":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		defer signal.Stop(signals)
		if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(96)
		}
		<-signals
		delay, _ := time.ParseDuration(os.Getenv("GODJ_HELPER_EXIT_DELAY"))
		if delay > 0 {
			time.Sleep(delay)
		}
		os.Exit(0)
	case "spawn-holder":
		environment := environmentValues(os.Environ())
		environment["GODJ_OWNED_PROCESS_HELPER"] = "hold"
		if holderReady := os.Getenv("GODJ_HELPER_HOLDER_READY"); holderReady != "" {
			environment["GODJ_HELPER_READY"] = holderReady
			environment["GODJ_HELPER_HOLDER_HANDSHAKE"] = "1"
		}
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedProcessHelper$")
		child.Env = sortedEnvironment(environment)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(97)
		}
		if ready := os.Getenv("GODJ_HELPER_READY"); ready != "" {
			payload := strconv.Itoa(os.Getpid()) + "," + strconv.Itoa(child.Process.Pid)
			if err := publishHelperReady(payload); err != nil {
				os.Exit(96)
			}
		}
		os.Exit(0)
	case "spawn-quiet-holder":
		environment := environmentValues(os.Environ())
		environment["GODJ_OWNED_PROCESS_HELPER"] = "hold"
		if holderReady := os.Getenv("GODJ_HELPER_HOLDER_READY"); holderReady != "" {
			environment["GODJ_HELPER_READY"] = holderReady
			environment["GODJ_HELPER_HOLDER_HANDSHAKE"] = "1"
		}
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedProcessHelper$")
		child.Env = sortedEnvironment(environment)
		if err := child.Start(); err != nil {
			os.Exit(97)
		}
		if ready := os.Getenv("GODJ_HELPER_READY"); ready != "" {
			payload := strconv.Itoa(os.Getpid()) + "," + strconv.Itoa(child.Process.Pid)
			if err := publishHelperReady(payload); err != nil {
				os.Exit(96)
			}
		}
		os.Exit(0)
	case "hold":
		signal.Ignore(os.Interrupt)
		if os.Getenv("GODJ_HELPER_HOLDER_HANDSHAKE") == "1" {
			if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
				os.Exit(96)
			}
		}
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

func TestAlreadyReapedDirectChildWaitPublicationIsReconciled(t *testing.T) {
	command := helperCommand("emit", nil)
	child := exec.Command(command.Argv[0], command.Argv[1:]...)
	child.Env = command.Env
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}

	type reconciliation struct {
		err      error
		complete bool
	}
	waited := make(chan error)
	reconciled := make(chan reconciliation, 1)
	want := errors.New("delayed wait publication")
	go func() {
		waitErr, complete := reconcileOwnedProcessWait(child.Process, waited, nil)
		reconciled <- reconciliation{err: waitErr, complete: complete}
	}()
	select {
	case waited <- want:
	case got := <-reconciled:
		t.Fatalf("already-reaped reconciliation returned before publication: %+v", got)
	case <-time.After(5 * time.Second):
		t.Fatal("already-reaped reconciliation did not await publication")
	}
	select {
	case got := <-reconciled:
		if !got.complete || !errors.Is(got.err, want) {
			t.Fatalf("already-reaped reconciliation = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("already-reaped reconciliation remained blocked after publication")
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

func publishHelperReady(payload string) error {
	ready := os.Getenv("GODJ_HELPER_READY")
	if ready == "" {
		return nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(ready), "."+filepath.Base(ready)+".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.WriteString(temporary, payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, ready)
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
