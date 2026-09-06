package orm

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func orderedRequiredSelectQuery(t *testing.T, backend db.Queryer) ForwardSelectQuery[relationObjectTestPost, relationObjectTestAuthor] {
	t.Helper()
	selected := requiredSelectQuery(t, backend)
	id := NewIntegerField[relationObjectTestPost](relationObjectTestPostDescriptor{}.Metadata().Fields[0])
	selected.plan = selected.plan.WithOrderings(id.Asc().ordering)
	return selected
}

func TestForwardSelectFirstBoundsScanPreservesPlanAndAllCache(t *testing.T) {
	ctx := context.Background()
	var issued []*selectRelatedRows
	backend := &selectRelatedBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		rows := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue(), selectRelatedRequiredValue()}}
		issued = append(issued, rows)
		return rows, nil
	}}
	eager := orderedRequiredSelectQuery(t, backend)
	eager.plan, _ = eager.plan.WithOffset(2)
	eager.plan, _ = eager.plan.WithLimit(3)
	eager.plan = eager.plan.WithDistinct()
	first, found, err := eager.First(ctx)
	if err != nil || !found || first == nil || issued[0].scanCalls.Load() != 1 || issued[0].closeCalls.Load() != 1 {
		t.Fatalf("cold First = (%v, %v, %v), rows=%+v", first, found, err, issued)
	}
	plan := backend.plans[0]
	limit, _ := plan.Limit()
	offset, _ := plan.Offset()
	projection, projected := plan.RelationProjection()
	if limit != 1 || offset != 2 || !plan.Distinct() || !projected || projection.Hop().Field() != "author" || len(plan.Orderings()) != 1 {
		t.Fatalf("First lost source plan: %+v", plan)
	}
	if _, ready := eager.evaluation.cachedValues(); ready {
		t.Fatal("cold First populated All cache")
	}
	all, err := eager.All(ctx)
	if err != nil || len(all) != 2 || backend.callCount() != 2 || issued[1].scanCalls.Load() != 2 {
		t.Fatalf("All after First = %d, %v, queries=%d", len(all), err, backend.callCount())
	}
	if limit, _ := backend.plans[1].Limit(); limit != 3 {
		t.Fatalf("First changed original limit to %d", limit)
	}
	all[0].source.Title = "caller mutation"
	warm, found, err := eager.First(ctx)
	if err != nil || !found || warm.source.Title != "Alpha" || warm == all[0] || warm.related == all[0].related {
		t.Fatalf("warm First shared result ownership: %v, %v, %v", warm, found, err)
	}
	author, found, err := warm.related.Get(ctx)
	if err != nil || !found || author.Name != "Ada" || backend.callCount() != 2 {
		t.Fatalf("warm relation = %+v, %v, %v, queries=%d", author, found, err, backend.callCount())
	}
}

func TestForwardSelectFirstEmptyAndZeroLimit(t *testing.T) {
	for _, zeroLimit := range []bool{false, true} {
		backend := &selectRelatedBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
			if limit, _ := plan.Limit(); zeroLimit && limit != 0 {
				t.Fatalf("First widened Limit(0) to %d", limit)
			}
			return &selectRelatedRows{}, nil
		}}
		eager := orderedRequiredSelectQuery(t, backend)
		if zeroLimit {
			eager.plan, _ = eager.plan.WithLimit(0)
		}
		for call := 0; call < 2; call++ {
			if value, found, err := eager.First(context.Background()); value != nil || found || err != nil {
				t.Fatalf("empty First = %v, %v, %v", value, found, err)
			}
		}
		if backend.callCount() != 2 {
			t.Fatal("cold empty First was cached")
		}
		if _, err := eager.All(context.Background()); err != nil {
			t.Fatal(err)
		}
		if value, found, err := eager.First(context.Background()); value != nil || found || err != nil || backend.callCount() != 3 {
			t.Fatalf("warm empty First = %v, %v, %v", value, found, err)
		}
	}
}

func TestForwardSelectFirstValidationAndContextPrecedeIO(t *testing.T) {
	backend := &selectRelatedBackend{}
	if _, _, err := requiredSelectQuery(t, backend).First(context.Background()); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnorderedQuery}) {
		t.Fatalf("unordered First = %v", err)
	}
	cause := errors.New("binding failure")
	eager := orderedRequiredSelectQuery(t, backend).WithConfigurationError(cause).WithConfigurationError(nil)
	if _, _, err := eager.First(context.Background()); err != cause {
		t.Fatalf("stored cause = %v", err)
	}
	if _, _, err := eager.First(nil); err == cause || err == nil {
		t.Fatalf("nil context precedence = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := eager.First(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation precedence = %v", err)
	}
	if backend.callCount() != 0 {
		t.Fatal("invalid First performed I/O")
	}
	backend.query = func(int, context.Context, query.Plan) (db.Rows, error) { return nil, nil }
	if value, found, err := orderedRequiredSelectQuery(t, backend).First(context.Background()); value != nil || found || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("nil backend rows = %v, %v, %v", value, found, err)
	}
}

func TestForwardSelectFirstFailuresCloseRowsAndPermitRetry(t *testing.T) {
	scanErr, rowsErr, closeErr, backendErr := errors.New("scan"), errors.New("rows"), errors.New("close"), errors.New("backend")
	for _, test := range []struct {
		name       string
		configure  func(*selectRelatedRows, context.CancelFunc)
		backendErr error
		want       []error
	}{
		{"scan rows close", func(r *selectRelatedRows, _ context.CancelFunc) {
			r.scanErr, r.rowsErr, r.closeErr = scanErr, rowsErr, closeErr
		}, nil, []error{scanErr, rowsErr, closeErr}},
		{"cancel after scan", func(r *selectRelatedRows, cancel context.CancelFunc) { r.afterScan, r.closeErr = cancel, closeErr }, nil, []error{context.Canceled, closeErr}},
		{"backend returned rows", func(r *selectRelatedRows, _ context.CancelFunc) { r.closeErr = closeErr }, backendErr, []error{backendErr, closeErr}},
		{"required absent", func(r *selectRelatedRows, _ context.CancelFunc) { r.values[0].target = nil }, nil, []error{&query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectProjection}}},
		{"target mismatch", func(r *selectRelatedRows, _ context.CancelFunc) { r.values[0].target.ID = 2 }, nil, []error{&query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectProjection}}},
		{"close precedes integrity", func(r *selectRelatedRows, _ context.CancelFunc) { r.values[0].target, r.closeErr = nil, closeErr }, nil, []error{closeErr}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failed := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}
			test.configure(failed, cancel)
			backend := &selectRelatedBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return failed, test.backendErr
				}
				return &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}, nil
			}}
			eager := orderedRequiredSelectQuery(t, backend)
			value, found, err := eager.First(ctx)
			if value != nil || found {
				t.Fatal("failed First published a partial object")
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Fatalf("error %v lost %v", err, want)
				}
			}
			if test.name == "close precedes integrity" && errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectProjection}) {
				t.Fatalf("integrity ran before close: %v", err)
			}
			if failed.closeCalls.Load() != 1 {
				t.Fatal("failed rows not closed exactly once")
			}
			if _, ready := eager.evaluation.cachedValues(); ready {
				t.Fatal("failed First populated cache")
			}
			if _, found, err := eager.First(context.Background()); err != nil || !found || backend.callCount() != 2 {
				t.Fatalf("retry First = %v, %v", found, err)
			}
		})
	}
}
