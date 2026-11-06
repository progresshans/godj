package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func rowLockBackends(t *testing.T) (*Backend, *Backend, rowLockFixture) {
	t.Helper()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	first := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	second := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	items, _ := quoteTable(namespace, "rowlock_items")
	categories, _ := quoteTable(namespace, "rowlock_categories")
	for _, statement := range []string{
		"CREATE TABLE " + categories + "(id BIGINT PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE " + items + "(id BIGINT PRIMARY KEY, name TEXT NOT NULL, category_id BIGINT NOT NULL REFERENCES " + categories + "(id) DEFERRABLE INITIALLY DEFERRED)",
		"INSERT INTO " + categories + " VALUES(1,'one'),(2,'two')",
		"INSERT INTO " + items + " VALUES(1,'first',1),(2,'second',2)",
	} {
		if _, err := first.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return first, second, rowLockPlans(t, false)
}

func readRowLockPlan(ctx context.Context, backend db.Queryer, plan query.Plan) (values [][]any, err error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	width := len(plan.SourceFields())
	if plan.ResultShape().Kind() == query.ResultProjection {
		width = len(plan.ResultShape().Expressions())
	}
	for _, projection := range plan.RelationProjections() {
		width += len(projection.TargetColumns())
	}
	for rows.Next() {
		row := make([]any, width)
		destinations := make([]any, width)
		for index := range row {
			destinations[index] = &row[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		values = append(values, row)
	}
	return values, rows.Err()
}

func filterRowLockID(t *testing.T, plan query.Plan, value int64) query.Plan {
	t.Helper()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := plan.WithConditions(query.NewCondition(id, query.LookupExact, query.Integer(value)))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func isRowLockUnavailable(err error) bool {
	var native *pgconn.PgError
	return errors.As(err, &native) && native.Code == "55P03"
}

func TestPostgresRowLockNativeTargetsAndProjection(t *testing.T) {
	first, second, fixture := rowLockBackends(t)
	ctx := t.Context()
	for _, test := range []struct {
		name                       string
		plan                       query.Plan
		targets                    []query.RowLockTarget
		itemLocked, categoryLocked bool
	}{
		{"selected_all", fixture.selected, nil, true, true},
		{"selected_self", fixture.selected, []query.RowLockTarget{query.LockSelf()}, true, false},
		{"selected_category", fixture.selected, []query.RowLockTarget{fixture.target}, false, true},
		{"values_only_category_with_self", fixture.values, []query.RowLockTarget{query.LockSelf()}, true, false},
		{"values_only_category_with_category", fixture.values, []query.RowLockTarget{fixture.target}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := lockPlan(t, filterRowLockID(t, test.plan, 1), query.LockForUpdate, query.LockWait, test.targets...)
			if err := first.Atomic(ctx, func(session db.Session) error {
				rows, err := readRowLockPlan(ctx, session, plan)
				if err != nil || len(rows) != 1 {
					return fmt.Errorf("locked selection: %v/%v", rows, err)
				}
				for _, probe := range []struct {
					table  string
					locked bool
				}{{"rowlock_items", test.itemLocked}, {"rowlock_categories", test.categoryLocked}} {
					probePlan := filterRowLockID(t, query.NewPlan(probe.table, []query.FieldRef{fixture.id}), 1)
					probePlan = lockPlan(t, probePlan, query.LockForUpdate, query.LockNoWait)
					err := second.Atomic(ctx, func(other db.Session) error { _, err := readRowLockPlan(ctx, other, probePlan); return err })
					if probe.locked && !isRowLockUnavailable(err) || !probe.locked && err != nil {
						return fmt.Errorf("%s locked=%v: %w", probe.table, probe.locked, err)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresRowLockNativeWaitSkipCancellationAndRelease(t *testing.T) {
	first, second, fixture := rowLockBackends(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	base := query.NewPlan("rowlock_items", []query.FieldRef{fixture.id}).WithOrderings(query.NewOrdering(fixture.id, query.Ascending))
	one := filterRowLockID(t, base, 1)
	locked := lockPlan(t, one, query.LockForUpdate, query.LockWait)
	if err := first.Atomic(ctx, func(session db.Session) error {
		if _, err := readRowLockPlan(ctx, session, locked); err != nil {
			return err
		}
		if err := second.Atomic(ctx, func(other db.Session) error {
			rows, err := readRowLockPlan(ctx, other, lockPlan(t, base, query.LockForUpdate, query.LockSkipLocked))
			if err != nil || !reflect.DeepEqual(rows, [][]any{{int64(2)}}) {
				return fmt.Errorf("skip locked rows=%v err=%v", rows, err)
			}
			return nil
		}); err != nil {
			return err
		}
		waiting, stop := context.WithTimeout(ctx, 150*time.Millisecond)
		defer stop()
		err := second.Atomic(waiting, func(other db.Session) error { _, err := readRowLockPlan(waiting, other, locked); return err })
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("canceled lock wait = %w", err)
		}
		return second.Atomic(ctx, func(other db.Session) error {
			_, err := readRowLockPlan(ctx, other, lockPlan(t, filterRowLockID(t, base, 2), query.LockForUpdate, query.LockNoWait))
			return err
		})
	}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	started := make(chan int, 1)
	waiterStarted, waiterJoined := false, false
	defer func() {
		if waiterStarted && !waiterJoined {
			cancel()
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("lock waiter did not terminate after cancellation")
			}
		}
	}()
	err := first.Atomic(ctx, func(session db.Session) error {
		if _, err := readRowLockPlan(ctx, session, locked); err != nil {
			return err
		}
		waiterStarted = true
		go func() {
			result <- second.Atomic(ctx, func(other db.Session) error {
				var pid int
				if err := other.(*transactionSession).transaction.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					return err
				}
				started <- pid
				rows, err := readRowLockPlan(ctx, other, locked)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(rows, [][]any{{int64(1)}}) {
					return fmt.Errorf("waiter rows = %v", rows)
				}
				return nil
			})
		}()
		var pid int
		select {
		case pid = <-started:
		case err := <-result:
			waiterJoined = true
			return fmt.Errorf("waiter failed before PID: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		}
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			var blocked bool
			if err := first.database.QueryRowContext(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", pid).Scan(&blocked); err != nil {
				return err
			}
			if blocked {
				return nil
			}
			select {
			case err := <-result:
				waiterJoined = true
				return fmt.Errorf("waiter completed before lock release: %w", err)
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
			}
		}
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	select {
	case err := <-result:
		waiterJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := second.Atomic(ctx, func(session db.Session) error {
		_, err := readRowLockPlan(ctx, session, lockPlan(t, one, query.LockForUpdate, query.LockNoWait))
		return err
	}); err != nil {
		t.Fatalf("lock survived commit: %v", err)
	}
}

func TestPostgresRowLockNativeSavepointAndCursorOwnership(t *testing.T) {
	first, second, fixture := rowLockBackends(t)
	ctx := t.Context()
	base := query.NewPlan("rowlock_items", []query.FieldRef{fixture.id})
	probe := func(id int64) error {
		return second.Atomic(ctx, func(session db.Session) error {
			_, err := readRowLockPlan(ctx, session, lockPlan(t, filterRowLockID(t, base, id), query.LockForUpdate, query.LockNoWait))
			return err
		})
	}
	for _, owner := range []struct {
		name string
		run  func(context.Context, func(db.Session) error) error
	}{
		{"ordinary", first.Atomic}, {"coordinated", first.CoordinatedAtomic},
		{"relation", func(ctx context.Context, callback func(db.Session) error) error {
			return first.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
		{"coordinated_relation", func(ctx context.Context, callback func(db.Session) error) error {
			return first.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
	} {
		t.Run(owner.name, func(t *testing.T) {
			var expired db.Session
			if err := owner.run(ctx, func(parent db.Session) error {
				if policy, err := parent.(db.ReadModifyWriteSession).ReadModifyWritePolicy(ctx); err != nil || policy != db.ReadModifyWriteRowLock {
					return fmt.Errorf("parent read-modify-write policy %q: %w", policy, err)
				}
				if err := db.WithSavepoint(ctx, parent, func(child db.Session) error {
					expired = child
					if policy, err := child.(db.ReadModifyWriteSession).ReadModifyWritePolicy(ctx); err != nil || policy != db.ReadModifyWriteRowLock {
						return fmt.Errorf("child read-modify-write policy %q: %w", policy, err)
					}
					plan := lockPlan(t, filterRowLockID(t, base, 1), query.LockForUpdate, query.LockWait)
					seen := 0
					if err := child.(db.BatchQueryer).QueryBatches(ctx, plan, 1, func(row db.Row) error {
						var id int64
						if err := row.Scan(&id); err != nil {
							return err
						}
						if id != 1 {
							return fmt.Errorf("cursor id=%d", id)
						}
						seen++
						return nil
					}, func(affinity db.Queryer) (bool, error) {
						if affinity != child {
							return false, errors.New("cursor lost its transaction affinity")
						}
						if err := probe(1); !isRowLockUnavailable(err) {
							return false, fmt.Errorf("cursor did not lock fetched row: %w", err)
						}
						return true, nil
					}); err != nil {
						return err
					}
					if seen != 1 {
						return fmt.Errorf("cursor emitted %d rows", seen)
					}
					return nil
				}); err != nil {
					return err
				}
				if err := probe(1); !isRowLockUnavailable(err) {
					return fmt.Errorf("released savepoint lost parent lock: %w", err)
				}
				rollback := errors.New("rollback child lock")
				err := db.WithSavepoint(ctx, parent, func(child db.Session) error {
					if _, err := readRowLockPlan(ctx, child, lockPlan(t, filterRowLockID(t, base, 2), query.LockForUpdate, query.LockWait)); err != nil {
						return err
					}
					if err := probe(2); !isRowLockUnavailable(err) {
						return fmt.Errorf("savepoint did not lock: %w", err)
					}
					return rollback
				})
				if !errors.Is(err, rollback) {
					return err
				}
				return probe(2)
			}); err != nil {
				t.Fatal(err)
			}
			if err := probe(1); err != nil {
				t.Fatalf("completed owner retained lock: %v", err)
			}
			if _, err := expired.(db.ReadModifyWriteSession).ReadModifyWritePolicy(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan, Category: query.CategoryBackend}) {
				t.Fatalf("expired session advertised a write policy: %v", err)
			}
			for _, empty := range []bool{false, true} {
				plan := lockPlan(t, base, query.LockForUpdate, query.LockWait)
				if empty {
					var err error
					plan, err = plan.WithLimit(0)
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := readRowLockPlan(ctx, expired, plan); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
					t.Fatalf("expired lock scope, empty=%v: %v", empty, err)
				}
			}
		})
	}
	if err := first.QueryBatches(ctx, base.WithOrderings(query.NewOrdering(fixture.id, query.Ascending)), 1, func(row db.Row) error { var id int64; return row.Scan(&id) }, func(affinity db.Queryer) (bool, error) {
		plan := lockPlan(t, filterRowLockID(t, base, 1), query.LockForUpdate, query.LockWait)
		if _, err := readRowLockPlan(ctx, affinity, plan); !errors.Is(err, &query.Error{Code: query.CodeTransactionRequired}) {
			return false, fmt.Errorf("root cursor affinity accepted row lock: %w", err)
		}
		err := affinity.(db.BatchQueryer).QueryBatches(ctx, plan, 1, func(db.Row) error { return errors.New("unexpected scan") }, func(db.Queryer) (bool, error) { return false, errors.New("unexpected yield") })
		if !errors.Is(err, &query.Error{Code: query.CodeTransactionRequired}) {
			return false, fmt.Errorf("root cursor nested lock: %w", err)
		}
		err = affinity.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			if _, err := readRowLockPlan(ctx, session, plan); err != nil {
				return err
			}
			if err := probe(1); !isRowLockUnavailable(err) {
				return fmt.Errorf("pinned writable transaction did not lock: %w", err)
			}
			return nil
		})
		return false, err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRowLockNoKeyUpdatePermitsForeignKeyReference(t *testing.T) {
	first, second, fixture := rowLockBackends(t)
	ctx := t.Context()
	parent := filterRowLockID(t, query.NewPlan("rowlock_categories", []query.FieldRef{fixture.id}), 1)
	items, _ := quoteTable(first.schema, "rowlock_items")
	for _, strength := range []query.LockStrength{query.LockForUpdate, query.LockForNoKeyUpdate} {
		t.Run(string(strength), func(t *testing.T) {
			if err := first.Atomic(ctx, func(session db.Session) error {
				if _, err := readRowLockPlan(ctx, session, lockPlan(t, parent, strength, query.LockWait)); err != nil {
					return err
				}
				err := second.Atomic(ctx, func(other db.Session) error {
					transaction := other.(*transactionSession).transaction
					if _, err := transaction.ExecContext(ctx, "SET LOCAL lock_timeout = '150ms'"); err != nil {
						return err
					}
					_, err := transaction.ExecContext(ctx, "INSERT INTO "+items+" VALUES(3,'fk-probe',1)")
					return err
				})
				if strength == query.LockForUpdate && !isRowLockUnavailable(err) || strength == query.LockForNoKeyUpdate && err != nil {
					return fmt.Errorf("FK reference under %s: %w", strength, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := first.database.QueryRowContext(ctx, "SELECT count(*) FROM "+items+" WHERE id=3").Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if strength == query.LockForNoKeyUpdate {
				want = 1
			}
			if count != want {
				t.Fatalf("committed FK row count = %d, want %d", count, want)
			}
		})
	}
}
