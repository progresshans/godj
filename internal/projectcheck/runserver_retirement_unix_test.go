//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunserverRetiredProcessExitsWithoutAnExtraInterrupt(t *testing.T) {
	for _, stop := range []string{"none", "operator-interrupt", "parent-cancel"} {
		t.Run(stop, func(t *testing.T) {
			ready, release := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "release")
			command := runserverProcessHelperCommand("retired-exit", map[string]string{
				"GODJ_RUNSERVER_PROCESS_READY": ready, "GODJ_RUNSERVER_PROCESS_RELEASE": release,
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			interrupt := make(chan struct{})
			parent, child := net.Pipe()
			defer child.Close()
			process := startManagedRunserverProcess(RunServerInvocation{
				Context: ctx, Interrupt: interrupt, Stdout: io.Discard, Stderr: io.Discard,
				runtime: runserverRuntimeHooks{grace: 3 * time.Second},
			}, command, parent, nil)
			t.Cleanup(func() { process.cancel(); <-process.done; _ = parent.Close() })
			waitForRunserverProcessReady(t, ready)
			joined := make(chan runserverProcessResult, 1)
			started := make(chan struct{})
			go func() { close(started); joined <- process.stop(true) }()
			<-started
			switch stop {
			case "operator-interrupt":
				close(interrupt)
			case "parent-cancel":
				cancel()
			}
			select {
			case result := <-joined:
				t.Fatalf("retired child was interrupted before normal exit: %+v", result)
			case <-time.After(100 * time.Millisecond):
			}
			if err := os.WriteFile(release, []byte("exit"), 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-joined:
				if result.ExitCode != 0 || result.WaitError != nil || result.SIGINTAttempts != 0 || result.Forced || result.CleanupFailed || result.DirectReaps != 1 {
					t.Fatalf("cooperative retirement failed: %+v", result)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("retired process was not joined")
			}
		})
	}
}

func TestRunserverRetirementGraceStillBoundsAnUncooperativeChild(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := runserverProcessHelperCommand("ignore", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
	ctx, cancel := context.WithCancel(t.Context())
	parent, child := net.Pipe()
	defer child.Close()
	defer cancel()
	process := startManagedRunserverProcess(RunServerInvocation{
		Context: ctx, Stdout: io.Discard, Stderr: io.Discard,
		runtime: runserverRuntimeHooks{grace: 50 * time.Millisecond},
	}, command, parent, nil)
	t.Cleanup(func() { process.cancel(); <-process.done; _ = parent.Close() })
	waitForRunserverProcessReady(t, ready)
	joined := make(chan runserverProcessResult, 1)
	go func() { joined <- process.stop(true) }()
	select {
	case result := <-joined:
		if !result.Forced || result.SIGINTAttempts != 1 || result.SIGKILLAttempts != 1 || result.DirectReaps != 1 || result.CleanupFailed {
			t.Fatalf("uncooperative retirement was not reaped: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retirement grace did not bound the child")
	}
}

func TestRunserverActiveProcessStopsThroughItsOwnerAfterCancellation(t *testing.T) {
	for _, stop := range []string{"operator-interrupt", "parent-cancel"} {
		t.Run(stop, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			command := runserverProcessHelperCommand("graceful", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
			parentContext, cancel := context.WithCancel(t.Context())
			defer cancel()
			interrupt := make(chan struct{})
			ctx, join := commandInterruptContext(parentContext, interrupt)
			defer join()
			parent, child := net.Pipe()
			defer child.Close()
			process := startManagedRunserverProcess(RunServerInvocation{
				Context: ctx, Interrupt: interrupt, Stdout: io.Discard, Stderr: io.Discard,
				runtime: runserverRuntimeHooks{grace: 3 * time.Second},
			}, command, parent, nil)
			t.Cleanup(func() { process.cancel(); <-process.done; _ = parent.Close() })
			waitForRunserverProcessReady(t, ready)
			if stop == "operator-interrupt" {
				close(interrupt)
			} else {
				cancel()
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("owner cancellation did not stop preparation")
			}
			result := process.stop(false)
			if result.ExitCode != 0 || result.WaitError != nil || result.SIGINTAttempts != 1 || !result.Canceled || result.Forced || result.CleanupFailed || result.DirectReaps != 1 {
				t.Fatalf("owner did not join the active runtime: %+v", result)
			}
		})
	}
}
