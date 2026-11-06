package systemstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestAuditPruneValidatesBoundedAggregateBeforeDeleting(t *testing.T) {
	closeFailure := errors.New("close failed")
	iterationFailure := errors.New("iteration failed")
	for _, test := range []struct {
		name                   string
		values                 [][2]int64
		nullMinimum            bool
		scanErr                error
		iterationErr, closeErr error
		wantDelete             []int64
		wantError              bool
	}{
		{name: "empty", values: [][2]int64{{0, 0}}, nullMinimum: true},
		{name: "retained prefix", values: [][2]int64{{2, 2}}},
		{name: "one victim", values: [][2]int64{{3, 1}}, wantDelete: []int64{1}},
		{name: "backend ignored limit", values: [][2]int64{{4, 1}}, wantError: true},
		{name: "negative count", values: [][2]int64{{-1, 1}}, wantError: true},
		{name: "invalid victim", values: [][2]int64{{3, 0}}, wantError: true},
		{name: "invalid retained sequence", values: [][2]int64{{2, -1}}, wantError: true},
		{name: "null nonempty minimum", values: [][2]int64{{2, 0}}, nullMinimum: true, wantError: true},
		{name: "nonnull empty minimum", values: [][2]int64{{0, 1}}, wantError: true},
		{name: "missing aggregate row", wantError: true},
		{name: "extra aggregate row", values: [][2]int64{{3, 1}, {3, 1}}, wantError: true},
		{name: "scan failure", values: [][2]int64{{3, 1}}, scanErr: errors.New("scan failed"), wantError: true},
		{name: "close failure", values: [][2]int64{{3, 1}}, closeErr: closeFailure, wantError: true},
		{name: "iteration and close failure", values: [][2]int64{{3, 1}}, iterationErr: iterationFailure, closeErr: closeFailure, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &auditAggregateRows{values: test.values, nullMinimum: test.nullMinimum, scanErr: test.scanErr, iterationErr: test.iterationErr, closeErr: test.closeErr}
			session := &auditPruneSession{rows: rows}
			err := pruneAuditRows(context.Background(), session, 2)
			if (err != nil) != test.wantError || !reflect.DeepEqual(session.deleted, test.wantDelete) || rows.closed != 1 {
				t.Fatalf("prune = error:%v deleted:%v closed:%d", err, session.deleted, rows.closed)
			}
			for _, cause := range []error{test.scanErr, test.iterationErr, test.closeErr} {
				if cause != nil && !errors.Is(err, cause) {
					t.Fatalf("prune lost cause %v: %v", cause, err)
				}
			}
		})
	}
}

func TestSystemRowScannerClosesOnPanicAndCancellation(t *testing.T) {
	marker := &struct{ name string }{"scanner panic"}
	rows := &auditAggregateRows{values: [][2]int64{{1, 1}}}
	func() {
		defer func() {
			if got := recover(); got != marker {
				t.Fatalf("scanner panic = %#v", got)
			}
		}()
		_ = scanRows(context.Background(), &auditPruneSession{rows: rows}, query.Plan{}, persistenceRowsFailure("test"), func(db.Rows) (bool, error) {
			panic(marker)
		})
	}()
	if rows.closed != 1 {
		t.Fatalf("panic close calls = %d", rows.closed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rows = &auditAggregateRows{values: [][2]int64{{2, 1}}, next: cancel}
	session := &auditPruneSession{rows: rows}
	if err := pruneAuditRows(ctx, session, 1); !errors.Is(err, context.Canceled) || rows.closed != 1 || len(session.deleted) != 0 {
		t.Fatalf("canceled prune = %v, close=%d deleted=%v", err, rows.closed, session.deleted)
	}
}

func BenchmarkAuditPruneSparseCapacity(b *testing.B) {
	for _, capacity := range []int{admin.DefaultAuditCapacity, admin.MaximumAuditCapacity} {
		b.Run(fmt.Sprintf("capacity_%d", capacity), func(b *testing.B) {
			rows := &auditAggregateRows{values: [][2]int64{{1, 1}}}
			session := &auditPruneSession{rows: rows}
			b.ReportAllocs()
			for b.Loop() {
				rows.position, rows.closed = 0, 0
				if err := pruneAuditRows(context.Background(), session, capacity); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type auditAggregateRows struct {
	values                 [][2]int64
	nullMinimum            bool
	scanErr                error
	position, closed       int
	iterationErr, closeErr error
	next                   func()
}

func (rows *auditAggregateRows) Next() bool {
	if rows.next != nil {
		rows.next()
	}
	if rows.position == len(rows.values) {
		return false
	}
	rows.position++
	return true
}
func (rows *auditAggregateRows) Scan(targets ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	*targets[0].(*int64) = rows.values[rows.position-1][0]
	*targets[1].(*sql.NullInt64) = sql.NullInt64{Int64: rows.values[rows.position-1][1], Valid: !rows.nullMinimum}
	return nil
}
func (rows *auditAggregateRows) Err() error   { return rows.iterationErr }
func (rows *auditAggregateRows) Close() error { rows.closed++; return rows.closeErr }

type auditPruneSession struct {
	db.Session
	rows    *auditAggregateRows
	deleted []int64
}

func (session *auditPruneSession) Query(context.Context, query.Plan) (db.Rows, error) {
	return session.rows, nil
}
func (session *auditPruneSession) Delete(_ context.Context, plan query.DeletePlan) (int64, error) {
	if session.rows.closed != 1 {
		return 0, errors.New("delete started before rows closed")
	}
	value, ok := plan.KeyValue().Integer()
	if !ok {
		return 0, errors.New("delete key is not integer")
	}
	session.deleted = append(session.deleted, value)
	return 1, nil
}
