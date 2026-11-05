// Package insertgraphtest exercises complete required-FK graphs through the
// public session APIs. Each backend supplies independent native schema and
// connections; expected rows are constructed directly from the input data.
package insertgraphtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var Key = query.NewFieldRef("record", "record_key", query.FieldInteger, false)
var Peer = query.NewFieldRef("peer", "peer_key", query.FieldInteger, false)
var Token = query.NewFieldRef("token", "token", query.FieldString, false)
var Optional = query.NewFieldRef("optional", "optional_value", query.FieldInteger, true)

type Backend interface {
	db.Queryer
	db.Atomic
	db.RelationAtomic
	db.CoordinatedAtomic
	db.CoordinatedRelationAtomic
	db.SnapshotReader
}

type Fixture struct {
	Backend  Backend
	Observer db.Queryer
}

type Row struct {
	Key, Peer int64
	Token     string
	Optional  sql.NullInt64
}

func Read(t *testing.T, source db.Queryer, table string) []Row {
	t.Helper()
	rows, err := source.Query(t.Context(), query.NewPlan(table, []query.FieldRef{Key, Peer, Token, Optional}))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []Row
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.Key, &row.Peer, &row.Token, &row.Optional); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(result, func(left, right Row) int {
		if left.Key < right.Key {
			return -1
		}
		if left.Key > right.Key {
			return 1
		}
		return 0
	})
	return result
}

func RequireRows(t *testing.T, source db.Queryer, table string, want []Row) {
	t.Helper()
	got := Read(t, source, table)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s rows = %#v, want %#v", table, got, want)
	}
}

func Insert(table string, row Row) query.InsertPlan {
	optional := query.Null()
	if row.Optional.Valid {
		optional = query.Integer(row.Optional.Int64)
	}
	return query.NewInsertPlanReturningKey(table, []query.Assignment{
		query.NewAssignment(Token, query.String(row.Token)),
		query.NewAssignment(Key, query.Integer(row.Key)),
		query.NewAssignment(Optional, optional),
		query.NewAssignment(Peer, query.Integer(row.Peer)),
	}, Key)
}

func Plan(t *testing.T, inserts ...query.InsertPlan) query.InsertGraphPlan {
	t.Helper()
	plan, err := query.NewInsertGraphPlan(inserts)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func Reserve(t *testing.T, session db.Session, table string, count int) []int64 {
	t.Helper()
	plan, err := query.NewAutoKeyPlan(table, Key, count)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := session.(db.AutoKeyAllocator).ReserveAutoKeys(t.Context(), plan)
	if err != nil || len(keys) != count {
		t.Fatal("native graph key reservation", keys, err)
	}
	return keys
}

func Cycle(t *testing.T, session db.Session, token string) (query.InsertGraphPlan, Row, Row) {
	t.Helper()
	leftKey := Reserve(t, session, "graph_left", 1)[0]
	rightKey := Reserve(t, session, "graph_right", 1)[0]
	left := Row{Key: leftKey, Peer: rightKey, Token: token + "_left'\"?\n값"}
	right := Row{Key: rightKey, Peer: leftKey, Token: token + "_right", Optional: sql.NullInt64{Valid: true, Int64: 9007199254740993}}
	return Plan(t, Insert("graph_left", left), Insert("graph_right", right)), left, right
}

func requireKeys(t *testing.T, actual []int64, err error, want ...int64) {
	t.Helper()
	if err != nil || !slices.Equal(actual, want) {
		t.Fatal("actual INSERT keys differ from requested graph", actual, want, err)
	}
}

func ordinaryInsert(ctx context.Context, session db.Session, insert query.InsertPlan) error {
	assignments := insert.Assignments()
	fields := make([]query.FieldRef, len(assignments))
	row := make([]query.Value, len(assignments))
	for index, assignment := range assignments {
		fields[index], row[index] = assignment.Field(), assignment.Value()
	}
	plan, err := query.NewBulkInsertPlan(insert.Table(), fields, [][]query.Value{row}, Key, query.BulkConflict{})
	if err != nil {
		return err
	}
	_, err = session.(db.BulkInserter).BulkInsert(ctx, plan)
	return err
}

// Check expects three fresh native AutoField tables. graph_left.peer_key and
// graph_right.peer_key are reciprocal required immediate FKs; graph_self has
// a required self FK. Each has a nonempty unique token and nullable integer.
func Check(t *testing.T, fresh func(*testing.T) Fixture) {
	t.Helper()
	t.Run("capability_is_borrowed_write_only", func(t *testing.T) {
		fixture := fresh(t)
		if _, ok := fixture.Backend.(db.GraphInserter); ok {
			t.Fatal("root backend advertises insertion graph")
		}
		if err := fixture.Backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
			if _, ok := reader.(db.GraphInserter); ok {
				t.Fatal("read snapshot advertises insertion graph")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	for _, name := range []string{"ordinary", "coordinated", "relation", "coordinated_relation"} {
		t.Run("scope_"+name, func(t *testing.T) {
			fixture := fresh(t)
			ctx := t.Context()
			var parent, retainedChild db.GraphInserter
			var wantLeft, wantRight []Row
			run := fixture.Backend.Atomic
			switch name {
			case "coordinated":
				run = fixture.Backend.CoordinatedAtomic
			case "relation":
				run = func(ctx context.Context, fn func(db.Session) error) error {
					return fixture.Backend.AtomicRelation(ctx, func(session db.RelationSession) error { return fn(session) })
				}
			case "coordinated_relation":
				run = func(ctx context.Context, fn func(db.Session) error) error {
					return fixture.Backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return fn(session) })
				}
			}
			if err := run(ctx, func(session db.Session) error {
				parent = session.(db.GraphInserter)
				if limits, err := parent.InsertGraphLimits(ctx); err != nil || limits.Rows < query.MaximumInsertGraphRows || limits.Parameters < 1024 {
					t.Fatal("invalid graph limits", limits, err)
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if keys, err := parent.InsertGraph(canceled, query.InsertGraphPlan{}); keys != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation must precede plan validation", keys, err)
				}
				if keys, err := parent.InsertGraph(nil, query.InsertGraphPlan{}); keys != nil || err == nil {
					t.Fatal("nil context accepted", keys, err)
				}
				if keys, err := parent.InsertGraph(ctx, query.InsertGraphPlan{}); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("zero graph accepted", keys, err)
				}
				first, left, right := Cycle(t, session, "first")
				keys, err := parent.InsertGraph(ctx, first)
				requireKeys(t, keys, err, left.Key, right.Key)
				keys[0]++ // Caller owns its returned result; storage must not change.
				wantLeft, wantRight = append(wantLeft, left), append(wantRight, right)
				RequireRows(t, session, "graph_left", wantLeft)
				RequireRows(t, session, "graph_right", wantRight)
				RequireRows(t, fixture.Observer, "graph_left", nil)
				RequireRows(t, fixture.Observer, "graph_right", nil)
				second, left, right := Cycle(t, session, "second")
				rows, err := session.Query(ctx, query.NewPlan("graph_left", []query.FieldRef{Key}))
				if err != nil {
					t.Fatal(err)
				}
				if keys, err := parent.InsertGraph(ctx, second); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("graph entered with a live parent cursor", keys, err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if err := session.(db.Savepointer).Savepoint(ctx, func(child db.Session) error {
					retainedChild = child.(db.GraphInserter)
					if _, err := parent.InsertGraphLimits(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
						t.Fatal("parent became available during child graph", err)
					}
					if keys, err := parent.InsertGraph(ctx, second); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
						t.Fatal("parent graph bypassed child scope", keys, err)
					}
					keys, err := retainedChild.InsertGraph(ctx, second)
					requireKeys(t, keys, err, left.Key, right.Key)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if keys, err := retainedChild.InsertGraph(ctx, second); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("retained child graph remained active", keys, err)
				}
				wantLeft, wantRight = append(wantLeft, left), append(wantRight, right)
				RequireRows(t, session, "graph_left", wantLeft)
				RequireRows(t, session, "graph_right", wantRight)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if keys, err := parent.InsertGraph(ctx, query.InsertGraphPlan{}); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("retained root graph remained active", keys, err)
			}
			if _, err := parent.InsertGraphLimits(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("expired limits remained available", err)
			}
			RequireRows(t, fixture.Observer, "graph_left", wantLeft)
			RequireRows(t, fixture.Observer, "graph_right", wantRight)
		})
	}
	t.Run("ordinary_insert_still_checks_foreign_keys", func(t *testing.T) {
		fixture := fresh(t)
		if err := fixture.Backend.Atomic(t.Context(), func(session db.Session) error {
			plan, left, right := Cycle(t, session, "intact")
			bad := Insert("graph_left", left)
			checkOrdinary := func() {
				if err := session.(db.Savepointer).Savepoint(t.Context(), func(child db.Session) error {
					return ordinaryInsert(t.Context(), child, bad)
				}); err == nil {
					t.Fatal("ordinary required-FK INSERT succeeded without its target")
				}
			}
			checkOrdinary()
			keys, err := session.(db.GraphInserter).InsertGraph(t.Context(), plan)
			requireKeys(t, keys, err, left.Key, right.Key)
			// A different missing endpoint after graph success demonstrates that
			// neither backend left connection-wide FK deferral enabled.
			bad = Insert("graph_left", Row{Key: Reserve(t, session, "graph_left", 1)[0], Peer: -999, Token: "missing"})
			checkOrdinary()
			RequireRows(t, session, "graph_left", []Row{left})
			RequireRows(t, session, "graph_right", []Row{right})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	for _, failure := range []string{"missing_foreign_key", "check_constraint", "unique_constraint"} {
		t.Run(failure+"_rolls_back_only_graph", func(t *testing.T) {
			fixture := fresh(t)
			if err := fixture.Backend.Atomic(t.Context(), func(session db.Session) error {
				markerKey := Reserve(t, session, "graph_self", 1)[0]
				marker := Row{Key: markerKey, Peer: markerKey, Token: "parent"}
				if err := ordinaryInsert(t.Context(), session, Insert("graph_self", marker)); err != nil {
					t.Fatal(err)
				}
				good, left, right := Cycle(t, session, "retry")
				badRight := right
				switch failure {
				case "missing_foreign_key":
					badRight.Peer = -999
				case "check_constraint":
					badRight.Token = ""
				}
				inserts := []query.InsertPlan{Insert("graph_left", left), Insert("graph_right", badRight)}
				if failure == "unique_constraint" {
					duplicate := left
					duplicate.Key = Reserve(t, session, "graph_left", 1)[0]
					inserts = append(inserts, Insert("graph_left", duplicate))
				}
				keys, err := session.(db.GraphInserter).InsertGraph(t.Context(), Plan(t, inserts...))
				if err == nil || keys != nil {
					t.Fatal("failed graph exposed a result", keys, err)
				}
				RequireRows(t, session, "graph_left", nil)
				RequireRows(t, session, "graph_right", nil)
				RequireRows(t, session, "graph_self", []Row{marker})
				keys, err = session.(db.GraphInserter).InsertGraph(t.Context(), good)
				requireKeys(t, keys, err, left.Key, right.Key)
				RequireRows(t, session, "graph_left", []Row{left})
				RequireRows(t, session, "graph_right", []Row{right})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("savepoint_and_root_rollback", func(t *testing.T) {
		fixture := fresh(t)
		stop := errors.New("graph owner requested rollback")
		err := fixture.Backend.Atomic(t.Context(), func(session db.Session) error {
			first, left, right := Cycle(t, session, "keep_until_root")
			keys, err := session.(db.GraphInserter).InsertGraph(t.Context(), first)
			requireKeys(t, keys, err, left.Key, right.Key)
			if err := session.(db.Savepointer).Savepoint(t.Context(), func(child db.Session) error {
				second, a, b := Cycle(t, child, "remove_in_child")
				keys, err := child.(db.GraphInserter).InsertGraph(t.Context(), second)
				requireKeys(t, keys, err, a.Key, b.Key)
				return stop
			}); !errors.Is(err, stop) {
				t.Fatal("nested rollback lost its cause", err)
			}
			RequireRows(t, session, "graph_left", []Row{left})
			RequireRows(t, session, "graph_right", []Row{right})
			return stop
		})
		if !errors.Is(err, stop) {
			t.Fatal("root rollback lost its cause", err)
		}
		RequireRows(t, fixture.Observer, "graph_left", nil)
		RequireRows(t, fixture.Observer, "graph_right", nil)
	})
	t.Run("maximum_cycle_is_complete", func(t *testing.T) {
		fixture := fresh(t)
		var expected []Row
		if err := fixture.Backend.Atomic(t.Context(), func(session db.Session) error {
			keys := Reserve(t, session, "graph_self", query.MaximumInsertGraphRows)
			inserts := make([]query.InsertPlan, len(keys))
			for index, key := range keys {
				row := Row{Key: key, Peer: keys[(index+1)%len(keys)], Token: fmt.Sprintf("row_%03d", index)}
				expected = append(expected, row)
				inserts[index] = Insert("graph_self", row)
			}
			actual, err := session.(db.GraphInserter).InsertGraph(t.Context(), Plan(t, inserts...))
			requireKeys(t, actual, err, keys...)
			RequireRows(t, session, "graph_self", expected)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		RequireRows(t, fixture.Observer, "graph_self", expected)
	})
	t.Run("signed_explicit_keys_and_self_reference", func(t *testing.T) {
		fixture := fresh(t)
		want := []Row{{Key: -7, Peer: 0, Token: "negative"}, {Key: 0, Peer: -7, Token: "zero"}, {Key: 9007199254740993, Peer: 9007199254740993, Token: "self"}}
		if err := fixture.Backend.Atomic(t.Context(), func(session db.Session) error {
			// Intentionally reverse input order: result association must follow
			// input, not key order, CTE scheduling, or physical scan order.
			plan := Plan(t, Insert("graph_self", want[2]), Insert("graph_self", want[1]), Insert("graph_self", want[0]))
			keys, err := session.(db.GraphInserter).InsertGraph(t.Context(), plan)
			requireKeys(t, keys, err, want[2].Key, want[1].Key, want[0].Key)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		RequireRows(t, fixture.Observer, "graph_self", want)
	})
}

// CheckInFlight waits until the backend has finished metadata inspection and
// is about to execute its actual graph statement. Parent operations must not
// overlap the private savepoint, including reads and another graph allocation.
func CheckInFlight(t *testing.T, session db.Session, entered <-chan struct{}, release func()) {
	t.Helper()
	plan, left, right := Cycle(t, session, "inflight")
	graph := session.(db.GraphInserter)
	type outcome struct {
		keys []int64
		err  error
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan outcome, 1)
	go func() {
		keys, err := graph.InsertGraph(ctx, plan)
		done <- outcome{keys: keys, err: err}
	}()
	joined := false
	defer func() {
		release()
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("native graph worker did not stop after release and cancellation")
			}
		}
	}()
	select {
	case <-entered:
	case result := <-done:
		joined = true
		t.Fatal("graph returned before the native statement boundary", result)
	case <-ctx.Done():
		t.Fatal("graph did not reach its native statement", ctx.Err())
	}
	if limits, err := graph.InsertGraphLimits(t.Context()); limits != (db.InsertGraphLimits{}) || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("parent graph remained available during native I/O", limits, err)
	}
	if rows, err := session.Query(t.Context(), query.NewPlan("graph_left", []query.FieldRef{Key})); rows != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		if rows != nil {
			_ = rows.Close()
		}
		t.Fatal("parent query overlapped native graph I/O", err)
	}
	if _, err := session.Insert(t.Context(), Insert("graph_self", Row{Key: 100, Peer: 100, Token: "concurrent"})); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("parent INSERT overlapped native graph I/O", err)
	}
	called := false
	if err := session.(db.Savepointer).Savepoint(t.Context(), func(db.Session) error {
		called = true
		return nil
	}); called || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("parent savepoint overlapped native graph I/O", called, err)
	}
	if keys, err := graph.InsertGraph(t.Context(), plan); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("second graph overlapped native graph I/O", keys, err)
	}
	release()
	select {
	case result := <-done:
		joined = true
		requireKeys(t, result.keys, result.err, left.Key, right.Key)
	case <-ctx.Done():
		t.Fatal("released graph did not complete", ctx.Err())
	}
	RequireRows(t, session, "graph_left", []Row{left})
	RequireRows(t, session, "graph_right", []Row{right})
	marker := Row{Key: 100, Peer: 100, Token: "after_native_io"}
	if err := ordinaryInsert(t.Context(), session, Insert("graph_self", marker)); err != nil {
		t.Fatal("parent did not become usable after graph completion", err)
	}
	RequireRows(t, session, "graph_self", []Row{marker})
}
