package sessions_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func TestManagerRotatePublishesTouchCompletedAfterReplacementConstruction(t *testing.T) {
	ctx := context.Background()
	memory, err := sessions.NewMemoryStore(4)
	if err != nil {
		t.Fatal(err)
	}
	barrier := &rotateBarrierStore{
		Store:   memory,
		entered: make(chan sessions.Record, 1),
		release: make(chan struct{}),
	}
	base := time.Date(2026, time.August, 25, 0, 0, 0, 0, time.UTC)
	clock := &rotateClock{now: base}
	manager, err := sessions.NewManager(barrier, sessions.Config{
		AbsoluteLifetime: time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            clock.Now,
		Random: bytes.NewReader(bytes.Join([][]byte{
			bytes.Repeat([]byte{0x11}, 32),
			bytes.Repeat([]byte{0x22}, 32),
		}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.Create(ctx, map[string]string{"state": "anonymous"})
	if err != nil {
		t.Fatal(err)
	}
	current, err = current.WithValue("state", "authenticated")
	if err != nil {
		t.Fatal(err)
	}

	rotationAt := base.Add(5 * time.Minute)
	clock.Set(rotationAt)
	type rotateResult struct {
		record sessions.Record
		err    error
	}
	result := make(chan rotateResult, 1)
	go func() {
		record, err := manager.Rotate(ctx, current)
		result <- rotateResult{record: record, err: err}
	}()

	var replacement sessions.Record
	select {
	case replacement = <-barrier.entered:
	case <-time.After(5 * time.Second):
		close(barrier.release)
		t.Fatal("Manager.Rotate did not reach the Store barrier")
	}
	if !replacement.AccessedAt().Equal(rotationAt) || !replacement.IdleExpiresAt().Equal(rotationAt.Add(30*time.Minute)) {
		close(barrier.release)
		t.Fatalf("pre-touch replacement timestamps = %v/%v", replacement.AccessedAt(), replacement.IdleExpiresAt())
	}

	touchAt := base.Add(10 * time.Minute)
	clock.Set(touchAt)
	touched, found, err := manager.Load(ctx, current.ID())
	if err != nil || !found {
		close(barrier.release)
		t.Fatalf("Load while Rotate is paused = (%v,%v,%v)", touched, found, err)
	}
	close(barrier.release)

	var rotated rotateResult
	select {
	case rotated = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Rotate did not leave the Store barrier")
	}
	if rotated.err != nil {
		t.Fatal(rotated.err)
	}
	if !rotated.record.AccessedAt().Equal(touched.AccessedAt()) || !rotated.record.IdleExpiresAt().Equal(touched.IdleExpiresAt()) {
		t.Fatalf("Rotate returned stale timestamps: rotated=%v touched=%v", rotated.record, touched)
	}
	if state, _ := rotated.record.Value("state"); state != "authenticated" {
		t.Fatalf("Rotate returned stale values: state=%q", state)
	}
	if _, found, err := memory.Load(ctx, current.ID()); err != nil || found {
		t.Fatalf("old ID after Rotate = found %v/error %v", found, err)
	}
	persisted, found, err := memory.Load(ctx, rotated.record.ID())
	if err != nil || !found {
		t.Fatalf("published ID after Rotate = (%v,%v,%v)", persisted, found, err)
	}
	if !reflect.DeepEqual(rotated.record.Snapshot(), persisted.Snapshot()) {
		t.Fatalf("returned and persisted Rotate records differ: returned=%v persisted=%v", rotated.record, persisted)
	}
}

func TestManagerRotateUsesAuthoritativeStoredIdleExpiry(t *testing.T) {
	t.Run("stale detached expiry does not delete a later touch", func(t *testing.T) {
		ctx := context.Background()
		store, err := sessions.NewMemoryStore(2)
		if err != nil {
			t.Fatal(err)
		}
		base := time.Date(2026, time.August, 25, 1, 0, 0, 0, time.UTC)
		clock := &rotateClock{now: base}
		manager, err := sessions.NewManager(store, sessions.Config{
			AbsoluteLifetime: time.Hour,
			IdleTimeout:      10 * time.Minute,
			Clock:            clock.Now,
			Random: bytes.NewReader(bytes.Join([][]byte{
				bytes.Repeat([]byte{0x31}, 32),
				bytes.Repeat([]byte{0x32}, 32),
			}, nil)),
		})
		if err != nil {
			t.Fatal(err)
		}
		stale, err := manager.Create(ctx, map[string]string{"state": "stale"})
		if err != nil {
			t.Fatal(err)
		}
		clock.Set(base.Add(9 * time.Minute))
		latest, found, err := manager.Load(ctx, stale.ID())
		if err != nil || !found {
			t.Fatalf("advancing Load = (%v,%v,%v)", latest, found, err)
		}
		clock.Set(base.Add(11 * time.Minute))
		rotated, err := manager.Rotate(ctx, stale)
		if err != nil {
			t.Fatalf("Rotate with stale detached idle expiry: %v", err)
		}
		if !rotated.AccessedAt().Equal(base.Add(11*time.Minute)) || !rotated.IdleExpiresAt().Equal(base.Add(21*time.Minute)) {
			t.Fatalf("rotated timestamps = %v/%v", rotated.AccessedAt(), rotated.IdleExpiresAt())
		}
		persisted, found, err := store.Load(ctx, rotated.ID())
		if err != nil || !found || !reflect.DeepEqual(rotated.Snapshot(), persisted.Snapshot()) {
			t.Fatalf("persisted stale-expiry rotation = (%v,%v,%v)", persisted, found, err)
		}
	})

	t.Run("authoritatively expired row is deleted", func(t *testing.T) {
		ctx := context.Background()
		store, err := sessions.NewMemoryStore(2)
		if err != nil {
			t.Fatal(err)
		}
		base := time.Date(2026, time.August, 25, 2, 0, 0, 0, time.UTC)
		clock := &rotateClock{now: base}
		manager, err := sessions.NewManager(store, sessions.Config{
			AbsoluteLifetime: time.Hour,
			IdleTimeout:      10 * time.Minute,
			Clock:            clock.Now,
			Random: bytes.NewReader(bytes.Join([][]byte{
				bytes.Repeat([]byte{0x41}, 32),
				bytes.Repeat([]byte{0x42}, 32),
				bytes.Repeat([]byte{0x42}, 32),
			}, nil)),
		})
		if err != nil {
			t.Fatal(err)
		}
		expired, err := manager.Create(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		collision, err := manager.Create(ctx, map[string]string{"owner": "collision"})
		if err != nil {
			t.Fatal(err)
		}
		clock.Set(base.Add(11 * time.Minute))
		published, err := manager.Rotate(ctx, expired)
		if !errors.Is(err, &sessions.Error{Code: sessions.CodeNotFound}) || published.ID().Valid() {
			t.Fatalf("Rotate expired = (%v,%v), want zero/not_found", published, err)
		}
		if _, found, err := store.Load(ctx, expired.ID()); err != nil || found {
			t.Fatalf("expired old ID after Rotate = found %v/error %v", found, err)
		}
		blocker, found, err := store.Load(ctx, collision.ID())
		if err != nil || !found {
			t.Fatalf("colliding replacement row after expired Rotate = (%v,%v,%v)", blocker, found, err)
		}
		if owner, _ := blocker.Value("owner"); owner != "collision" {
			t.Fatalf("expired Rotate changed collision row: owner=%q", owner)
		}
	})
}

func TestManagerRotateRetriesCollisionAndPreservesCollisionRow(t *testing.T) {
	ctx := context.Background()
	store, err := sessions.NewMemoryStore(4)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.August, 25, 3, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return base },
		Random: bytes.NewReader(bytes.Join([][]byte{
			bytes.Repeat([]byte{0x51}, 32),
			bytes.Repeat([]byte{0x52}, 32),
			bytes.Repeat([]byte{0x52}, 32),
			bytes.Repeat([]byte{0x53}, 32),
		}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	old, err := manager.Create(ctx, map[string]string{"owner": "old"})
	if err != nil {
		t.Fatal(err)
	}
	collision, err := manager.Create(ctx, map[string]string{"owner": "collision"})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := manager.Rotate(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ID() == old.ID() || rotated.ID() == collision.ID() {
		t.Fatalf("Rotate did not retry the colliding ID: rotated=%v", rotated.ID())
	}
	if _, found, err := store.Load(ctx, old.ID()); err != nil || found {
		t.Fatalf("old collision-retry ID = found %v/error %v", found, err)
	}
	blocker, found, err := store.Load(ctx, collision.ID())
	if err != nil || !found {
		t.Fatalf("collision record disappeared = (%v,%v,%v)", blocker, found, err)
	}
	if owner, _ := blocker.Value("owner"); owner != "collision" {
		t.Fatalf("collision record changed: owner=%q", owner)
	}
	persisted, found, err := store.Load(ctx, rotated.ID())
	if err != nil || !found || !reflect.DeepEqual(rotated.Snapshot(), persisted.Snapshot()) {
		t.Fatalf("collision-retry publication = (%v,%v,%v)", persisted, found, err)
	}
}

type rotateBarrierStore struct {
	sessions.Store
	entered chan sessions.Record
	release chan struct{}
}

func (store *rotateBarrierStore) Rotate(
	ctx context.Context,
	oldID sessions.ID,
	replacement sessions.Record,
) (sessions.Record, bool, error) {
	store.entered <- replacement
	select {
	case <-store.release:
		return store.Store.Rotate(ctx, oldID, replacement)
	case <-ctx.Done():
		return sessions.Record{}, false, ctx.Err()
	}
}

type rotateClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *rotateClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *rotateClock) Set(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}
