package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestPostgresRelationSetNullValidatesAndParameterizes(t *testing.T) {
	fk := query.NewFieldRef("owner", "owner_id", query.FieldInteger, true)
	for _, key := range []int64{-1, 0, 17} {
		sql, args, err := compileRelationSetNull("app", query.NewRelationSetNullPlan("child", fk, query.Integer(key)))
		if err != nil || sql != `UPDATE "app"."child" SET "owner_id" = NULL WHERE "owner_id" = $1` || !reflect.DeepEqual(args, []any{key}) {
			t.Fatal("SET_NULL plan changed", sql, args, err)
		}
	}
	for _, plan := range []query.RelationSetNullPlan{
		{}, query.NewRelationSetNullPlan("child", fk, query.Null()), query.NewRelationSetNullPlan("child", fk, query.String("1")),
		query.NewRelationSetNullPlan("child", query.NewFieldRef("owner", "owner_id", query.FieldInteger, false), query.Integer(1)),
		query.NewRelationSetNullPlan("child", query.NewFieldRef("owner", "owner_id", query.FieldString, true), query.Integer(1)),
		query.NewRelationSetNullPlan("bad\x00table", fk, query.Integer(1)),
	} {
		if sql, args, err := compileRelationSetNull("app", plan); err == nil || sql != "" || args != nil {
			t.Fatal("invalid SET_NULL plan accepted", sql, args, err)
		}
	}
}

func TestPostgresAtomicRelationOwnsLifetimeRollbackAndUnknownOutcome(t *testing.T) {
	state := &transactionTestState{}
	backend := newTransactionTestBackend(state)
	t.Cleanup(func() { _ = backend.Close() })
	ctx := t.Context()
	plan := query.NewRelationSetNullPlan("child", query.NewFieldRef("owner", "owner_id", query.FieldInteger, true), query.Integer(0))
	if err := backend.AtomicRelation(ctx, nil); err == nil || state.snapshot().begins != 0 {
		t.Fatal("nil callback began a transaction", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	calls := 0
	if err := backend.AtomicRelation(canceled, func(db.RelationSession) error { calls++; return nil }); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled callback ran", err)
	}
	var retained db.RelationSession
	if err := backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		calls++
		retained = session
		count, err := session.RelationSetNull(ctx, plan)
		if err != nil || count != 1 {
			t.Fatal("transaction SET_NULL failed", count, err)
		}
		return nil
	}); err != nil || calls != 1 || state.snapshot().commits != 1 {
		t.Fatal("relation callback/commit count changed", err)
	}
	if _, err := retained.RelationSetNull(ctx, plan); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatal("retained relation session remained usable", err)
	}
	sentinel := errors.New("relation callback failure")
	if err := backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		if _, err := session.RelationSetNull(ctx, plan); err != nil {
			return err
		}
		return sentinel
	}); err != sentinel || state.snapshot().rollbacks != 1 {
		t.Fatal("normal rollback changed callback ownership", err)
	}
	commitErr := errors.New("commit transport failure")
	state.setCommitError(commitErr)
	err := backend.AtomicRelation(ctx, func(db.RelationSession) error { return nil })
	if !errors.Is(err, commitErr) || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}) {
		t.Fatal("literal COMMIT failure lost unknown outcome", err)
	}
	state.setCommitError(nil)
	rollbackErr := errors.New("rollback transport failure")
	state.setRollbackError(rollbackErr)
	err = backend.AtomicRelation(ctx, func(db.RelationSession) error { return sentinel })
	if !errors.Is(err, sentinel) || !errors.Is(err, rollbackErr) || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown}) {
		t.Fatal("rollback failure hidden", err)
	}
}
