// Package savepointtest checks the public nested-session contract against each
// native backend. Native tables and their real constraints are installed by the
// backend test; no scope implementation or generated expected result is used.
package savepointtest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type Owner func(context.Context, func(db.Session) error) error

var (
	id     = query.NewFieldRef("id", "id", query.FieldInteger, false)
	name   = query.NewFieldRef("name", "name", query.FieldString, false)
	parent = query.NewFieldRef("parent_id", "parent_id", query.FieldInteger, true)
)

// Check requires savepoint_items(id generated primary key, name unique not
// null, parent_id nullable self-reference). Each subtest uses distinct names.
func Check(t *testing.T, root db.Queryer, owner Owner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	t.Run("nested_unique_failure_parent_commit", func(t *testing.T) {
		prefix := t.Name() + "/"
		rollback := errors.New("undo grandchild")
		var retained []db.Session
		err := owner(ctx, func(outer db.Session) error {
			retained = append(retained, outer)
			_, outerRelation := outer.(db.RelationSession)
			if _, err := insert(ctx, outer, prefix+"before"); err != nil {
				return err
			}
			if err := db.WithSavepoint(ctx, outer, func(child db.Session) error {
				retained = append(retained, child)
				if _, related := child.(db.RelationSession); related != outerRelation {
					return errors.New("savepoint changed relation capability")
				}
				if _, ok := child.(db.BatchQueryer); !ok {
					return errors.New("savepoint lost batch capability")
				}
				if _, ok := child.(db.ConflictInserter); !ok {
					return errors.New("savepoint lost conflict-insert capability")
				}
				if _, err := insert(ctx, outer, prefix+"escaped_parent"); !invalid(err) {
					return fmt.Errorf("parent write during child: %v", err)
				}
				if _, err := insert(ctx, child, prefix+"child"); err != nil {
					return err
				}
				if err := db.WithSavepoint(ctx, child, func(grandchild db.Session) error {
					retained = append(retained, grandchild)
					if _, err := insert(ctx, grandchild, prefix+"grandchild"); err != nil {
						return err
					}
					return rollback
				}); err != rollback {
					return fmt.Errorf("grandchild rollback: %w", err)
				}
				return assertNames(ctx, child, prefix, "before", "child")
			}); err != nil {
				return err
			}
			// PostgreSQL remains usable only if the unique failure's native
			// aborted subtransaction was actually rolled back first.
			if err := db.WithSavepoint(ctx, outer, func(child db.Session) error {
				_, err := insert(ctx, child, prefix+"before")
				return err
			}); !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
				return fmt.Errorf("unique child failure: %v", err)
			}
			if _, err := insert(ctx, outer, prefix+"after"); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := assertNames(ctx, root, prefix, "after", "before", "child"); err != nil {
			t.Fatal(err)
		}
		for _, session := range retained {
			assertExpired(t, ctx, session)
		}
	})
	t.Run("released_child_is_undone_by_parent_rollback", func(t *testing.T) {
		prefix := t.Name() + "/"
		rollback := errors.New("undo parent")
		err := owner(ctx, func(outer db.Session) error {
			if _, err := insert(ctx, outer, prefix+"parent"); err != nil {
				return err
			}
			if err := db.WithSavepoint(ctx, outer, func(child db.Session) error { _, err := insert(ctx, child, prefix+"released"); return err }); err != nil {
				return err
			}
			if err := assertNames(ctx, outer, prefix, "parent", "released"); err != nil {
				return err
			}
			return rollback
		})
		if err != rollback {
			t.Fatalf("parent rollback: %v", err)
		}
		if err := assertNames(ctx, root, prefix); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("child_panic_rollback_preserves_parent", func(t *testing.T) {
		prefix := t.Name() + "/"
		marker := errors.New("original child panic")
		err := owner(ctx, func(outer db.Session) error {
			if _, err := insert(ctx, outer, prefix+"before"); err != nil {
				return err
			}
			var recovered any
			func() {
				defer func() {
					if value := recover(); value != nil {
						recovered = value
					}
				}()
				err := db.WithSavepoint(ctx, outer, func(child db.Session) error {
					if _, err := insert(ctx, child, prefix+"panicked"); err != nil {
						return err
					}
					panic(marker)
				})
				if err != nil {
					recovered = err
				}
			}()
			if recovered != marker {
				return fmt.Errorf("child panic changed: %v", recovered)
			}
			if _, err := insert(ctx, outer, prefix+"after"); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := assertNames(ctx, root, prefix, "after", "before"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("child_goexit_rolls_back_both_scopes", func(t *testing.T) {
		prefix := t.Name() + "/"
		done := make(chan struct{})
		returned := false
		var result error
		go func() {
			defer close(done)
			result = owner(ctx, func(outer db.Session) error {
				if _, err := insert(ctx, outer, prefix+"parent"); err != nil {
					return err
				}
				return db.WithSavepoint(ctx, outer, func(child db.Session) error {
					if _, err := insert(ctx, child, prefix+"child"); err != nil {
						return err
					}
					runtime.Goexit()
					return nil
				})
			})
			returned = true
		}()
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("Goexit cleanup did not complete")
		}
		if returned || result != nil {
			t.Fatalf("Goexit returned: %v/%v", returned, result)
		}
		if err := assertNames(ctx, root, prefix); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("child_cancellation_and_unclosed_rows", func(t *testing.T) {
		prefix := t.Name() + "/"
		var retained db.Rows
		err := owner(ctx, func(outer db.Session) error {
			childContext, cancelChild := context.WithCancel(ctx)
			defer cancelChild()
			if err := db.WithSavepoint(childContext, outer, func(child db.Session) error {
				if _, err := insert(ctx, child, prefix+"canceled"); err != nil {
					return err
				}
				cancelChild()
				return nil
			}); !errors.Is(err, context.Canceled) || errors.Is(err, &query.Error{Code: query.CodeTransactionRollbackRequired}) {
				return fmt.Errorf("child cancellation: %v", err)
			}
			if _, err := insert(ctx, outer, prefix+"survivor"); err != nil {
				return err
			}
			if err := db.WithSavepoint(ctx, outer, func(child db.Session) error {
				var err error
				retained, err = child.Query(ctx, namesPlan(prefix))
				if err != nil {
					return err
				}
				if !retained.Next() {
					return errors.New("child cursor was empty")
				}
				return nil // Scope must close it before RELEASE.
			}); err != nil {
				return err
			}
			if retained.Next() || !invalid(retained.Err()) {
				return errors.New("child rowset escaped its callback")
			}
			if err := retained.Close(); err != nil {
				return err
			}
			return assertNames(ctx, outer, prefix, "survivor")
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := assertNames(ctx, root, prefix, "survivor"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cursor_and_stream_ownership", func(t *testing.T) {
		prefix := t.Name() + "/"
		err := owner(ctx, func(outer db.Session) error {
			for _, suffix := range []string{"a", "b", "c"} {
				if _, err := insert(ctx, outer, prefix+suffix); err != nil {
					return err
				}
			}
			rows, err := outer.Query(ctx, namesPlan(prefix))
			if err != nil {
				return err
			}
			calls := 0
			err = db.WithSavepoint(ctx, outer, func(db.Session) error { calls++; return nil })
			closeErr := rows.Close()
			if !invalid(err) || calls != 0 || closeErr != nil {
				return fmt.Errorf("parent cursor crossing: %v/%d/%v", err, calls, closeErr)
			}
			return db.WithSavepoint(ctx, outer, func(child db.Session) error {
				stream := child.(db.BatchQueryer)
				var names []string
				err := stream.QueryBatches(ctx, namesPlan(prefix), 1, func(row db.Row) error {
					var value string
					if err := row.Scan(&value); err != nil {
						return err
					}
					names = append(names, value)
					return nil
				}, func(affinity db.Queryer) (bool, error) {
					if affinity != child {
						return false, errors.New("stream changed its borrowed handle")
					}
					if err := db.WithSavepoint(ctx, child, func(db.Session) error { calls++; return nil }); !invalid(err) {
						return false, fmt.Errorf("live stream crossed savepoint: %v", err)
					}
					return true, assertNames(ctx, child, prefix, "a", "b", "c")
				})
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(names, []string{prefix + "a", prefix + "b", prefix + "c"}) || calls != 0 {
					return fmt.Errorf("stream results = %v, forbidden callbacks %d", names, calls)
				}
				return db.WithSavepoint(ctx, child, func(grandchild db.Session) error {
					inserted, err := grandchild.(db.ConflictInserter).InsertOnConflict(ctx, query.NewConflictInsertPlan("savepoint_items", []query.Assignment{query.NewAssignment(name, query.String(prefix+"a"))}, []query.FieldRef{name}))
					if err != nil || inserted {
						return fmt.Errorf("child conflict insertion: %v/%v", inserted, err)
					}
					return nil
				})
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := assertNames(ctx, root, prefix, "a", "b", "c"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("relation_mutation_and_child_rollback", func(t *testing.T) {
		prefix := t.Name() + "/"
		err := owner(ctx, func(outer db.Session) error {
			_, related := outer.(db.RelationSession)
			parentID, err := insert(ctx, outer, prefix+"parent")
			if err != nil {
				return err
			}
			childID, err := outer.Insert(ctx, query.NewInsertPlanReturningKey("savepoint_items", []query.Assignment{query.NewAssignment(name, query.String(prefix+"child")), query.NewAssignment(parent, query.Integer(parentID))}, id))
			if err != nil {
				return err
			}
			if !related {
				return nil
			}
			rollback := errors.New("undo relation mutation")
			err = db.WithSavepoint(ctx, outer, func(child db.Session) error {
				count, err := child.(db.RelationSession).RelationSetNull(ctx, query.NewRelationSetNullPlan("savepoint_items", parent, query.Integer(parentID)))
				if err != nil || count != 1 {
					return fmt.Errorf("child SET_NULL = %d/%v", count, err)
				}
				return rollback
			})
			if err != rollback {
				return fmt.Errorf("relation rollback = %v", err)
			}
			plan, err := query.NewPlan("savepoint_items", []query.FieldRef{id, parent}).WithConditions(query.NewCondition(id, query.LookupExact, query.Integer(childID)))
			if err != nil {
				return err
			}
			projection, err := query.NewProjectionResult(query.FieldResult(parent))
			if err != nil {
				return err
			}
			plan, err = plan.WithResultShape(projection)
			if err != nil {
				return err
			}
			rows, err := outer.Query(ctx, plan)
			if err != nil {
				return err
			}
			defer rows.Close()
			if !rows.Next() {
				return errors.New("relation child disappeared")
			}
			var value int64
			if err := rows.Scan(&value); err != nil {
				return err
			}
			if value != parentID {
				return fmt.Errorf("child rollback lost parent: %d, want %d", value, parentID)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func insert(ctx context.Context, session db.Session, value string) (int64, error) {
	return session.Insert(ctx, query.NewInsertPlanReturningKey("savepoint_items", []query.Assignment{query.NewAssignment(name, query.String(value))}, id))
}

// CheckPinned exercises savepoint rollback under a root stream's independently
// held cursor. The owner must keep using that stream's physical connection.
func CheckPinned(t *testing.T, root db.Queryer, owner Owner) {
	t.Helper()
	ctx := t.Context()
	prefix := t.Name() + "/"
	rollback := errors.New("undo nested pinned work")
	var retained db.Session
	err := owner(ctx, func(outer db.Session) error {
		retained = outer
		if _, err := insert(ctx, outer, prefix+"before"); err != nil {
			return err
		}
		if err := db.WithSavepoint(ctx, outer, func(child db.Session) error { _, err := insert(ctx, child, prefix+"before"); return err }); !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
			return fmt.Errorf("pinned unique rollback: %v", err)
		}
		if err := db.WithSavepoint(ctx, outer, func(child db.Session) error {
			if _, err := insert(ctx, child, prefix+"rolled_back"); err != nil {
				return err
			}
			return rollback
		}); err != rollback {
			return fmt.Errorf("pinned callback rollback: %v", err)
		}
		if err := db.WithSavepoint(ctx, outer, func(child db.Session) error { _, err := insert(ctx, child, prefix+"released"); return err }); err != nil {
			return err
		}
		_, err := insert(ctx, outer, prefix+"after")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := assertNames(ctx, root, prefix, "after", "before", "released"); err != nil {
		t.Fatal(err)
	}
	assertExpired(t, ctx, retained)
}

func namesPlan(prefix string) query.Plan {
	plan, err := query.NewPlan("savepoint_items", []query.FieldRef{name}).WithConditions(query.NewCondition(name, query.LookupIContains, query.String(prefix)))
	if err != nil {
		panic(err)
	} // Constant fixture AST construction cannot fail.
	return plan.WithOrderings(query.NewOrdering(name, query.Ascending))
}

func assertNames(ctx context.Context, source db.Queryer, prefix string, suffixes ...string) (err error) {
	rows, err := source.Query(ctx, namesPlan(prefix))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Err(), rows.Close()) }()
	got := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return err
		}
		got = append(got, value)
	}
	want := make([]string, len(suffixes))
	for index, suffix := range suffixes {
		want[index] = prefix + suffix
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("persisted names = %v, want %v", got, want)
	}
	return nil
}

func invalid(err error) bool {
	return errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan})
}

func assertExpired(t *testing.T, ctx context.Context, session db.Session) {
	t.Helper()
	if err := session.(db.SessionValidator).ValidateSession(ctx); !invalid(err) {
		t.Fatalf("retained validator: %v", err)
	}
	plan, _ := namesPlan("").WithLimit(0)
	if rows, err := session.Query(ctx, plan); !invalid(err) || rows != nil {
		t.Fatalf("retained empty Query: %v/%v", rows, err)
	}
	if _, err := session.Insert(ctx, query.InsertPlan{}); !invalid(err) {
		t.Fatalf("retained Insert: %v", err)
	}
	if _, err := session.Update(ctx, query.UpdatePlan{}); !invalid(err) {
		t.Fatalf("retained Update: %v", err)
	}
	if _, err := session.Delete(ctx, query.DeletePlan{}); !invalid(err) {
		t.Fatalf("retained Delete: %v", err)
	}
	if _, err := session.(db.ConflictInserter).InsertOnConflict(ctx, query.ConflictInsertPlan{}); !invalid(err) {
		t.Fatalf("retained conflict: %v", err)
	}
	if err := session.(db.BatchQueryer).QueryBatches(ctx, plan, 1, nil, nil); !invalid(err) {
		t.Fatalf("retained batch: %v", err)
	}
	if err := db.WithSavepoint(ctx, session, func(db.Session) error { t.Fatal("retained savepoint callback ran"); return nil }); !invalid(err) {
		t.Fatalf("retained savepoint: %v", err)
	}
	if relation, ok := session.(db.RelationSession); ok {
		if _, err := relation.RelationSetNull(ctx, query.RelationSetNullPlan{}); !invalid(err) {
			t.Fatalf("retained relation: %v", err)
		}
	}
}
