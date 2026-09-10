package testprocess

import (
	"strings"
	"sync"
	"testing"
)

func TestBufferPreservesBoundedPrefixAndReportsDiscardedOutput(t *testing.T) {
	t.Parallel()
	buffer := NewBuffer(5)
	for _, chunk := range []string{"ab", "cde"} {
		if n, err := buffer.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if buffer.String() != "abcde" || buffer.Truncated() {
		t.Fatal("exact-limit output changed or was reported as truncated")
	}
	if n, err := buffer.Write([]byte("fg")); n != 2 || err != nil {
		t.Fatalf("discarded write = %d, %v; want full acceptance", n, err)
	}
	if buffer.String() != "abcde" || !buffer.Truncated() {
		t.Fatal("overflow changed the retained prefix or lost truncation")
	}
	zero := NewBuffer(0)
	if n, err := zero.Write([]byte("x")); n != 1 || err != nil || zero.String() != "" || !zero.Truncated() {
		t.Fatal("zero-limit output was not discarded and reported")
	}
}

func TestBufferSupportsConcurrentWritersAndReaders(t *testing.T) {
	t.Parallel()
	buffer := NewBuffer(32)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 8 {
				_, _ = buffer.Write([]byte("x"))
				_ = buffer.String()
				_ = buffer.Truncated()
			}
		})
	}
	workers.Wait()
	if buffer.String() != strings.Repeat("x", 32) || !buffer.Truncated() {
		t.Fatal("concurrent output lost its prefix or truncation signal")
	}
}

func TestReadinessBufferWaitsForCompleteLineAndPublishesOnlyFirst(t *testing.T) {
	t.Parallel()
	output := NewReadinessBuffer(128, "listening ")
	for _, fragment := range []string{"noise\nlist", "ening 127.0."} {
		_, _ = output.Write([]byte(fragment))
	}
	select {
	case address := <-output.Ready():
		t.Fatalf("partial readiness published %q", address)
	default:
	}
	_, _ = output.Write([]byte("0.1:8123\nlistening other\n"))
	select {
	case address := <-output.Ready():
		if address != "127.0.0.1:8123" {
			t.Fatalf("complete readiness = %q", address)
		}
	default:
		t.Fatal("complete readiness was not published")
	}
	select {
	case address := <-output.Ready():
		t.Fatalf("second readiness published %q", address)
	default:
	}
	bounded := NewReadinessBuffer(10, "listening ")
	_, _ = bounded.Write([]byte("listening host:80\n"))
	if !bounded.Truncated() {
		t.Fatal("readiness output lost its bound")
	}
	select {
	case <-bounded.Ready():
		t.Fatal("truncated readiness line was published")
	default:
	}
}

func TestBufferEmptyWriteAndCopiedBytesPreserveOutput(t *testing.T) {
	t.Parallel()
	buffer := NewBuffer(3)
	_, _ = buffer.Write([]byte("abc"))
	_, _ = buffer.Write(nil)
	copied := buffer.Bytes()
	copied[0] = 'X'
	value, truncated := buffer.Snapshot()
	if value != "abc" || truncated {
		t.Fatalf("snapshot = %q truncated=%t", value, truncated)
	}
}
