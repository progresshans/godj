//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/runservercontrol"
)

func publicationTestCandidate(t *testing.T) (*runserverCandidate, net.Conn) {
	t.Helper()
	parent, child := net.Pipe()
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
	return &runserverCandidate{process: &managedRunserver{control: parent}}, child
}

func publicationTestReply(t *testing.T, child net.Conn, expected, reply string, cancel context.CancelFunc) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		if err := runservercontrol.Expect(child, expected); err != nil {
			done <- err
			return
		}
		if cancel != nil {
			cancel()
		}
		done <- runservercontrol.Send(child, reply)
	}()
	t.Cleanup(func() {
		_ = child.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("publication peer was not joined")
		}
	})
	return done
}

func requirePublicationPeer(t *testing.T, peer <-chan error) {
	t.Helper()
	select {
	case err := <-peer:
		if err != nil {
			t.Fatal("publication acknowledgment was interrupted", err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication peer did not finish")
	}
}

// Pause the supervisor between sending Retire and reading its reply until
// the child has exited and the production process waiter has joined it.
type readAfterRunserverExit struct {
	net.Conn
	done <-chan struct{}
	ctx  context.Context
}

func (conn readAfterRunserverExit) Read(p []byte) (int, error) {
	select {
	case <-conn.done:
		return conn.Conn.Read(p)
	case <-conn.ctx.Done():
		return 0, conn.ctx.Err()
	}
}

func TestRunserverPublicationReadsRetirementReplyAfterChildExit(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "runtime")
	// The fixture uses the same inherited control socket as a project server.
	// It exits immediately after writing the chosen response.
	const app = `#!/bin/sh
IFS= read -r request <&4 || exit 41
[ "$request" = "$GODJ_TEST_RETIRE" ] || exit 42
printf '%s' "$GODJ_TEST_REPLY" >&4
`
	if err := os.WriteFile(binary, []byte(app), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	prepared := preparedProjectRuntime{
		binary:  binary,
		command: &projectCommand[RunServerFailure]{selected: retainedProject{rootPath: dir}},
	}
	for _, tc := range []struct {
		name    string
		reply   string
		wantErr error
	}{
		{"acknowledged", runservercontrol.Retired, nil},
		{"missing", "", io.EOF},
		{"truncated", strings.TrimSuffix(runservercontrol.Retired, "\n"), io.EOF},
		{"wrong-message", runservercontrol.Active, runservercontrol.ErrProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			input := RunServerInvocation{
				Environment: []string{
					"GODJ_TEST_RETIRE=" + strings.TrimSuffix(runservercontrol.Retire, "\n"),
					"GODJ_TEST_REPLY=" + tc.reply,
				},
				Stdout:  io.Discard,
				Stderr:  io.Discard,
				runtime: runserverRuntimeHooks{grace: time.Second},
			}
			process, err := newManagedRunserver(ctx, input, prepared, listener)
			if err != nil {
				t.Fatal(err)
			}
			active := &runserverCandidate{process: process}
			process.control = readAfterRunserverExit{Conn: process.control, done: process.done, ctx: ctx}
			t.Cleanup(func() {
				result := process.stop(active.retired)
				if result.ExitCode != 0 || result.DirectReaps != 1 || result.WaitError != nil || result.Forced || result.CleanupFailed {
					t.Errorf("retiring child did not exit cleanly: %+v", result)
				}
				if err := process.control.SetDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
					t.Errorf("control socket after stop: %v", err)
				}
			})
			candidate, next := publicationTestCandidate(t)
			peer := publicationTestReply(t, next, runservercontrol.Activate, runservercontrol.Active, nil)
			err = publishRunserverCandidate(ctx, active, candidate)
			wantPublished := 0
			if tc.wantErr == nil {
				wantPublished = 1
			}
			if !errors.Is(err, tc.wantErr) || active.retired != (wantPublished == 1) || candidate.report.ReloadPublished != wantPublished {
				t.Fatalf("retirement reply after child exit: err=%v, retired=%t, published=%d", err, active.retired, candidate.report.ReloadPublished)
			}
			if wantPublished == 1 {
				requirePublicationPeer(t, peer)
				return
			}
			_ = candidate.process.control.Close()
			select {
			case err := <-peer:
				if !errors.Is(err, io.EOF) {
					t.Fatalf("successor received activation after rejected retirement: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestRunserverPublicationRetainsRetirementAcknowledgedDuringCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	active, previous := publicationTestCandidate(t)
	candidate, next := publicationTestCandidate(t)
	peer := publicationTestReply(t, previous, runservercontrol.Retire, runservercontrol.Retired, cancel)
	err := publishRunserverCandidate(ctx, active, candidate)
	if !errors.Is(err, context.Canceled) || !active.retired || candidate.report.ReloadPublished != 0 {
		t.Fatal("cancellation lost acknowledged retirement or published the next app", err, active.retired, candidate.report.ReloadPublished)
	}
	requirePublicationPeer(t, peer)
	if err := next.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var buffer [1]byte
	_, err = next.Read(buffer[:])
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal("cancellation between phases sent activation", err)
	}
}

func TestRunserverPublicationRetainsActivationAcknowledgedDuringCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	candidate, child := publicationTestCandidate(t)
	peer := publicationTestReply(t, child, runservercontrol.Activate, runservercontrol.Active, cancel)
	if err := publishRunserverCandidate(ctx, nil, candidate); err != nil || candidate.report.ReloadPublished != 1 {
		t.Fatal("cancellation lost an acknowledged active app", err, candidate.report.ReloadPublished)
	}
	requirePublicationPeer(t, peer)
}

func TestRunserverPublicationRejectsCancellationBeforeStarting(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	active, _ := publicationTestCandidate(t)
	candidate, _ := publicationTestCandidate(t)
	if err := publishRunserverCandidate(ctx, active, candidate); !errors.Is(err, context.Canceled) || active.retired || candidate.report.ReloadPublished != 0 {
		t.Fatal("canceled publication entered a control phase", err)
	}
}

func TestRunserverPublicationRejectsInvalidActivationAcknowledgment(t *testing.T) {
	candidate, child := publicationTestCandidate(t)
	publicationTestReply(t, child, runservercontrol.Activate, runservercontrol.Prepared, nil)
	if err := publishRunserverCandidate(t.Context(), nil, candidate); err == nil || candidate.report.ReloadPublished != 0 {
		t.Fatal("invalid acknowledgment published an app", err)
	}
}
