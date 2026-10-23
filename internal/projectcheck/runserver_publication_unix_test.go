//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"net"
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
