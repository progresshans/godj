package sessiontest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

// AccessValidation checks policy failures against real stores, including
// validation before clock sampling and cancellation before any publication.
func AccessValidation(t *testing.T, newStore func(*testing.T) sessions.Store) {
	t.Helper()
	for _, name := range []string{"manager value budget", "zero clock", "missing with zero clock", "canceling clock", "panicking clock"} {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			base := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			seedManager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return base }})
			if err != nil {
				t.Fatal(err)
			}
			original, err := seedManager.Create(context.Background(), map[string]string{"key": "stored-value"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clockCalls, failing := 0, true
			marker := &struct{}{}
			config := sessions.Config{Clock: func() time.Time {
				clockCalls++
				if failing {
					switch name {
					case "zero clock", "missing with zero clock":
						return time.Time{}
					case "canceling clock":
						cancel()
					case "panicking clock":
						panic(marker)
					}
				}
				return base.Add(time.Minute)
			}}
			if name == "manager value budget" {
				config.Limits.MaxValueBytes = 1
			}
			if name == "missing with zero clock" {
				if err := store.Delete(ctx, original.ID()); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := sessions.NewManager(store, config)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			var loaded sessions.Record
			var panicValue any
			func() {
				defer func() { panicValue = recover() }()
				loaded, found, err = manager.Load(ctx, original.ID())
			}()
			if found || loaded.ID().Valid() {
				t.Fatal("failed access published a record")
			}
			switch name {
			case "manager value budget":
				classified, ok := err.(*sessions.Error)
				if !ok || classified.Code != sessions.CodeInvalidRecord || clockCalls != 0 {
					t.Fatalf("budget error/clock = %v/%d", err, clockCalls)
				}
			case "zero clock":
				classified, ok := err.(*sessions.Error)
				if !ok || classified.Code != sessions.CodeInvalidConfig || clockCalls != 1 {
					t.Fatalf("clock error/calls = %v/%d", err, clockCalls)
				}
			case "missing with zero clock":
				if err != nil || clockCalls != 0 {
					t.Fatalf("missing access = %v, clock calls=%d", err, clockCalls)
				}
			case "canceling clock":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceling access = %v", err)
				}
			case "panicking clock":
				if panicValue != marker {
					t.Fatalf("clock panic = %v", panicValue)
				}
			}
			failing = false
			// A bounded goroutine also detects a leaked store or Manager source
			// lock after panic without leaving the test itself stuck indefinitely.
			finished := make(chan error, 1)
			go func() {
				stored, present, loadErr := store.Load(context.Background(), original.ID())
				if loadErr == nil && (present != (name != "missing with zero clock") || (present && !reflect.DeepEqual(stored.Snapshot(), original.Snapshot()))) {
					loadErr = errors.New("failed access changed stored state")
				}
				if loadErr == nil && name == "panicking clock" {
					_, present, loadErr = manager.Load(context.Background(), original.ID())
					if loadErr == nil && !present {
						loadErr = errors.New("load after panic lost session")
					}
				}
				finished <- loadErr
			}()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("access failure retained a manager or store lock")
			}
		})
	}
}
