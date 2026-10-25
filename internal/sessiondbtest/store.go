// Package sessiondbtest exercises the independent session database through
// native backends and public migrations. It installs no identity model.
package sessiondbtest

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/sessiontest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/sessions/database/models"
)

type Backend interface {
	db.Session
	sessiondb.Backend
	mb.RevisionFencedBackend
}
type Open func(*testing.T) (Backend, func(*testing.T) Backend)

var instant = time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)

func Run(t *testing.T, open Open) {
	newStore := func(t *testing.T) sessions.Store {
		backend, _ := open(t)
		migrate(t, backend)
		return store(t, backend, "contract", sessiondb.Config{})
	}
	t.Run("atomic_access", func(t *testing.T) { sessiontest.AtomicAccess(t, newStore) })
	t.Run("access_validation", func(t *testing.T) { sessiontest.AccessValidation(t, newStore) })
	t.Run("replacement", func(t *testing.T) { sessiontest.Replacement(t, newStore) })
	for _, test := range []struct {
		name string
		run  func(*testing.T, Backend, func(*testing.T) Backend)
	}{
		{"namespace_and_reopen", namespaceAndReopen},
		{"global_capacity", globalCapacity},
		{"batch_rollback", batchRollback},
		{"borrowed_scope", borrowedScope},
		{"read_completion", readCompletion},
		{"write_completion", writeCompletion},
		{"stored_integrity", storedIntegrity},
		{"concurrent_capacity", concurrentCapacity},
	} {
		t.Run(test.name, func(t *testing.T) { backend, reopen := open(t); migrate(t, backend); test.run(t, backend, reopen) })
	}
	t.Run("schema_required", func(t *testing.T) {
		backend, _ := open(t)
		value := store(t, backend, "absent", sessiondb.Config{})
		if created, err := value.Create(t.Context(), record(t, 1, nil, instant)); created || err == nil {
			t.Fatal("store silently provisioned a missing schema")
		}
		if count, err := value.Inspect(t.Context()); count != 0 || err == nil {
			t.Fatal("missing schema passed startup inspection")
		}
	})
}
func migrate(t *testing.T, backend Backend) {
	t.Helper()
	loaded, _, err := definition.Load(sessiondb.MigrationSources()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
}
func store(t *testing.T, backend sessiondb.Backend, namespace string, config sessiondb.Config) *sessiondb.Store {
	t.Helper()
	database, err := sessiondb.New(backend, config)
	if err != nil {
		t.Fatal(err)
	}
	value, err := database.Store(namespace)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func record(t *testing.T, index int, values map[string]string, at time.Time) sessions.Record {
	t.Helper()
	digest := sha256.Sum256([]byte(fmt.Sprintf("native-session-%d", index)))
	id, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	value, err := sessions.RestoreRecord(sessions.RecordSnapshot{ID: id, Values: values, CreatedAt: at, AccessedAt: at, AbsoluteExpiresAt: at.Add(2 * time.Hour), IdleExpiresAt: at.Add(time.Hour)}, sessions.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func create(t *testing.T, store sessions.Store, value sessions.Record) {
	t.Helper()
	if created, err := store.Create(t.Context(), value); err != nil || !created {
		t.Fatal("create", created, err)
	}
}
func requireRecord(t *testing.T, store sessions.Store, value sessions.Record, want bool) {
	t.Helper()
	actual, present, err := store.Load(t.Context(), value.ID())
	if err != nil || present != want || want && !reflect.DeepEqual(actual.Snapshot(), value.Snapshot()) || !want && actual.ID().Valid() {
		t.Fatal("record observation", present, err)
	}
}

func namespaceAndReopen(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	a, b := store(t, backend, "domain-a", sessiondb.Config{}), store(t, backend, "domain-b", sessiondb.Config{})
	one := record(t, 1, map[string]string{"principal": "same-principal"}, instant)
	create(t, a, one)
	if created, err := a.Create(t.Context(), one); err != nil || created {
		t.Fatal("confirmed collision did not preserve original", created, err)
	}
	other, err := one.WithValue("private", "domain-b-only")
	if err != nil {
		t.Fatal(err)
	}
	create(t, b, other)
	rows, err := models.SessionObjects.Using(backend).OrderBy(models.SessionFields.ID.Asc()).All(t.Context())
	if err != nil || len(rows) != 2 || rows[0].Digest == rows[1].Digest {
		t.Fatal("namespace digest isolation", len(rows), err)
	}
	for _, row := range rows {
		if strings.Contains(row.Digest+row.Payload+row.Namespace, one.ID().Encoded()) {
			t.Fatal("raw bearer was persisted")
		}
	}
	requireRecord(t, a, one, true)
	requireRecord(t, b, other, true)
	keep := record(t, 2, map[string]string{"principal": "same-principal"}, instant)
	anonymous := record(t, 3, map[string]string{"note": "anonymous"}, instant)
	foreign := record(t, 4, map[string]string{"principal": "another"}, instant)
	create(t, a, keep)
	create(t, a, anonymous)
	create(t, a, foreign)
	if count, err := a.DeleteMatching(t.Context(), "principal", "same-principal", keep.ID()); err != nil || count != 1 {
		t.Fatal("targeted revoke", count, err)
	}
	requireRecord(t, a, one, false)
	requireRecord(t, b, other, true)
	requireRecord(t, a, keep, true)
	requireRecord(t, a, anonymous, true)
	requireRecord(t, a, foreign, true)
	again := store(t, reopen(t), "domain-a", sessiondb.Config{})
	requireRecord(t, again, keep, true)
	if count, err := again.DeleteAll(t.Context()); err != nil || count != 3 {
		t.Fatal("namespace retirement", count, err)
	}
	requireRecord(t, b, other, true)
	if count, err := b.Inspect(t.Context()); err != nil || count != 1 {
		t.Fatal("complete inventory", count, err)
	}
	database, err := sessiondb.New(reopen(t), sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := database.Inspect(t.Context()); err != nil || count != 1 {
		t.Fatal("namespace-free database inspection", count, err)
	}
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		count, err := database.InspectIn(t.Context(), scope)
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("borrowed database inspection lost foreign namespace")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func globalCapacity(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	a, b := store(t, backend, "a", sessiondb.Config{MaxRecords: 2}), store(t, backend, "b", sessiondb.Config{MaxRecords: 2})
	one, two := record(t, 1, nil, instant), record(t, 2, nil, instant.Add(30*time.Minute))
	create(t, a, one)
	create(t, b, two)
	if created, err := a.Create(t.Context(), record(t, 3, nil, instant)); created || !errors.Is(err, &sessions.Error{Code: sessions.CodeStoreFull}) {
		t.Fatal("capacity was not global", created, err)
	}
	requireRecord(t, a, one, true)
	requireRecord(t, b, two, true)
	later := record(t, 3, nil, instant.Add(time.Hour))
	create(t, b, later)
	requireRecord(t, a, one, false)
	requireRecord(t, b, two, true)
	requireRecord(t, b, later, true)
	if count, err := a.Inspect(t.Context()); err != nil || count != 2 {
		t.Fatal("reclaim changed capacity", count, err)
	}
}

func batchRollback(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	value := store(t, backend, "batch", sessiondb.Config{})
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		bound, err := value.InSession(t.Context(), scope)
		if err != nil {
			return err
		}
		for i := 1; i <= 257; i++ {
			created, err := bound.Create(t.Context(), record(t, i, map[string]string{"principal": "victim"}, instant))
			if err != nil {
				return err
			}
			if !created {
				return errors.New("unexpected fixture collision")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	last, present, err := models.SessionObjects.Using(backend).OrderBy(models.SessionFields.ID.Desc()).First(t.Context())
	if err != nil || !present {
		t.Fatal("last batch fixture", err)
	}
	saved := last.Payload
	if _, err := models.SessionObjects.Patch(t.Context(), backend, last, models.SessionPatch{}.WithPayload("corrupt-last-page")); err != nil {
		t.Fatal(err)
	}
	probe := &faultBackend{Backend: backend, mode: "count_deletes"}
	faulted := store(t, probe, "batch", sessiondb.Config{})
	if count, err := faulted.DeleteMatching(t.Context(), "principal", "victim", sessions.ID{}); count != 0 || err == nil {
		t.Fatal("late corrupt row published partial revoke", count, err)
	}
	if probe.deletes != 256 {
		t.Fatal("late failure did not exercise earlier batch mutations", probe.deletes)
	}
	if count, err := models.SessionObjects.Using(backend).Count(t.Context()); err != nil || count != 257 {
		t.Fatal("earlier batch deletes were not rolled back", count, err)
	}
	if _, err := models.SessionObjects.Patch(t.Context(), backend, last, models.SessionPatch{}.WithPayload(saved)); err != nil {
		t.Fatal(err)
	}
	if count, err := value.DeleteAll(t.Context()); err != nil || count != 257 {
		t.Fatal("complete repaired batch", count, err)
	}
}

func borrowedScope(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	value, original := store(t, backend, "borrowed", sessiondb.Config{}), record(t, 1, nil, instant)
	rollback := errors.New("deliberate owner rollback")
	var retained *sessiondb.Store
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		var err error
		retained, err = value.InSession(t.Context(), scope)
		if err != nil {
			return err
		}
		if created, err := retained.Create(t.Context(), original); err != nil || !created {
			return errors.Join(err, errors.New("borrowed creation failed"))
		}
		if row, present, err := value.LoadIn(t.Context(), scope, original.ID()); err != nil || !present || row.ID() != original.ID() {
			return errors.Join(err, errors.New("same write scope could not read provisional row"))
		}
		return rollback
	}); err != rollback {
		t.Fatal(err)
	}
	requireRecord(t, value, original, false)
	if created, err := retained.Create(t.Context(), original); created || err == nil {
		t.Fatal("expired borrowed store survived rollback")
	}
	create(t, value, original)
	var reader db.Queryer
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		reader = scope
		row, present, err := value.LoadIn(t.Context(), scope, original.ID())
		if err != nil {
			return err
		}
		if !present || row.ID() != original.ID() {
			return errors.New("read-only composition failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if row, present, err := value.LoadIn(t.Context(), reader, original.ID()); row.ID().Valid() || present || err == nil {
		t.Fatal("expired read scope published row")
	}
	if row, present, err := value.LoadIn(t.Context(), backend, original.ID()); row.ID().Valid() || present || err == nil {
		t.Fatal("root reader admitted as borrowed")
	}
}
