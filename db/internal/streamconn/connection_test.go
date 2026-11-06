package streamconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openOwner(t *testing.T) (*sql.DB, *Owner) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "stream.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := database.ExecContext(t.Context(), "CREATE TABLE entries(id INTEGER); INSERT INTO entries VALUES(1),(2),(3)"); err != nil {
		t.Fatal(err)
	}
	connection, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	owner := New(connection)
	t.Cleanup(func() { _ = owner.Finish() })
	return database, owner
}

func TestStreamConnectionSeparatesSourceRowsCallbackRowsAndRetainedLease(t *testing.T) {
	database, owner := openOwner(t)
	source, _ := owner.Acquire()
	source.SourceRows()
	native, err := source.QueryContext(t.Context(), "SELECT id FROM entries ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	rows := source.Rows(native)
	if !rows.Next() {
		t.Fatal("missing first source row")
	}
	child, _ := owner.Acquire()
	nested, err := child.QueryContext(t.Context(), "SELECT id FROM entries")
	if err != nil {
		t.Fatal(err)
	}
	held := child.Rows(nested)
	if err := owner.FinishReads(); err != nil {
		t.Fatal(err)
	}
	if held.Next() || held.Err() == nil {
		t.Fatal("abandoned callback rows remain usable")
	}
	if !rows.Next() {
		t.Fatal("callback cleanup closed source", rows.Err())
	}
	retained, _ := owner.Acquire()
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Finish(); err != nil {
		t.Fatal(err)
	}
	if lease, err := owner.Acquire(); lease != nil || err != nil {
		t.Fatal("finished scope did not fall back", lease, err)
	}
	blocked, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := database.ExecContext(blocked, "SELECT 1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("retained connection returned to pool", err)
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if err := retained.Close(); err != nil {
		t.Fatal("lease close not idempotent", err)
	}
}

func TestStreamConnectionDiscardsWithLiveSourceWithoutDeadlock(t *testing.T) {
	database, owner := openOwner(t)
	source, _ := owner.Acquire()
	source.SourceRows()
	native, err := source.QueryContext(t.Context(), "SELECT id FROM entries")
	if err != nil {
		t.Fatal(err)
	}
	rows := source.Rows(native)
	if !rows.Next() {
		t.Fatal("missing source")
	}
	transaction, _ := owner.Acquire()
	done := make(chan error, 1)
	go func() { done <- transaction.Raw(func(any) error { return driver.ErrBadConn }) }()
	select {
	case err := <-done:
		if !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("discard deadlocked behind source rows")
	}
	if rows.Next() || rows.Err() == nil {
		t.Fatal("discarded stream still readable")
	}
	if err := owner.Finish(); err == nil {
		t.Fatal("discard was hidden")
	}
	if _, err := database.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal("discarded connection not replaced", err)
	}
}

func TestStreamConnectionFinishAndLeaseCloseRace(t *testing.T) {
	database, owner := openOwner(t)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		workers.Go(func() {
			<-start
			for range 32 {
				lease, err := owner.Acquire()
				if err != nil {
					t.Error(err)
					return
				}
				if lease != nil {
					if err := lease.Close(); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	close(start)
	if err := owner.Finish(); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	if database.Stats().InUse != 0 {
		t.Fatal("live lease after finish")
	}
	if _, err := database.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
}
