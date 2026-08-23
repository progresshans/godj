package sessions_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func TestManagerCreateLoadRotateAndFlush(t *testing.T) {
	t.Parallel()
	store, err := sessions.NewMemoryStore(8)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	random := bytes.NewReader(append(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)...))
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: time.Hour,
		IdleTimeout:      10 * time.Minute,
		Clock:            clock.Now,
		Random:           random,
	})
	if err != nil {
		t.Fatal(err)
	}

	record, err := manager.Create(context.Background(), map[string]string{"theme": "dark"})
	if err != nil {
		t.Fatal(err)
	}
	if len(record.ID().Encoded()) != 43 || record.ID().String() != "[session-id]" {
		t.Fatalf("unexpected opaque ID shape: length=%d string=%q", len(record.ID().Encoded()), record.ID().String())
	}
	values := record.Values()
	values["theme"] = "mutated"
	if value, _ := record.Value("theme"); value != "dark" {
		t.Fatalf("record aliased caller values: %q", value)
	}

	clock.Advance(5 * time.Minute)
	loaded, found, err := manager.Load(context.Background(), record.ID())
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if !loaded.AccessedAt().Equal(now.Add(5*time.Minute)) || !loaded.IdleExpiresAt().Equal(now.Add(15*time.Minute)) {
		t.Fatalf("unexpected sliding expiry: accessed=%v idle=%v", loaded.AccessedAt(), loaded.IdleExpiresAt())
	}
	changed, err := loaded.WithValue("principal", "operator")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := manager.Rotate(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ID() == record.ID() || !rotated.AbsoluteExpiresAt().Equal(record.AbsoluteExpiresAt()) {
		t.Fatal("rotation did not replace ID while preserving absolute expiry")
	}
	if _, found, err := manager.Load(context.Background(), record.ID()); err != nil || found {
		t.Fatalf("old identifier survived rotation: found=%v err=%v", found, err)
	}
	if value, ok := rotated.Value("principal"); !ok || value != "operator" {
		t.Fatalf("rotation lost derived values: %q %v", value, ok)
	}
	if err := manager.Flush(context.Background(), rotated.ID()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := manager.Load(context.Background(), rotated.ID()); err != nil || found {
		t.Fatalf("flushed record remains: found=%v err=%v", found, err)
	}
}

func TestManagerAbsoluteAndIdleExpiryDeleteRecords(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		advance time.Duration
	}{
		{name: "idle", advance: 11 * time.Minute},
		{name: "absolute", advance: 61 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := sessions.NewMemoryStore(1)
			if err != nil {
				t.Fatal(err)
			}
			clock := &testClock{now: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)}
			manager, err := sessions.NewManager(store, sessions.Config{
				AbsoluteLifetime: time.Hour,
				IdleTimeout:      10 * time.Minute,
				Clock:            clock.Now,
				Random:           bytes.NewReader(bytes.Repeat([]byte{3}, 32)),
			})
			if err != nil {
				t.Fatal(err)
			}
			record, err := manager.Create(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			clock.Advance(test.advance)
			if _, found, err := manager.Load(context.Background(), record.ID()); err != nil || found {
				t.Fatalf("expired record: found=%v err=%v", found, err)
			}
			if _, found, err := store.Load(context.Background(), record.ID()); err != nil || found {
				t.Fatalf("expired record was not deleted: found=%v err=%v", found, err)
			}
		})
	}
}

func TestManagerBoundsContextAndSecretFreeEntropyFailure(t *testing.T) {
	t.Parallel()
	store, err := sessions.NewMemoryStore(1)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{
		Limits: sessions.Limits{MaxValues: 1, MaxKeyBytes: 4, MaxValueBytes: 4, MaxTotalBytes: 8},
		Random: errorReader{err: errors.New("reader diagnostic contains SHOULD_NOT_ESCAPE")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(context.Background(), map[string]string{"long-key": "x"}); !errors.Is(err, &sessions.Error{Code: sessions.CodeInvalidRecord}) {
		t.Fatalf("expected bounded value failure, got %v", err)
	}
	_, err = manager.Create(context.Background(), nil)
	if !errors.Is(err, &sessions.Error{Code: sessions.CodeEntropy}) {
		t.Fatalf("expected entropy error, got %v", err)
	}
	if strings.Contains(err.Error(), "SHOULD_NOT_ESCAPE") {
		t.Fatalf("cause leaked through diagnostic: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Create(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
}

func TestManagerCollisionAndMemoryCapacityFailClosed(t *testing.T) {
	t.Parallel()
	t.Run("capacity", func(t *testing.T) {
		store, err := sessions.NewMemoryStore(1)
		if err != nil {
			t.Fatal(err)
		}
		random := bytes.NewReader(append(bytes.Repeat([]byte{4}, 32), bytes.Repeat([]byte{5}, 32)...))
		manager, err := sessions.NewManager(store, sessions.Config{Random: random})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Create(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Create(context.Background(), nil); !errors.Is(err, &sessions.Error{Code: sessions.CodeStoreFull}) {
			t.Fatalf("expected store-full error, got %v", err)
		}
	})
	t.Run("collision", func(t *testing.T) {
		store, err := sessions.NewMemoryStore(8)
		if err != nil {
			t.Fatal(err)
		}
		random := bytes.NewReader(bytes.Repeat([]byte{6}, 32*5))
		manager, err := sessions.NewManager(store, sessions.Config{Random: random})
		if err != nil {
			t.Fatal(err)
		}
		first, err := manager.Create(context.Background(), map[string]string{"owner": "first"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Create(context.Background(), map[string]string{"owner": "second"}); !errors.Is(err, &sessions.Error{Code: sessions.CodeEntropy}) {
			t.Fatalf("expected collision limit, got %v", err)
		}
		loaded, found, err := manager.Load(context.Background(), first.ID())
		if err != nil || !found {
			t.Fatalf("first record disappeared: found=%v err=%v", found, err)
		}
		if owner, _ := loaded.Value("owner"); owner != "first" {
			t.Fatalf("collision overwrote record: %q", owner)
		}
	})
}

func TestMemoryStoreReapsExpiredRecordsOnlyAtCapacity(t *testing.T) {
	t.Parallel()
	store, err := sessions.NewMemoryStore(1)
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)}
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: time.Hour,
		IdleTimeout:      time.Minute,
		Clock:            clock.Now,
		Random:           bytes.NewReader(append(bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)...)),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	second, err := manager.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("create after passive expiry: %v", err)
	}
	if first.ID() == second.ID() {
		t.Fatal("expired record replacement reused an identifier")
	}
	if _, found, err := store.Load(context.Background(), first.ID()); err != nil || found {
		t.Fatalf("expired capacity entry survived reap: found=%v err=%v", found, err)
	}
}

func TestMemoryStoreTouchDoesNotRegressUnderOutOfOrderCalls(t *testing.T) {
	t.Parallel()
	store, err := sessions.NewMemoryStore(1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: time.Hour,
		IdleTimeout:      10 * time.Minute,
		Clock:            func() time.Time { return now },
		Random:           bytes.NewReader(bytes.Repeat([]byte{9}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	laterAccess := now.Add(10 * time.Minute)
	laterExpiry := now.Add(20 * time.Minute)
	if _, found, err := store.Touch(context.Background(), record.ID(), laterAccess, laterExpiry); err != nil || !found {
		t.Fatalf("later touch: found=%v err=%v", found, err)
	}
	touched, found, err := store.Touch(context.Background(), record.ID(), now.Add(5*time.Minute), now.Add(15*time.Minute))
	if err != nil || !found {
		t.Fatalf("older touch: found=%v err=%v", found, err)
	}
	if !touched.AccessedAt().Equal(laterAccess) || !touched.IdleExpiresAt().Equal(laterExpiry) {
		t.Fatalf("touch regressed: accessed=%v idle=%v", touched.AccessedAt(), touched.IdleExpiresAt())
	}
}

func TestMemoryStoreConcurrentLifecycle(t *testing.T) {
	store, err := sessions.NewMemoryStore(256)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 64
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			ctx := context.Background()
			record, err := manager.Create(ctx, map[string]string{"worker": fmt.Sprint(worker)})
			if err != nil {
				errorsFound <- err
				return
			}
			loaded, found, err := manager.Load(ctx, record.ID())
			if err != nil || !found {
				errorsFound <- fmt.Errorf("load found=%v: %w", found, err)
				return
			}
			rotated, err := manager.Rotate(ctx, loaded)
			if err != nil {
				errorsFound <- err
				return
			}
			if err := manager.Delete(ctx, rotated.ID()); err != nil {
				errorsFound <- err
			}
		}(worker)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionIdentifierParserRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("!", 43)} {
		if _, err := sessions.ParseID(value); err == nil {
			t.Fatalf("accepted invalid identifier of length %d", len(value))
		}
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

var _ io.Reader = errorReader{}
