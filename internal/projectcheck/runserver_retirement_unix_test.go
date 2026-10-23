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
	ready, release := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "release")
	command := runserverProcessHelperCommand("retired-exit", map[string]string{
		"GODJ_RUNSERVER_PROCESS_READY": ready, "GODJ_RUNSERVER_PROCESS_RELEASE": release,
	})
	ctx, cancel := context.WithCancel(t.Context())
	parent, child := net.Pipe()
	defer child.Close()
	process := &managedRunserver{control: parent, cancel: cancel, grace: 3 * time.Second, done: make(chan struct{})}
	go func() {
		process.result = executeRunserverProcess(ctx, nil, command, io.Discard, io.Discard, time.Second)
		close(process.done)
	}()
	t.Cleanup(func() { cancel(); <-process.done; _ = parent.Close() })
	waitForRunserverProcessReady(t, ready)
	joined := make(chan runserverProcessResult, 1)
	started := make(chan struct{})
	go func() { close(started); joined <- process.stop(true) }()
	<-started
	select {
	case <-ctx.Done():
		t.Fatal("retired child received cancellation before its grace expired")
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
}

func TestRunserverRetirementGraceStillBoundsAnUncooperativeChild(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := runserverProcessHelperCommand("ignore", map[string]string{"GODJ_RUNSERVER_PROCESS_READY": ready})
	ctx, cancel := context.WithCancel(t.Context())
	parent, child := net.Pipe()
	defer child.Close()
	process := &managedRunserver{control: parent, cancel: cancel, grace: 50 * time.Millisecond, done: make(chan struct{})}
	go func() {
		process.result = executeRunserverProcess(ctx, nil, command, io.Discard, io.Discard, 50*time.Millisecond)
		close(process.done)
	}()
	t.Cleanup(func() { cancel(); <-process.done; _ = parent.Close() })
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
