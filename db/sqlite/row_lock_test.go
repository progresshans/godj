package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestSQLiteExplicitRowLocksAreUnsupportedInEveryScope(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	base := query.NewPlan("missing_lock_table", []query.FieldRef{id})
	check := func(reader db.Queryer) error {
		for _, strength := range []query.LockStrength{query.LockForUpdate, query.LockForNoKeyUpdate} {
			for _, wait := range []query.LockWaitPolicy{query.LockWait, query.LockNoWait, query.LockSkipLocked} {
				lock, err := query.NewRowLock(strength, wait, query.LockSelf())
				if err != nil {
					return err
				}
				plan, err := base.WithRowLock(lock)
				if err != nil {
					return err
				}
				for _, empty := range []bool{false, true} {
					if empty {
						plan, err = plan.WithLimit(0)
						if err != nil {
							return err
						}
					}
					rows, err := reader.Query(t.Context(), plan)
					if rows != nil {
						_ = rows.Close()
						t.Fatal("unsupported lock returned rows")
					}
					if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
						t.Fatalf("SQLite row lock %s/%s empty=%v = %v", strength, wait, empty, err)
					}
				}
			}
		}
		return nil
	}
	if err := check(backend); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []func(context.Context, func(db.Session) error) error{backend.Atomic, backend.CoordinatedAtomic, func(ctx context.Context, callback func(db.Session) error) error {
		return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
	}, func(ctx context.Context, callback func(db.Session) error) error {
		return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
	}} {
		var expired db.ReadModifyWriteSession
		if err := owner(t.Context(), func(session db.Session) error {
			if policy, err := session.(db.ReadModifyWriteSession).ReadModifyWritePolicy(t.Context()); err != nil || policy != db.ReadModifyWriteConflict {
				t.Fatalf("native SQLite transaction policy %q: %v", policy, err)
			}
			if err := check(session); err != nil {
				return err
			}
			return db.WithSavepoint(t.Context(), session, func(child db.Session) error {
				expired = child.(db.ReadModifyWriteSession)
				if policy, err := expired.ReadModifyWritePolicy(t.Context()); err != nil || policy != db.ReadModifyWriteConflict {
					t.Fatalf("native SQLite child policy %q: %v", policy, err)
				}
				return check(child)
			})
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := expired.ReadModifyWritePolicy(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan, Category: query.CategoryBackend}) {
			t.Fatal("expired SQLite session advertised a write policy", err)
		}
	}
	if err := backend.ReadSnapshot(t.Context(), check); err != nil {
		t.Fatal(err)
	}
	lock, err := query.NewRowLock(query.LockForUpdate, query.LockWait)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := base.WithRowLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	count, err := plan.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Compile(count); err != nil {
		t.Fatalf("count must remove explicit row locks: %v", err)
	}
}
