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
