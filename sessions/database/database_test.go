package database_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

type untouchedBackend struct {
	db.Session
	reads, writes int
}

func (backend *untouchedBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	backend.reads++
	return errors.New("unexpected read")
}
func (backend *untouchedBackend) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	backend.writes++
	return nil
}

type expiringSession struct {
	db.Session
	err error
}

func (scope *expiringSession) ValidateSession(context.Context) error { return scope.err }

func TestDatabaseSessionConfigurationAndInputsArePure(t *testing.T) {
	owner := &untouchedBackend{}
	for _, backend := range []sessiondb.Backend{nil, (*untouchedBackend)(nil)} {
		if value, err := sessiondb.New(backend, sessiondb.Config{}); err == nil || value != nil {
			t.Fatal("nil backend admitted")
		}
	}
	for _, config := range []sessiondb.Config{{MaxRecords: -1}, {MaxRecords: sessiondb.MaximumRecords + 1}, {Limits: sessions.Limits{MaxValues: 33}}, {Limits: sessions.Limits{MaxTotalBytes: -1}}} {
		if value, err := sessiondb.New(owner, config); err == nil || value != nil {
			t.Fatal("invalid storage limits admitted")
		}
	}
	database, err := sessiondb.New(owner, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "bad\x00namespace", string([]byte{0xff}), strings.Repeat("n", 129)} {
		if value, err := database.Store(name); err == nil || value != nil {
			t.Fatal("invalid namespace admitted")
		}
	}
	store, err := database.Store("private-namespace-marker")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{database, *database, store, *store} {
		for _, format := range []string{"%v", "%+v", "%#v", "%p", "%w", "%x"} {
			if strings.Contains(fmt.Sprintf(format, value), "private-namespace-marker") {
				t.Fatal("database diagnostic leaked namespace")
			}
		}
	}
	for _, ctx := range []context.Context{nil} {
		if _, _, err := store.Load(ctx, sessions.ID{}); err == nil {
			t.Fatal("nil context admitted")
		}
	}
	if _, _, err := store.Load(t.Context(), sessions.ID{}); err == nil {
		t.Fatal("zero bearer admitted")
	}
	if _, err := store.Create(t.Context(), sessions.Record{}); err == nil {
		t.Fatal("zero session admitted")
	}
	if _, err := store.DeleteMatching(t.Context(), "", "value", sessions.ID{}); err == nil {
		t.Fatal("invalid matching key admitted")
	}
	for _, scope := range []db.Session{nil, (*expiringSession)(nil), owner, &expiringSession{err: context.Canceled}} {
		if value, err := store.InSession(t.Context(), scope); err == nil || value != nil {
			t.Fatal("root/nil/expired scope admitted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	scope := &expiringSession{}
	borrowed, err := store.InSession(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := borrowed.InSession(ctx, scope); err == nil {
		t.Fatal("borrowed store rebound to another scope")
	}
	cancel()
	if _, err := borrowed.DeleteAll(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatal("retained borrowed lifetime accepted", err)
	}
	if owner.reads != 0 || owner.writes != 0 {
		t.Fatal("invalid config/input performed I/O")
	}
	// A structurally valid record reaches the owner, but a skipped callback
	// cannot publish creation or become a retryable ordinary collision.
	id, err := sessions.ParseID(strings.Repeat("A", 43))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	record, err := sessions.RestoreRecord(sessions.RecordSnapshot{ID: id, CreatedAt: at, AccessedAt: at, AbsoluteExpiresAt: at.Add(time.Hour), IdleExpiresAt: at.Add(time.Minute)}, sessions.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if created, err := store.Create(t.Context(), record); created || !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || owner.writes != 1 {
		t.Fatal("skipped owner reported session creation", created, err)
	}
}
