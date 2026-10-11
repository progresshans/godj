//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRunserverProcessHelper(t *testing.T) {
	mode := os.Getenv("GODJ_RUNSERVER_PROCESS_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "emit":
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(91)
		}
		_, _ = fmt.Fprintln(os.Stdout, cwd)
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("GODJ_RUNSERVER_TEST_VALUE"))
		_, _ = io.WriteString(os.Stderr, "runtime-stderr\n")
		exitCode, _ := strconv.Atoi(os.Getenv("GODJ_RUNSERVER_TEST_EXIT"))
		os.Exit(exitCode)
	case "graceful":
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		if err := publishRunserverProcessReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(92)
		}
		<-interrupt
		_, _ = io.WriteString(os.Stdout, "runtime-stopped\n")
		os.Exit(0)
	case "stream-failure":
		signal.Ignore(os.Interrupt)
		if err := publishRunserverProcessReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(92)
		}
		_, _ = io.WriteString(os.Stdout, "runtime-ready\n")
		for {
			time.Sleep(time.Hour)
		}
	case "ignore":
		signal.Ignore(os.Interrupt)
		if err := publishRunserverProcessReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(92)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "spawn-holder":
		environment := environmentValues(os.Environ())
		environment["GODJ_RUNSERVER_PROCESS_HELPER"] = "hold"
		holder := exec.Command(os.Args[0], "-test.run=^TestRunserverProcessHelper$")
		holder.Env = sortedEnvironment(environment)
		holder.Stdout = os.Stdout
		holder.Stderr = os.Stderr
		if err := holder.Start(); err != nil {
			os.Exit(93)
		}
		if err := publishRunserverProcessReady(strconv.Itoa(os.Getpid()) + "," + strconv.Itoa(holder.Process.Pid)); err != nil {
			_ = holder.Process.Kill()
			os.Exit(92)
		}
		os.Exit(9)
	case "spawn-quiet-holder":
		environment := environmentValues(os.Environ())
		environment["GODJ_RUNSERVER_PROCESS_HELPER"] = "hold"
		holder := exec.Command(os.Args[0], "-test.run=^TestRunserverProcessHelper$")
		holder.Env = sortedEnvironment(environment)
		if err := holder.Start(); err != nil {
			os.Exit(93)
		}
		if err := publishRunserverProcessReady(strconv.Itoa(os.Getpid()) + "," + strconv.Itoa(holder.Process.Pid)); err != nil {
			_ = holder.Process.Kill()
			os.Exit(92)
		}
		os.Exit(8)
	case "hold":
		signal.Ignore(os.Interrupt)
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(94)
	}
}

func TestRunserverProcessStreamsProjectRuntimeAndReturnsCleanExit(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	command := runserverProcessHelperCommand("emit", map[string]string{
		"GODJ_RUNSERVER_TEST_EXIT":  "0",
		"GODJ_RUNSERVER_TEST_VALUE": "runserver-value",
	})
	command.Dir = projectRoot
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	result := executeRunserverProcess(context.Background(), nil, command, &stdout, &stderr, 500*time.Millisecond)
	resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantStdout := resolvedRoot + "\nrunserver-value\n"
	if result.StartError != nil || result.WaitError != nil || result.StdoutError != nil || result.StderrError != nil || result.CleanupFailed {
		t.Fatalf("clean process errors = %+v", result)
	}
	if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 || result.Interrupted || result.Canceled || result.Forced {
		t.Fatalf("clean process = %+v", result)
	}
	if stdout.String() != wantStdout || stderr.String() != "runtime-stderr\n" {
		t.Fatalf("streamed output = %q / %q, want %q / %q", stdout.String(), stderr.String(), wantStdout, "runtime-stderr\n")
	}
}

func TestRunserverProcessPreservesUnexpectedExitForCaller(t *testing.T) {
	t.Parallel()
	command := runserverProcessHelperCommand("emit", map[string]string{"GODJ_RUNSERVER_TEST_EXIT": "7"})
	result := executeRunserverProcess(context.Background(), nil, command, io.Discard, io.Discard, 500*time.Millisecond)
	var exitError *exec.ExitError
	if !result.Started || result.ExitCode != 7 || result.DirectReaps != 1 || !errors.As(result.WaitError, &exitError) || result.CleanupFailed || result.Interrupted || result.Canceled || result.Forced || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 {
		t.Fatalf("unexpected exit = %+v", result)
	}
}

func TestRunserverProcessPreStoppedDoesNotStart(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		ctx       func() context.Context
		interrupt func() <-chan struct{}
		wantStop  runserverProcessStop
	}{
		{
			name: "interrupt",
			ctx:  context.Background,
			interrupt: func() <-chan struct{} {
				stopped := make(chan struct{})
				close(stopped)
				return stopped
			},
			wantStop: runserverProcessInterrupted,
		},
		{
			name: "context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			interrupt: func() <-chan struct{} { return nil },
			wantStop:  runserverProcessCanceled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := executeRunserverProcess(test.ctx(), test.interrupt(), runserverProcessHelperCommand("emit", nil), io.Discard, io.Discard, 100*time.Millisecond)
			if result.Started || result.DirectReaps != 0 || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 || result.StartError != nil {
				t.Fatalf("pre-stopped process = %+v", result)
			}
			if test.wantStop == runserverProcessInterrupted && (!result.Interrupted || result.Canceled) {
				t.Fatalf("pre-interrupted process = %+v", result)
			}
			if test.wantStop == runserverProcessCanceled && (!result.Canceled || result.Interrupted) {
				t.Fatalf("pre-canceled process = %+v", result)
			}
		})
	}
}

func TestRunserverProcessInterruptAllowsCleanRuntimeDrain(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := runserverProcessHelperCommand("graceful", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
	interrupt := make(chan struct{})
	var stdout bytes.Buffer
	done := make(chan runserverProcessResult, 1)
	go func() {
		done <- executeRunserverProcess(context.Background(), interrupt, command, &stdout, io.Discard, 3*time.Second)
	}()
	waitForRunserverProcessReady(t, ready)
	close(interrupt)

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || !result.Interrupted || result.Canceled || result.Forced || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 0 || result.CleanupFailed || result.WaitError != nil {
			t.Fatalf("clean interrupt = %+v", result)
		}
		if stdout.String() != "runtime-stopped\n" {
			t.Fatalf("graceful runtime output = %q", stdout.String())
		}
	case <-time.After(6 * time.Second):
		t.Fatal("clean interrupted runtime did not return")
	}
}

func TestRunserverProcessCancellationStopsGroupGracefully(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := runserverProcessHelperCommand("graceful", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan runserverProcessResult, 1)
	go func() {
		done <- executeRunserverProcess(ctx, nil, command, io.Discard, io.Discard, 3*time.Second)
	}()
	waitForRunserverProcessReady(t, ready)
	cancel()

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.Interrupted || !result.Canceled || result.Forced || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 0 || result.CleanupFailed || result.WaitError != nil {
			t.Fatalf("clean cancellation = %+v", result)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("canceled runtime did not return")
	}
}

func TestRunserverProcessInterruptTimeoutKillsGroupOnce(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := runserverProcessHelperCommand("ignore", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
	interrupt := make(chan struct{})
	done := make(chan runserverProcessResult, 1)
	go func() {
		done <- executeRunserverProcess(context.Background(), interrupt, command, io.Discard, io.Discard, 50*time.Millisecond)
	}()
	pid := waitForRunserverProcessReady(t, ready)
	t.Cleanup(func() { _ = unix.Kill(pid, unix.SIGKILL) })
	started := time.Now()
	close(interrupt)

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != -1 || result.DirectReaps != 1 || !result.Interrupted || result.Canceled || !result.Forced || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 1 || result.CleanupFailed {
			t.Fatalf("forced interrupt = %+v", result)
		}
		if elapsed := time.Since(started); elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
			t.Fatalf("forced interrupt duration = %s", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forced interrupted runtime did not return")
	}
}

func TestRunserverProcessBoundsDescendantHeldOutputPipes(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "holder")
	command := runserverProcessHelperCommand("spawn-holder", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
	done := make(chan runserverProcessResult, 1)
	go func() {
		done <- executeRunserverProcess(context.Background(), nil, command, io.Discard, io.Discard, 50*time.Millisecond)
	}()
	groupPID, holderPID := waitForRunserverProcessPair(t, ready)
	t.Cleanup(func() {
		if runserverProcessGroupExists(groupPID) {
			_ = unix.Kill(-groupPID, unix.SIGKILL)
		}
		_ = unix.Kill(holderPID, unix.SIGKILL)
	})

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 9 || result.DirectReaps != 1 || result.Interrupted || result.Canceled || !result.Forced || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 1 || result.CleanupFailed {
			t.Fatalf("descendant-held output = %+v", result)
		}
		assertRunserverProcessGroupAbsent(t, groupPID)
	case <-time.After(3 * time.Second):
		t.Fatal("descendant-held output kept runserver process blocked")
	}
}

func TestRunserverProcessKillsSameGroupDescendantAfterClosedOutput(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "quiet-holder")
	command := runserverProcessHelperCommand("spawn-quiet-holder", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
	done := make(chan runserverProcessResult, 1)
	started := time.Now()
	go func() {
		done <- executeRunserverProcess(context.Background(), nil, command, io.Discard, io.Discard, 50*time.Millisecond)
	}()
	groupPID, holderPID := waitForRunserverProcessPair(t, ready)
	t.Cleanup(func() {
		if runserverProcessGroupExists(groupPID) {
			_ = unix.Kill(-groupPID, unix.SIGKILL)
		}
		_ = unix.Kill(holderPID, unix.SIGKILL)
	})

	select {
	case result := <-done:
		if !result.Started || result.ExitCode != 8 || result.DirectReaps != 1 || result.Interrupted || result.Canceled || !result.Forced || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 1 || result.CleanupFailed {
			t.Fatalf("quiet same-group descendant = %+v", result)
		}
		if elapsed := time.Since(started); elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
			t.Fatalf("quiet descendant cleanup duration = %s", elapsed)
		}
		assertRunserverProcessGroupAbsent(t, groupPID)
	case <-time.After(3 * time.Second):
		t.Fatal("quiet same-group descendant outlived bounded cleanup")
	}
}

func TestRunserverProcessRecordsWriterFailureWhileContinuingDrain(t *testing.T) {
	t.Parallel()
	want := errors.New("stream delivery failed")
	command := runserverProcessHelperCommand("emit", map[string]string{"GODJ_RUNSERVER_TEST_EXIT": "0"})
	result := executeRunserverProcess(context.Background(), nil, command, runserverErrorWriter{err: want}, io.Discard, 500*time.Millisecond)
	if !result.Started || result.DirectReaps != 1 || !errors.Is(result.StdoutError, want) || result.CleanupFailed || result.Forced || result.SIGINTAttempts > 1 || result.SIGKILLAttempts != 0 {
		t.Fatalf("writer failure = %+v", result)
	}
	if result.ExitCode != 0 && result.ExitCode != -1 {
		t.Fatalf("writer failure exit = %+v", result)
	}
}

func TestRunserverProcessWriterFailureStopsLongLivedRuntime(t *testing.T) {
	t.Parallel()
	want := errors.New("stream delivery failed")
	ready := filepath.Join(t.TempDir(), "ready")
	started := time.Now()
	result := executeRunserverProcess(
		context.Background(),
		nil,
		runserverProcessHelperCommand("stream-failure", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready}),
		runserverErrorWriter{err: want},
		io.Discard,
		50*time.Millisecond,
	)
	if !result.Started || result.ExitCode != -1 || result.DirectReaps != 1 || !errors.Is(result.StdoutError, want) || result.StderrError != nil || result.CleanupFailed || result.Interrupted || result.Canceled || !result.Forced || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 1 {
		t.Fatalf("long-lived writer failure = %+v", result)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("long-lived writer failure cleanup duration = %s", elapsed)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("long-lived writer helper did not publish signal readiness: %v", err)
	}
}

func TestRunserverProcessRejectsUnsupportedInvocation(t *testing.T) {
	t.Parallel()
	for _, command := range []Command{
		{},
		{Argv: []string{""}},
		{Argv: []string{"runtime"}, Stdin: []byte("unexpected")},
	} {
		result := executeRunserverProcess(context.Background(), nil, command, io.Discard, io.Discard, 100*time.Millisecond)
		if result.StartError == nil || result.Started || result.DirectReaps != 0 {
			t.Fatalf("invalid invocation = %+v", result)
		}
	}
	result := executeRunserverProcess(context.Background(), nil, Command{Argv: []string{"runtime"}}, io.Discard, io.Discard, 0)
	if result.StartError == nil || result.Started {
		t.Fatalf("invalid grace = %+v", result)
	}
}

type runserverErrorWriter struct {
	err error
}

func (writer runserverErrorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func runserverProcessHelperCommand(mode string, extra map[string]string) Command {
	environment := environmentValues(os.Environ())
	environment["GODJ_RUNSERVER_PROCESS_HELPER"] = mode
	for key, value := range extra {
		environment[key] = value
	}
	return Command{
		Argv: []string{os.Args[0], "-test.run=^TestRunserverProcessHelper$"},
		Env:  sortedEnvironment(environment),
	}
}

func publishRunserverProcessReady(payload string) error {
	ready := os.Getenv("GODJ_RUNSERVER_PROCESS_READY")
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

func waitForRunserverProcessReady(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(payload)))
			if parseErr != nil {
				t.Fatalf("parse ready process %q: %v", payload, parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read ready process: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runserver process did not publish readiness at %s", path)
	return 0
}

func waitForRunserverProcessPair(t *testing.T, path string) (int, int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			parts := strings.Split(strings.TrimSpace(string(payload)), ",")
			if len(parts) != 2 {
				t.Fatalf("invalid ready process pair %q", payload)
			}
			groupPID, groupErr := strconv.Atoi(parts[0])
			holderPID, holderErr := strconv.Atoi(parts[1])
			if groupErr != nil || holderErr != nil {
				t.Fatalf("parse ready process pair %q: %v / %v", payload, groupErr, holderErr)
			}
			return groupPID, holderPID
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read ready process pair: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runserver process did not publish readiness pair at %s", path)
	return 0, 0
}

func assertRunserverProcessGroupAbsent(t *testing.T, groupPID int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !runserverProcessGroupExists(groupPID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runserver process group %d remains after cleanup", groupPID)
}
