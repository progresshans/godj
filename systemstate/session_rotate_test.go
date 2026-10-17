package systemstate

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func TestDurableSessionStoreRotateCollisionAndExpiredDeletion(t *testing.T) {
	ctx := context.Background()
	backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "rotate-contract.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = backend.Close() })
	explicitlyMigrateSystemState(t, ctx, backend)
	store := mustDurableSessionStore(t, &sessionStoreTestGate{backend: backend}, 4)
	base := time.Date(2026, time.August, 25, 4, 0, 0, 0, time.UTC)

	oldID := mustCodecSessionID(t, "A")
	old := mustSessionStoreRecord(
		t,
		oldID,
		map[string]string{"owner": "old"},
		base,
		base,
		base.Add(2*time.Hour),
		base.Add(30*time.Minute),
	)
	collisionID := mustCodecSessionID(t, "Q")
	collision := mustSessionStoreRecord(
		t,
		collisionID,
		map[string]string{"owner": "collision"},
		base,
		base,
		base.Add(2*time.Hour),
		base.Add(30*time.Minute),
	)
	for _, record := range []sessions.Record{old, collision} {
		if created, err := store.Create(ctx, record); err != nil || !created {
			t.Fatalf("Create rotate collision fixture = (%v,%v)", created, err)
		}
	}
	collidingReplacement := mustSessionStoreRecord(
		t,
		collisionID,
		map[string]string{"owner": "replacement"},
		old.CreatedAt(),
		base.Add(time.Minute),
		old.AbsoluteExpiresAt(),
		base.Add(31*time.Minute),
	)
	published, rotated, err := store.Rotate(ctx, oldID, collidingReplacement)
	if !errors.Is(err, &sessions.Error{Code: sessions.CodeEntropy}) || rotated || published.ID().Valid() {
		t.Fatalf("Rotate collision = (%v,%v,%v), want zero/false/entropy", published, rotated, err)
	}
	for _, fixture := range []struct {
		record sessions.Record
		owner  string
	}{
		{record: old, owner: "old"},
		{record: collision, owner: "collision"},
	} {
		stored, found, loadErr := store.Load(ctx, fixture.record.ID())
		if loadErr != nil || !found {
			t.Fatalf("Load after Rotate collision = (%v,%v,%v)", stored, found, loadErr)
		}
		if owner, _ := stored.Value("owner"); owner != fixture.owner {
			t.Fatalf("Rotate collision changed %q row to owner %q", fixture.owner, owner)
		}
	}
	successID := mustCodecSessionID(t, "w")
	successReplacement := mustSessionStoreRecord(
		t,
		successID,
		map[string]string{"owner": "published"},
		old.CreatedAt(),
		base.Add(5*time.Minute),
		old.AbsoluteExpiresAt(),
		base.Add(35*time.Minute),
	)
	published, rotated, err = store.Rotate(ctx, oldID, successReplacement)
	if err != nil || !rotated {
		t.Fatalf("Rotate after collision = (%v,%v,%v)", published, rotated, err)
	}
	persisted, found, err := store.Load(ctx, successID)
	if err != nil || !found || !reflect.DeepEqual(published.Snapshot(), persisted.Snapshot()) {
		t.Fatalf("returned and persisted durable Rotate records differ = (%v,%v,%v)", persisted, found, err)
	}
	if _, found, err := store.Load(ctx, oldID); err != nil || found {
		t.Fatalf("old ID after successful Rotate = found %v/error %v", found, err)
	}

	expiredID := mustCodecSessionID(t, "g")
	expired := mustSessionStoreRecord(
		t,
		expiredID,
		map[string]string{"owner": "expired"},
		base,
		base,
		base.Add(2*time.Hour),
		base.Add(10*time.Minute),
	)
	if created, err := store.Create(ctx, expired); err != nil || !created {
		t.Fatalf("Create expired rotate fixture = (%v,%v)", created, err)
	}
	replacementID := successID
	expiredReplacement := mustSessionStoreRecord(
		t,
		replacementID,
		map[string]string{"owner": "replacement"},
		expired.CreatedAt(),
		base.Add(11*time.Minute),
		expired.AbsoluteExpiresAt(),
		base.Add(41*time.Minute),
	)
	published, rotated, err = store.Rotate(ctx, expiredID, expiredReplacement)
	if err != nil || rotated || published.ID().Valid() {
		t.Fatalf("Rotate expired = (%v,%v,%v), want zero/false/nil", published, rotated, err)
	}
	if _, found, err := store.Load(ctx, expiredID); err != nil || found {
		t.Fatalf("expired old ID after Rotate = found %v/error %v", found, err)
	}
	remainingCollision, found, err := store.Load(ctx, replacementID)
	if err != nil || !found || !reflect.DeepEqual(persisted.Snapshot(), remainingCollision.Snapshot()) {
		t.Fatalf("expired Rotate changed colliding replacement row = (%v,%v,%v)", remainingCollision, found, err)
	}
	rows, err := listSessionRows(ctx, backend, 4)
	if err != nil || len(rows) != 2 {
		t.Fatalf("durable rows after collision/expired Rotate = (%d,%v), want 2", len(rows), err)
	}
}
