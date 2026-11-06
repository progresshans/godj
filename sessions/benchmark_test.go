package sessions_test

import (
	"context"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func BenchmarkActiveSessionLoad(b *testing.B) {
	store, err := sessions.NewMemoryStore(8)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return now }})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	record, err := manager.Create(ctx, map[string]string{"principal_id": "operator", "locale": "ko", "theme": "dark"})
	if err != nil {
		b.Fatal(err)
	}
	now = now.Add(time.Minute)
	b.ReportAllocs()
	for b.Loop() {
		if _, found, err := manager.Load(ctx, record.ID()); err != nil || !found {
			b.Fatalf("load: found=%v error=%v", found, err)
		}
	}
}
