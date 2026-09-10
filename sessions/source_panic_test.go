package sessions_test

import (
	"context"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func TestManagerEntropyPanicDoesNotRetainSourceLock(t *testing.T) {
	store, err := sessions.NewMemoryStore(2)
	if err != nil {
		t.Fatal(err)
	}
	source := &panicOnceEntropy{}
	manager, err := sessions.NewManager(store, sessions.Config{Random: source})
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() != source {
				t.Fatal("entropy panic did not propagate")
			}
		}()
		_, _ = manager.Create(context.Background(), nil)
	}()
	finished := make(chan error, 1)
	go func() {
		_, err := manager.Create(context.Background(), nil)
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("entropy panic retained the source lock")
	}
}

type panicOnceEntropy struct{ panicked bool }

func (source *panicOnceEntropy) Read(buffer []byte) (int, error) {
	if !source.panicked {
		source.panicked = true
		panic(source)
	}
	for i := range buffer {
		buffer[i] = byte(i)
	}
	return len(buffer), nil
}
