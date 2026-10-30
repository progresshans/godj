package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
)

func TestPBKDF2EntropyPanicReleasesSourceForSubsequentHashes(t *testing.T) {
	source := &panicOnceSalt{}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000, Random: source})
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() != source {
				t.Fatal("entropy panic did not propagate unchanged")
			}
		}()
		_, _ = hasher.Hash(context.Background(), "password")
	}()
	type result struct {
		encoded string
		err     error
	}
	finished := make(chan result, 8)
	for range cap(finished) {
		go func() {
			encoded, err := hasher.Hash(context.Background(), "password")
			finished <- result{encoded, err}
		}()
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for range cap(finished) {
		select {
		case got := <-finished:
			if got.err != nil {
				t.Fatal(got.err)
			}
			if valid, err := hasher.Verify(context.Background(), "password", got.encoded); err != nil || !valid {
				t.Fatalf("subsequent hash verification = %v, %v", valid, err)
			}
		case <-deadline.C:
			t.Fatal("entropy panic retained the source lock")
		}
	}
}

type panicOnceSalt struct{ panicked bool }

func (source *panicOnceSalt) Read(buffer []byte) (int, error) {
	if !source.panicked {
		source.panicked = true
		panic(source)
	}
	for index := range buffer {
		buffer[index] = byte(index)
	}
	return len(buffer), nil
}
