package systemstate

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

func TestDurableSessionStoreExplicitMigrationRestartAndBearerFreeRows(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "durable-session.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	firstBackend := openSessionStoreBackend(t, ctx, dataSourceName)
	explicitlyMigrateSystemState(t, ctx, firstBackend)
	firstGate := &sessionStoreTestGate{backend: firstBackend}
	firstStore := mustDurableSessionStore(t, firstGate, 4)

	createdAt := time.Date(2026, time.August, 24, 3, 0, 0, 0, time.UTC)
	oldID := mustCodecSessionID(t, "A")
	oldRecord := mustSessionStoreRecord(t, oldID, map[string]string{
		"principal_id": "admin-principal",
		"auth_marker":  "opaque-auth-value",
	}, createdAt, createdAt, createdAt.Add(24*time.Hour), createdAt.Add(30*time.Minute))
	if created, err := firstStore.Create(ctx, oldRecord); err != nil || !created {
		t.Fatalf("Create(first) = (%v,%v), want true/nil", created, err)
	}
	if created, err := firstStore.Create(ctx, oldRecord); err != nil || created {
		t.Fatalf("Create(duplicate) = (%v,%v), want false/nil", created, err)
	}

	rows, err := listSessionRows(ctx, firstBackend, 2)
	if err != nil || len(rows) != 1 {
		t.Fatalf("listSessionRows() = (%+v,%v), want one row", rows, err)
	}
	wantDigest, err := sessionDigest(oldID)
	if err != nil {
		t.Fatalf("sessionDigest(): %v", err)
	}
	if rows[0].digest != wantDigest || strings.Contains(rows[0].digest, oldID.Encoded()) ||
		strings.Contains(rows[0].payload, oldID.Encoded()) {
		t.Fatalf("stored row contains bearer or wrong digest: digest=%q payload=%q", rows[0].digest, rows[0].payload)
	}
	if got := firstStore.String(); got != "systemstate.SessionStore{redacted}" || strings.Contains(got, oldID.Encoded()) {
		t.Fatalf("SessionStore.String() = %q", got)
	}
	if err := firstBackend.Close(); err != nil {
		t.Fatalf("close first backend: %v", err)
	}

	secondBackend := openSessionStoreBackend(t, ctx, dataSourceName)
	secondGate := &sessionStoreTestGate{backend: secondBackend}
	secondStore := mustDurableSessionStore(t, secondGate, 4)
	loaded, found, err := secondStore.Load(ctx, oldID)
	if err != nil || !found {
		_ = secondBackend.Close()
		t.Fatalf("Load(after reopen) = (%v,%v,%v), want record/true/nil", loaded, found, err)
	}
	if principal, _ := loaded.Value("principal_id"); principal != "admin-principal" {
		_ = secondBackend.Close()
		t.Fatalf("loaded principal = %q", principal)
	}
	touched, found, err := secondStore.Touch(ctx, oldID, createdAt.Add(5*time.Minute), createdAt.Add(35*time.Minute))
	if err != nil || !found || !touched.AccessedAt().Equal(createdAt.Add(5*time.Minute)) ||
		!touched.IdleExpiresAt().Equal(createdAt.Add(35*time.Minute)) {
		_ = secondBackend.Close()
		t.Fatalf("Touch() = (%v,%v,%v), want monotonic persisted touch", touched, found, err)
	}
	if got := secondGate.updates.Load(); got != 1 {
		t.Fatalf("advancing Touch updates = %d, want 1", got)
	}
	// An out-of-order touch cannot move either timestamp backwards.
	regression, found, err := secondStore.Touch(ctx, oldID, createdAt.Add(time.Minute), createdAt.Add(31*time.Minute))
	if err != nil || !found || regression.AccessedAt() != touched.AccessedAt() || regression.IdleExpiresAt() != touched.IdleExpiresAt() {
		_ = secondBackend.Close()
		t.Fatalf("Touch(regression) = (%v,%v,%v), want unchanged timestamps", regression, found, err)
	}
	if got := secondGate.updates.Load(); got != 1 {
		t.Fatalf("nonadvancing Touch total updates = %d, want unchanged 1", got)
	}

	newID := mustCodecSessionID(t, "Q")
	replacement := mustSessionStoreRecord(
		t,
		newID,
		touched.Values(),
		touched.CreatedAt(),
		touched.AccessedAt().Add(time.Minute),
		touched.AbsoluteExpiresAt(),
		touched.IdleExpiresAt().Add(time.Minute),
	)
	if rotated, err := secondStore.Rotate(ctx, oldID, replacement); err != nil || !rotated {
		_ = secondBackend.Close()
		t.Fatalf("Rotate() = (%v,%v), want true/nil", rotated, err)
	}
	if _, found, err := secondStore.Load(ctx, oldID); err != nil || found {
		_ = secondBackend.Close()
		t.Fatalf("Load(old after rotate) = found %v/error %v", found, err)
	}
	if got, found, err := secondStore.Load(ctx, newID); err != nil || !found || got.ID() != newID {
		_ = secondBackend.Close()
		t.Fatalf("Load(new after rotate) = (%v,%v,%v)", got, found, err)
	}
	if err := secondStore.Delete(ctx, newID); err != nil {
		_ = secondBackend.Close()
		t.Fatalf("Delete(new): %v", err)
	}
	if err := secondStore.Delete(ctx, newID); err != nil {
		_ = secondBackend.Close()
		t.Fatalf("Delete(absent idempotent): %v", err)
	}
	if err := secondBackend.Close(); err != nil {
		t.Fatalf("close second backend: %v", err)
	}

	thirdBackend := openSessionStoreBackend(t, ctx, dataSourceName)
	t.Cleanup(func() { _ = thirdBackend.Close() })
	thirdStore := mustDurableSessionStore(t, &sessionStoreTestGate{backend: thirdBackend}, 4)
	if _, found, err := thirdStore.Load(ctx, newID); err != nil || found {
		t.Fatalf("Load(deleted after second reopen) = found %v/error %v", found, err)
	}
	history, err := thirdBackend.ReadAppliedMigrations(ctx)
	if err != nil || len(history) != 1 || history[0].App != InitialMigrationKey().App || history[0].Name != InitialMigrationKey().Name {
		t.Fatalf("migration history after restarts = (%+v,%v)", history, err)
	}
}

func TestDurableSessionStoreCapacityReapAndRotateRollback(t *testing.T) {
	ctx := context.Background()
	backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "capacity.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = backend.Close() })
	explicitlyMigrateSystemState(t, ctx, backend)
	gate := &sessionStoreTestGate{backend: backend}
	store := mustDurableSessionStore(t, gate, 1)

	base := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	expiredID := mustCodecSessionID(t, "A")
	expired := mustSessionStoreRecord(t, expiredID, map[string]string{"state": "expired"}, base, base, base.Add(time.Hour), base.Add(30*time.Minute))
	if created, err := store.Create(ctx, expired); err != nil || !created {
		t.Fatalf("Create(expired candidate) = (%v,%v)", created, err)
	}
	activeID := mustCodecSessionID(t, "Q")
	active := mustSessionStoreRecord(t, activeID, map[string]string{"state": "active"}, base.Add(2*time.Hour), base.Add(2*time.Hour), base.Add(3*time.Hour), base.Add(150*time.Minute))
	if created, err := store.Create(ctx, active); err != nil || !created {
		t.Fatalf("Create(reap then publish) = (%v,%v)", created, err)
	}
	if _, found, err := store.Load(ctx, expiredID); err != nil || found {
		t.Fatalf("Load(reaped) = found %v/error %v", found, err)
	}

	otherID := mustCodecSessionID(t, "g")
	other := mustSessionStoreRecord(t, otherID, map[string]string{"state": "other"}, base.Add(2*time.Hour), base.Add(2*time.Hour), base.Add(4*time.Hour), base.Add(3*time.Hour))
	if created, err := store.Create(ctx, other); err == nil || created {
		t.Fatalf("Create(full) = (%v,%v), want false/store_full", created, err)
	} else {
		var classified *sessions.Error
		if !errors.As(err, &classified) || classified.Code != sessions.CodeStoreFull {
			t.Fatalf("Create(full) error = %#v", err)
		}
	}

	injectedFailure := errors.New("injected replacement insert failure")
	gate.failNextInsert = injectedFailure
	if rotated, err := store.Rotate(ctx, activeID, other); err == nil || rotated || !errors.Is(err, injectedFailure) {
		t.Fatalf("Rotate(injected insert failure) = (%v,%v)", rotated, err)
	}
	if current, found, err := store.Load(ctx, activeID); err != nil || !found || current.ID() != activeID {
		t.Fatalf("Load(old after rollback) = (%v,%v,%v)", current, found, err)
	}
	if _, found, err := store.Load(ctx, otherID); err != nil || found {
		t.Fatalf("Load(replacement after rollback) = found %v/error %v", found, err)
	}
}

func TestDurableSessionStoreCapacityScanFailsClosedBeforeMinimalReap(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)

	t.Run("duplicate digest is never reaped", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "duplicate-reap.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		explicitlyMigrateSystemState(t, ctx, backend)
		store := mustDurableSessionStore(t, &sessionStoreTestGate{backend: backend}, 2)

		duplicateID := mustCodecSessionID(t, "A")
		duplicate := mustSessionStoreRecord(t, duplicateID, map[string]string{"state": "expired"}, base, base, base.Add(time.Hour), base.Add(30*time.Minute))
		digest, _ := sessionDigest(duplicateID)
		payload, _ := encodeSessionPayload(duplicate, sessions.Limits{})
		firstID, err := backend.Insert(ctx, sessionInsertPlan(digest, payload))
		if err != nil {
			t.Fatalf("insert first duplicate: %v", err)
		}
		secondID, err := backend.Insert(ctx, sessionInsertPlan(digest, payload))
		if err != nil {
			t.Fatalf("insert second duplicate: %v", err)
		}

		candidateID := mustCodecSessionID(t, "Q")
		candidate := mustSessionStoreRecord(t, candidateID, nil, base.Add(2*time.Hour), base.Add(2*time.Hour), base.Add(3*time.Hour), base.Add(150*time.Minute))
		created, createErr := store.Create(ctx, candidate)
		var classified *Error
		if created || !errors.As(createErr, &classified) || classified.Code != CodeCardinality {
			t.Fatalf("Create(over duplicate capacity) = (%v,%#v), want false/cardinality", created, createErr)
		}
		if got := mustSessionRowIDs(t, ctx, backend); !slices.Equal(got, []int64{firstID, secondID}) {
			t.Fatalf("rows after duplicate rejection = %v, want unchanged [%d %d]", got, firstID, secondID)
		}
		candidateDigest, _ := sessionDigest(candidateID)
		if _, found, err := loadSessionRow(ctx, backend, candidateDigest); err != nil || found {
			t.Fatalf("candidate after duplicate rejection = found %v/error %v", found, err)
		}
	})

	t.Run("oversized payload is rejected before reap", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "oversize-reap.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		explicitlyMigrateSystemState(t, ctx, backend)
		store := mustDurableSessionStore(t, &sessionStoreTestGate{backend: backend}, 1)

		storedID := mustCodecSessionID(t, "A")
		digest, _ := sessionDigest(storedID)
		rowID, err := backend.Insert(ctx, sessionInsertPlan(digest, strings.Repeat("A", maxSessionPayloadBytes+1)))
		if err != nil {
			t.Fatalf("insert oversized session payload: %v", err)
		}
		candidateID := mustCodecSessionID(t, "Q")
		candidate := mustSessionStoreRecord(t, candidateID, nil, base.Add(2*time.Hour), base.Add(2*time.Hour), base.Add(3*time.Hour), base.Add(150*time.Minute))
		created, createErr := store.Create(ctx, candidate)
		var classified *Error
		if created || !errors.As(createErr, &classified) || classified.Code != CodeCorruptState || classified.Field != "session_payload" {
			t.Fatalf("Create(over oversized payload) = (%v,%#v), want false/corrupt payload", created, createErr)
		}
		if got := mustSessionRowIDs(t, ctx, backend); !slices.Equal(got, []int64{rowID}) {
			t.Fatalf("rows after oversized rejection = %v, want unchanged [%d]", got, rowID)
		}
	})

	t.Run("only lowest id expired row is reaped", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "minimal-reap.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		explicitlyMigrateSystemState(t, ctx, backend)
		store := mustDurableSessionStore(t, &sessionStoreTestGate{backend: backend}, 2)

		firstExpiredID := mustCodecSessionID(t, "A")
		firstExpired := mustSessionStoreRecord(t, firstExpiredID, nil, base, base, base.Add(time.Hour), base.Add(30*time.Minute))
		firstDigest, _ := sessionDigest(firstExpiredID)
		firstPayload, _ := encodeSessionPayload(firstExpired, sessions.Limits{})
		firstRowID, err := backend.Insert(ctx, sessionInsertPlan(firstDigest, firstPayload))
		if err != nil {
			t.Fatalf("insert first expired row: %v", err)
		}

		secondExpiredID := mustCodecSessionID(t, "Q")
		secondExpired := mustSessionStoreRecord(t, secondExpiredID, nil, base, base, base.Add(time.Hour), base.Add(30*time.Minute))
		secondDigest, _ := sessionDigest(secondExpiredID)
		secondPayload, _ := encodeSessionPayload(secondExpired, sessions.Limits{})
		secondRowID, err := backend.Insert(ctx, sessionInsertPlan(secondDigest, secondPayload))
		if err != nil {
			t.Fatalf("insert second expired row: %v", err)
		}
		if firstRowID >= secondRowID {
			t.Fatalf("seed row order = %d/%d, want ascending", firstRowID, secondRowID)
		}

		candidateID := mustCodecSessionID(t, "g")
		candidate := mustSessionStoreRecord(t, candidateID, nil, base.Add(2*time.Hour), base.Add(2*time.Hour), base.Add(3*time.Hour), base.Add(150*time.Minute))
		if created, err := store.Create(ctx, candidate); err != nil || !created {
			t.Fatalf("Create(minimal reap) = (%v,%v)", created, err)
		}
		if _, found, err := store.Load(ctx, firstExpiredID); err != nil || found {
			t.Fatalf("lowest-ID expired row after reap = found %v/error %v", found, err)
		}
		if _, found, err := store.Load(ctx, secondExpiredID); err != nil || !found {
			t.Fatalf("second expired row after minimal reap = found %v/error %v", found, err)
		}
		if _, found, err := store.Load(ctx, candidateID); err != nil || !found {
			t.Fatalf("candidate after minimal reap = found %v/error %v", found, err)
		}
	})
}

func TestSessionCapacityScansKeepPayloadOutOfInventoryAndSortByPrimaryKey(t *testing.T) {
	ctx := context.Background()
	backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "stream-shape.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = backend.Close() })
	explicitlyMigrateSystemState(t, ctx, backend)

	now := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	id := mustCodecSessionID(t, "A")
	record := mustSessionStoreRecord(t, id, map[string]string{"state": "bounded"}, now, now, now.Add(time.Hour), now.Add(30*time.Minute))
	digest, _ := sessionDigest(id)
	payload, _ := encodeSessionPayload(record, sessions.Limits{})
	if _, err := backend.Insert(ctx, sessionInsertPlan(digest, payload)); err != nil {
		t.Fatalf("insert stream shape seed: %v", err)
	}

	observed := &sessionPlanObserver{Queryer: backend}
	if count, err := scanSessionInventory(ctx, observed, 3); err != nil || count != 1 {
		t.Fatalf("scanSessionInventory() = (%d,%v), want 1/nil", count, err)
	}
	if len(observed.plans) != 1 || !slices.Equal(observed.plans[0].SourceFields(), []query.FieldRef{
		systemRowIDField,
		sessionDigestField,
	}) || !slices.Equal(observed.plans[0].Orderings(), []query.Ordering{
		query.NewOrdering(sessionDigestField, query.Ascending),
		query.NewOrdering(systemRowIDField, query.Ascending),
	}) {
		t.Fatalf("inventory plan = %#v, want digest-only ordered stream", observed.plans)
	}

	observed.plans = nil
	if count, err := scanSessionPayloads(ctx, observed, sessions.Limits{}, 3, nil); err != nil || count != 1 {
		t.Fatalf("scanSessionPayloads() = (%d,%v), want 1/nil", count, err)
	}
	if len(observed.plans) != 1 || !slices.Equal(observed.plans[0].SourceFields(), []query.FieldRef{
		systemRowIDField,
		sessionDigestField,
		sessionPayloadField,
	}) || !slices.Equal(observed.plans[0].Orderings(), []query.Ordering{
		query.NewOrdering(systemRowIDField, query.Ascending),
	}) {
		t.Fatalf("payload plan = %#v, want primary-key ordered stream", observed.plans)
	}
}

func TestDurableSessionStoreFailsClosedOnDuplicateCorruptMissingAndUnknownCommit(t *testing.T) {
	ctx := context.Background()
	t.Run("duplicate and corrupt", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "corrupt.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		explicitlyMigrateSystemState(t, ctx, backend)
		store := mustDurableSessionStore(t, &sessionStoreTestGate{backend: backend}, 4)
		id := mustCodecSessionID(t, "A")
		record := mustSessionStoreRecord(t, id, map[string]string{"principal_id": "secret-marker"}, time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC))
		digest, _ := sessionDigest(id)
		payload, _ := encodeSessionPayload(record, sessions.Limits{})
		firstID, err := backend.Insert(ctx, sessionInsertPlan(digest, payload))
		if err != nil {
			t.Fatalf("insert first duplicate seed: %v", err)
		}
		secondID, err := backend.Insert(ctx, sessionInsertPlan(digest, payload))
		if err != nil {
			t.Fatalf("insert second duplicate seed: %v", err)
		}
		if _, found, err := store.Load(ctx, id); err == nil || found {
			t.Fatalf("Load(duplicate) = found %v/error %v", found, err)
		} else {
			var classified *Error
			if !errors.As(err, &classified) || classified.Code != CodeCardinality {
				t.Fatalf("Load(duplicate) error = %#v", err)
			}
			if strings.Contains(err.Error(), id.Encoded()) || strings.Contains(err.Error(), "secret-marker") {
				t.Fatalf("duplicate error leaked secret: %q", err)
			}
		}
		for _, rowID := range []int64{firstID, secondID} {
			if affected, err := backend.Delete(ctx, query.NewDeletePlan(sessionTableName, systemRowIDField, query.Integer(rowID))); err != nil || affected != 1 {
				t.Fatalf("delete duplicate seed %d = (%d,%v)", rowID, affected, err)
			}
		}
		if _, err := backend.Insert(ctx, sessionInsertPlan(digest, "v9.secret-marker")); err != nil {
			t.Fatalf("insert corrupt seed: %v", err)
		}
		if _, found, err := store.Load(ctx, id); err == nil || found {
			t.Fatalf("Load(corrupt) = found %v/error %v", found, err)
		} else {
			var classified *Error
			if !errors.As(err, &classified) || classified.Code != CodeCorruptState || strings.Contains(err.Error(), "secret-marker") {
				t.Fatalf("Load(corrupt) error = %#v", err)
			}
		}
	})

	t.Run("missing schema stays missing", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "missing.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		store := mustDurableSessionStore(t, &sessionStoreTestGate{backend: backend}, 4)
		id := mustCodecSessionID(t, "A")
		if _, found, err := store.Load(ctx, id); err == nil || found {
			t.Fatalf("Load(missing schema) = found %v/error %v", found, err)
		}
		history, err := backend.ReadAppliedMigrations(ctx)
		if err != nil || len(history) != 0 {
			t.Fatalf("history after missing-schema load = (%+v,%v)", history, err)
		}
		rows, queryErr := backend.Query(ctx, query.NewPlan(sessionTableName, []query.FieldRef{systemRowIDField}))
		if rows != nil {
			_ = rows.Close()
		}
		var classified *query.Error
		if !errors.As(queryErr, &classified) || classified.Code != query.CodeMissingTable {
			t.Fatalf("query after missing-schema load = %v, want missing table", queryErr)
		}
	})

	t.Run("commit unknown is not retried", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "commit-unknown.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		explicitlyMigrateSystemState(t, ctx, backend)
		gate := &sessionStoreUnknownGate{backend: backend}
		store := mustDurableSessionStore(t, gate, 4)
		id := mustCodecSessionID(t, "A")
		now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
		record := mustSessionStoreRecord(t, id, nil, now, now, now.Add(time.Hour), now.Add(30*time.Minute))
		if created, err := store.Create(ctx, record); err == nil || created {
			t.Fatalf("Create(commit unknown) = (%v,%v)", created, err)
		} else {
			var classified *query.Error
			if !errors.As(err, &classified) || classified.Code != query.CodeCommitOutcomeUnknown {
				t.Fatalf("Create(commit unknown) error = %#v", err)
			}
		}
		if got := gate.calls.Load(); got != 1 {
			t.Fatalf("withAtomic calls = %d, want exactly 1", got)
		}
		if got := gate.callbacks.Load(); got != 1 {
			t.Fatalf("atomic callback calls = %d, want exactly 1", got)
		}
		digest, _ := sessionDigest(id)
		row, found, err := loadSessionRow(ctx, backend, digest)
		if err != nil || !found {
			t.Fatalf("row after commit-unknown = (%+v,%v,%v), want committed physical row", row, found, err)
		}
		if _, err := decodeSessionPayload(row.payload, id, sessions.Limits{}); err != nil {
			t.Fatalf("decode row after commit-unknown: %v", err)
		}
	})
}

func TestDurableSessionStoreConcurrentDuplicateCreatePublishesOnce(t *testing.T) {
	ctx := context.Background()
	backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "concurrent.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = backend.Close() })
	explicitlyMigrateSystemState(t, ctx, backend)
	store := mustDurableSessionStore(t, &sessionStoreTestGate{backend: backend}, 8)
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	record := mustSessionStoreRecord(t, mustCodecSessionID(t, "A"), map[string]string{"principal": "admin"}, now, now, now.Add(time.Hour), now.Add(30*time.Minute))

	var created atomic.Int64
	var failures atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			published, err := store.Create(ctx, record)
			if err != nil {
				failures.Add(1)
				return
			}
			if published {
				created.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := failures.Load(); got != 0 {
		t.Fatalf("concurrent Create failures = %d", got)
	}
	if got := created.Load(); got != 1 {
		t.Fatalf("concurrent Create published = %d, want 1", got)
	}
	rows, err := listSessionRows(ctx, backend, 2)
	if err != nil || len(rows) != 1 {
		t.Fatalf("concurrent stored rows = (%d,%v), want 1", len(rows), err)
	}
}

type sessionStoreTestGate struct {
	mu             sync.Mutex
	backend        *sqlite.Backend
	failNextInsert error
	updates        atomic.Int64
}

func (gate *sessionStoreTestGate) withAtomic(ctx context.Context, callback func(db.Session) error) error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	failure := gate.failNextInsert
	gate.failNextInsert = nil
	return gate.backend.Atomic(ctx, func(session db.Session) error {
		return callback(&sessionStoreFaultSession{Session: session, failInsert: failure, updates: &gate.updates})
	})
}

type sessionStoreFaultSession struct {
	db.Session
	failInsert error
	updates    *atomic.Int64
}

func (session *sessionStoreFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if session.failInsert != nil {
		err := session.failInsert
		session.failInsert = nil
		return 0, err
	}
	return session.Session.Insert(ctx, plan)
}

func (session *sessionStoreFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if session.updates != nil {
		session.updates.Add(1)
	}
	return session.Session.Update(ctx, plan)
}

type sessionStoreUnknownGate struct {
	backend   *sqlite.Backend
	calls     atomic.Int64
	callbacks atomic.Int64
}

type sessionPlanObserver struct {
	db.Queryer
	plans []query.Plan
}

func (observer *sessionPlanObserver) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	observer.plans = append(observer.plans, plan)
	return observer.Queryer.Query(ctx, plan)
}

func (gate *sessionStoreUnknownGate) withAtomic(ctx context.Context, callback func(db.Session) error) error {
	gate.calls.Add(1)
	if err := gate.backend.Atomic(ctx, func(session db.Session) error {
		gate.callbacks.Add(1)
		return callback(session)
	}); err != nil {
		return err
	}
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeCommitOutcomeUnknown,
		Detail:   "injected unknown commit outcome",
	}
}

func mustSessionRowIDs(t *testing.T, ctx context.Context, queryer db.Queryer) []int64 {
	t.Helper()
	plan, err := query.NewPlan(sessionTableName, []query.FieldRef{systemRowIDField}).WithLimit(16)
	if err != nil {
		t.Fatalf("build session row ID query: %v", err)
	}
	plan = plan.WithOrderings(query.NewOrdering(systemRowIDField, query.Ascending))
	rows, err := queryer.Query(ctx, plan)
	if err != nil {
		t.Fatalf("query session row IDs: %v", err)
	}
	if rows == nil {
		t.Fatal("query session row IDs returned nil rows")
	}
	var identifiers []int64
	for rows.Next() {
		var identifier int64
		if err := rows.Scan(&identifier); err != nil {
			_ = rows.Close()
			t.Fatalf("scan session row ID: %v", err)
		}
		identifiers = append(identifiers, identifier)
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil || closeErr != nil {
		t.Fatalf("iterate session row IDs: %v", errors.Join(iterationErr, closeErr))
	}
	return identifiers
}

func openSessionStoreBackend(t *testing.T, ctx context.Context, dataSourceName string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dataSourceName, err)
	}
	return backend
}

func explicitlyMigrateSystemState(t *testing.T, ctx context.Context, backend *sqlite.Backend) {
	t.Helper()
	loaded, _ := loadInitialDefinition(t)
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatalf("explicit system-state migrate: %v", err)
	}
}

func mustDurableSessionStore(t *testing.T, gate atomicGate, maxRecords int) *durableSessionStore {
	t.Helper()
	store, err := newDurableSessionStore(gate, sessions.Limits{}, maxRecords)
	if err != nil {
		t.Fatalf("newDurableSessionStore(): %v", err)
	}
	return store
}

func mustSessionStoreRecord(
	t *testing.T,
	id sessions.ID,
	values map[string]string,
	createdAt time.Time,
	accessedAt time.Time,
	absoluteExpiresAt time.Time,
	idleExpiresAt time.Time,
) sessions.Record {
	t.Helper()
	record, err := sessions.RestoreRecord(sessions.RecordSnapshot{
		ID:                id,
		Values:            values,
		CreatedAt:         createdAt,
		AccessedAt:        accessedAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
		IdleExpiresAt:     idleExpiresAt,
	}, sessions.Limits{})
	if err != nil {
		t.Fatalf("sessions.RestoreRecord(): %v", err)
	}
	return record
}
