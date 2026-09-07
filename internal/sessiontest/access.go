// Package sessiontest checks the shared session Store contract using real stores.
// It owns only the request interleaving; each caller owns its backend fixture.
package sessiontest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

// AtomicAccess checks the gap between Manager's detached read and atomic touch.
// Every case uses the same ID across requests and controls the ordering with a
// barrier rather than sleeps or probabilistic stress.
func AtomicAccess(t *testing.T, newStore func(*testing.T) sessions.Store) {
	t.Helper()
	for _, test := range []struct {
		name   string
		after  int64
		status sessions.TouchStatus
	}{
		{"renewed", 11, sessions.TouchActive},
		{"idle expiry", 10, sessions.TouchExpired},
		{"absolute expiry", 30, sessions.TouchExpired},
		{"rotated", 11, sessions.TouchMissing},
		{"canceled", 11, sessions.TouchMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &readBarrier{Store: newStore(t), entered: make(chan struct{}), release: make(chan struct{})}
			defer store.resume()
			base := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			var seconds atomic.Int64
			manager, err := sessions.NewManager(store, sessions.Config{
				AbsoluteLifetime: 30 * time.Second,
				IdleTimeout:      10 * time.Second,
				Clock:            func() time.Time { return base.Add(time.Duration(seconds.Load()) * time.Second) },
				Random:           bytes.NewReader(append(bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)...)),
			})
			if err != nil {
				t.Fatal(err)
			}
			initial, err := manager.Create(ctx, map[string]string{"principal": "operator"})
			if err != nil {
				t.Fatal(err)
			}
			seconds.Store(8)
			store.armed.Store(true)
			type loadResult struct {
				record sessions.Record
				found  bool
				err    error
			}
			result := make(chan loadResult, 1)
			go func() {
				record, found, err := manager.Load(ctx, initial.ID())
				result <- loadResult{record, found, err}
			}()
			select {
			case <-store.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("load did not reach the detached-read barrier")
			}
			retainedID := initial.ID()
			switch test.name {
			case "absolute expiry":
				for _, second := range []int{9, 18, 27} {
					_, status, err := store.Store.Touch(ctx, initial.ID(), base.Add(time.Duration(second)*time.Second), base.Add(time.Duration(second+10)*time.Second))
					if err != nil || status != sessions.TouchActive {
						t.Fatalf("renew before absolute expiry: status=%v error=%v", status, err)
					}
				}
			case "renewed":
				_, status, err := store.Store.Touch(ctx, initial.ID(), base.Add(9*time.Second), base.Add(19*time.Second))
				if err != nil || status != sessions.TouchActive {
					t.Fatalf("other request's touch: status=%v error=%v", status, err)
				}
			case "rotated":
				seconds.Store(9)
				rotated, err := manager.Rotate(ctx, initial)
				if err != nil {
					t.Fatal(err)
				}
				retainedID = rotated.ID()
			case "canceled":
				cancel()
			}
			seconds.Store(test.after)
			store.resume()
			var got loadResult
			select {
			case got = <-result:
			case <-time.After(5 * time.Second):
				t.Fatal("load did not finish after releasing the barrier")
			}
			wantError := error(nil)
			if test.name == "canceled" {
				wantError = context.Canceled
			}
			if !errors.Is(got.err, wantError) || got.found != (test.status == sessions.TouchActive) {
				t.Fatalf("load: found=%v error=%v, want active=%v error=%v", got.found, got.err, test.status == sessions.TouchActive, wantError)
			}
			if status := sessions.TouchStatus(store.status.Load()); status != test.status || store.deletes.Load() != 0 {
				t.Fatalf("atomic touch status=%v unconditional deletes=%d", status, store.deletes.Load())
			}
			stored, present, err := store.Store.Load(context.Background(), retainedID)
			wantPresent := test.status != sessions.TouchExpired
			if err != nil || present != wantPresent {
				t.Fatalf("stored record: present=%v error=%v, want %v", present, err, wantPresent)
			}
			if present {
				if value, _ := stored.Value("principal"); value != "operator" {
					t.Fatal("access changed session values")
				}
				if stored.AbsoluteExpiresAt() != initial.AbsoluteExpiresAt() {
					t.Fatal("access extended absolute lifetime")
				}
			}
			if got.found && (got.record.IdleExpiresAt() != base.Add(21*time.Second) || got.record.AccessedAt() != base.Add(11*time.Second)) {
				t.Fatal("load did not publish the current monotonic session state")
			}
		})
	}
}

type readBarrier struct {
	sessions.Store
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	status  atomic.Uint32
	deletes atomic.Int64
}

func (store *readBarrier) resume() { store.once.Do(func() { close(store.release) }) }

func (store *readBarrier) Load(ctx context.Context, id sessions.ID) (sessions.Record, bool, error) {
	record, found, err := store.Store.Load(ctx, id)
	if err == nil && found && store.armed.CompareAndSwap(true, false) {
		close(store.entered)
		<-store.release
	}
	return record, found, err
}

func (store *readBarrier) Touch(ctx context.Context, id sessions.ID, at, deadline time.Time) (sessions.Record, sessions.TouchStatus, error) {
	record, status, err := store.Store.Touch(ctx, id, at, deadline)
	store.status.Store(uint32(status))
	return record, status, err
}

func (store *readBarrier) Delete(ctx context.Context, id sessions.ID) error {
	store.deletes.Add(1)
	return store.Store.Delete(ctx, id)
}
