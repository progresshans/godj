package systemstate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestAuditPruneValidatesBoundedStreamBeforeDeleting(t *testing.T) {
	closeFailure := errors.New("close failed")
	iterationFailure := errors.New("iteration failed")
	for _, test := range []struct {
		name                   string
		ids                    []int64
		iterationErr, closeErr error
		wantDelete             []int64
		wantError              bool
	}{
		{name: "retained prefix", ids: []int64{3, 2}},
		{name: "one victim", ids: []int64{3, 2, 1}, wantDelete: []int64{1}},
		{name: "backend ignored limit", ids: []int64{4, 3, 2, 1}, wantError: true},
		{name: "invalid victim", ids: []int64{3, 2, 0}, wantError: true},
		{name: "close failure", ids: []int64{3, 2, 1}, closeErr: closeFailure, wantError: true},
		{name: "iteration and close failure", ids: []int64{3, 2, 1}, iterationErr: iterationFailure, closeErr: closeFailure, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &systemIDRows{ids: test.ids, iterationErr: test.iterationErr, closeErr: test.closeErr}
			session := &auditPruneSession{rows: rows}
			err := pruneAuditRows(context.Background(), session, 2)
			if (err != nil) != test.wantError || !reflect.DeepEqual(session.deleted, test.wantDelete) || rows.closed != 1 {
				t.Fatalf("prune = error:%v deleted:%v closed:%d", err, session.deleted, rows.closed)
			}
			for _, cause := range []error{test.iterationErr, test.closeErr} {
				if cause != nil && !errors.Is(err, cause) {
					t.Fatalf("prune lost cause %v: %v", cause, err)
				}
			}
		})
	}
}

func TestSystemRowScannerClosesOnPanicAndCancellation(t *testing.T) {
	marker := &struct{ name string }{"scanner panic"}
	rows := &systemIDRows{ids: []int64{1}}
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
	rows = &systemIDRows{ids: []int64{2, 1}, next: cancel}
	session := &auditPruneSession{rows: rows}
	if err := pruneAuditRows(ctx, session, 1); !errors.Is(err, context.Canceled) || rows.closed != 1 || len(session.deleted) != 0 {
		t.Fatalf("canceled prune = %v, close=%d deleted=%v", err, rows.closed, session.deleted)
	}
}

func BenchmarkAuditPruneSparseCapacity(b *testing.B) {
	for _, capacity := range []int{admin.DefaultAuditCapacity, admin.MaximumAuditCapacity} {
		b.Run(fmt.Sprintf("capacity_%d", capacity), func(b *testing.B) {
			rows := &systemIDRows{ids: []int64{1}}
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

type systemIDRows struct {
	ids                    []int64
	position, closed       int
	iterationErr, closeErr error
	next                   func()
}

func (rows *systemIDRows) Next() bool {
	if rows.next != nil {
		rows.next()
	}
	if rows.position == len(rows.ids) {
		return false
	}
	rows.position++
	return true
}
func (rows *systemIDRows) Scan(targets ...any) error {
	*targets[0].(*int64) = rows.ids[rows.position-1]
	return nil
}
func (rows *systemIDRows) Err() error   { return rows.iterationErr }
func (rows *systemIDRows) Close() error { rows.closed++; return rows.closeErr }

type auditPruneSession struct {
	db.Session
	rows    *systemIDRows
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
