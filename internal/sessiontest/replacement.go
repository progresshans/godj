package sessiontest

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

// Replacement exercises the same fresh-lifetime contract on each real store.
func Replacement(t *testing.T, newStore func(*testing.T) sessions.Store) {
	t.Helper()
	for _, mode := range []string{"fresh", "collision", "missing", "expired", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			store := newStore(t)
			now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
			entropy := bytes.Join([][]byte{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)}, nil)
			manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return now }, Random: bytes.NewReader(entropy), AbsoluteLifetime: time.Minute, IdleTimeout: 10 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			old, err := manager.Create(t.Context(), map[string]string{"private_old_owner": "must disappear"})
			if err != nil {
				t.Fatal(err)
			}
			blocker, err := manager.Create(t.Context(), map[string]string{"owner": "collision row"})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "collision" {
				if err := store.Delete(t.Context(), blocker.ID()); err != nil {
					t.Fatal(err)
				}
			}
			now = now.Add(5 * time.Second)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "missing":
				if err := store.Delete(ctx, old.ID()); err != nil {
					t.Fatal(err)
				}
			case "expired":
				now = now.Add(5 * time.Second)
			case "canceled":
				cancel()
			}
			result, err := manager.Replace(ctx, old, map[string]string{"owner": "new identity"})
			if mode == "fresh" || mode == "collision" {
				if err != nil || result.ID() == old.ID() || !result.CreatedAt().Equal(now) || !result.AccessedAt().Equal(now) || !result.AbsoluteExpiresAt().Equal(now.Add(time.Minute)) || !result.IdleExpiresAt().Equal(now.Add(10*time.Second)) {
					t.Fatal("replacement did not create its own lifetime", err)
				}
				if !reflect.DeepEqual(result.Values(), map[string]string{"owner": "new identity"}) {
					t.Fatal("replacement retained another identity's data")
				}
				persisted, found, err := store.Load(t.Context(), result.ID())
				if err != nil || !found || !reflect.DeepEqual(persisted.Snapshot(), result.Snapshot()) {
					t.Fatal("replacement result differs from durable state", err)
				}
			} else {
				if err == nil || result.ID().Valid() {
					t.Fatal("failed replacement published a session")
				}
				if mode == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal("replacement cancellation lost")
				}
			}
			retained, found, err := store.Load(t.Context(), old.ID())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				if !found || !reflect.DeepEqual(retained.Snapshot(), old.Snapshot()) {
					t.Fatal("canceled replacement changed old session")
				}
			} else if found {
				t.Fatal("replacement left the old identifier active")
			}
			if mode == "collision" {
				current, found, err := store.Load(t.Context(), blocker.ID())
				if err != nil || !found || !reflect.DeepEqual(current.Snapshot(), blocker.Snapshot()) {
					t.Fatal("collision retry damaged another session", err)
				}
			}
		})
	}
}
