package sessions_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/sessiontest"
	"github.com/progresshans/godj/sessions"
)

type serialLoginEntropy struct {
	next  byte
	calls int
}

func (source *serialLoginEntropy) Read(data []byte) (int, error) {
	source.next++
	source.calls++
	for index := range data {
		data[index] = source.next
	}
	return len(data), nil
}

func TestManagerStoreRebindingSharesSourcesAndPreservesBindings(t *testing.T) {
	first, _ := sessions.NewMemoryStore(128)
	second, _ := sessions.NewMemoryStore(128)
	source := &serialLoginEntropy{}
	clockCalls := 0
	manager, err := sessions.NewManager(first, sessions.Config{Random: source, Clock: func() time.Time {
		clockCalls++
		return time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC).Add(time.Duration(clockCalls) * time.Millisecond)
	}})
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := manager.WithStore(second)
	if err != nil || manager.Store() != first || rebound.Store() != second {
		t.Fatal("store binding changed original manager", err)
	}
	start := make(chan struct{})
	ids := make(chan sessions.ID, 64)
	var workers sync.WaitGroup
	for index := range 64 {
		workers.Go(func() {
			<-start
			active, other := manager, sessions.Store(second)
			if index%2 == 1 {
				active, other = rebound, first
			}
			record, err := active.Create(t.Context(), map[string]string{"owner": "snapshot"})
			if err != nil {
				t.Error(err)
				return
			}
			if _, exists, err := other.Load(t.Context(), record.ID()); err != nil || exists {
				t.Error("rebound write used another store")
			}
			ids <- record.ID()
		})
	}
	close(start)
	workers.Wait()
	close(ids)
	seen := map[sessions.ID]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("shared entropy source raced or duplicated")
		}
		seen[id] = true
	}
	if len(seen) != 64 || source.calls != 64 || clockCalls != 64 {
		t.Fatal("source owner was copied or called during binding", len(seen), source.calls, clockCalls)
	}
	var absent *sessions.MemoryStore
	if _, err := manager.WithStore(absent); err == nil {
		t.Fatal("typed nil rebound store accepted")
	}
	if _, err := sessions.NewManager(absent, sessions.Config{}); err == nil {
		t.Fatal("typed nil initial store accepted")
	}
	if _, err := (*sessions.Manager)(nil).WithStore(first); err == nil {
		t.Fatal("nil manager rebound")
	}
	if (*sessions.Manager)(nil).Store() != nil {
		t.Fatal("nil manager published a store")
	}
}

func TestManagerPeekDoesNotTouchOrDeleteStoredState(t *testing.T) {
	store, _ := sessions.NewMemoryStore(4)
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return now }, IdleTimeout: 10 * time.Second, AbsoluteLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	original, err := manager.Create(t.Context(), map[string]string{"owner": "private"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Second)
	peek, found, err := manager.Peek(t.Context(), original.ID())
	if err != nil || !found || !reflect.DeepEqual(peek.Snapshot(), original.Snapshot()) {
		t.Fatal("peek touched or lost stored state", err)
	}
	now = now.Add(time.Second)
	if record, found, err := manager.Peek(t.Context(), original.ID()); err != nil || found || record.ID().Valid() {
		t.Fatal("peek exposed expired state", err)
	}
	stored, found, err := store.Load(t.Context(), original.ID())
	if err != nil || !found || !reflect.DeepEqual(stored.Snapshot(), original.Snapshot()) {
		t.Fatal("peek deleted or modified an expired row", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := manager.Peek(ctx, original.ID()); !errors.Is(err, context.Canceled) {
		t.Fatal("peek cancellation lost", err)
	}
}

func TestMemoryStoreFreshReplacement(t *testing.T) {
	sessiontest.Replacement(t, func(t *testing.T) sessions.Store {
		store, err := sessions.NewMemoryStore(4)
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}

type loginFailureStore struct {
	sessions.Store
	failure error
	calls   int
}

func (store *loginFailureStore) Create(context.Context, sessions.Record) (bool, error) {
	store.calls++
	return false, store.failure
}
func (store *loginFailureStore) Rotate(context.Context, sessions.ID, sessions.Record) (sessions.Record, bool, error) {
	store.calls++
	return sessions.Record{}, false, store.failure
}
func (store *loginFailureStore) Replace(context.Context, sessions.ID, sessions.Record) (sessions.Record, bool, error) {
	store.calls++
	return sessions.Record{}, false, store.failure
}

func TestManagerDoesNotRetryWrappedCollisionsOrEraseCapacityCauses(t *testing.T) {
	for _, operation := range []string{"create", "rotate", "replace"} {
		t.Run(operation, func(t *testing.T) {
			memory, _ := sessions.NewMemoryStore(4)
			manager, err := sessions.NewManager(memory, sessions.Config{})
			if err != nil {
				t.Fatal(err)
			}
			original, err := manager.Create(t.Context(), map[string]string{"owner": "preserved"})
			if err != nil {
				t.Fatal(err)
			}
			uncertain := errors.New("private uncertain persistence detail")
			code := sessions.CodeEntropy
			if operation == "create" {
				code = sessions.CodeStoreFull
			}
			store := &loginFailureStore{Store: memory, failure: fmt.Errorf("transaction completion: %w", errors.Join(uncertain, &sessions.Error{Code: code}))}
			bound, err := manager.WithStore(store)
			if err != nil {
				t.Fatal(err)
			}
			var result sessions.Record
			switch operation {
			case "create":
				result, err = bound.Create(t.Context(), nil)
			case "rotate":
				result, err = bound.Rotate(t.Context(), original)
			case "replace":
				result, err = bound.Replace(t.Context(), original, nil)
			}
			if err == nil || !errors.Is(err, uncertain) || store.calls != 1 || result.ID().Valid() {
				t.Fatal("uncertain write was retried, reclassified, or published", store.calls, err)
			}
			after, found, err := memory.Load(t.Context(), original.ID())
			if err != nil || !found || !reflect.DeepEqual(after.Snapshot(), original.Snapshot()) {
				t.Fatal("failed write changed original session", err)
			}
		})
	}
}
