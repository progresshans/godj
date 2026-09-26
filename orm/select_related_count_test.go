package orm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestForwardSelectCountPreservesSourceAndOnlyReusesCompleteEagerCache(t *testing.T) {
	ctx := t.Context()
	backend := &selectRelatedBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		if plan.ResultShape().IsCountAll() {
			return &resultTestRows{values: [][]any{{int64(2)}}}, nil
		}
		return &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue(), selectRelatedRequiredValue()}}, nil
	}}
	eager := requiredSelectQuery(t, backend)
	id := NewAutoField[relationObjectTestPost](relationObjectTestPostDescriptor{}.Metadata().Fields[0])
	eager.plan, _ = eager.plan.WithWhere(id.GreaterThan(3).expression)
	eager.plan = eager.plan.WithOrderings(id.Desc().ordering).WithDistinct()
	eager.plan, _ = eager.plan.WithOffset(1)
	eager.plan, _ = eager.plan.WithLimit(2)
	before := eager.Plan()
	for call := 1; call <= 2; call++ {
		if got, err := eager.Count(ctx); err != nil || got != 2 || backend.callCount() != call {
			t.Fatalf("cold Count = %d, %v; queries=%d", got, err, backend.callCount())
		}
		if _, ready := eager.evaluation.cachedValues(); ready {
			t.Fatal("cold Count populated eager cache")
		}
	}
	want, err := before.WithoutRelationProjections().WithResultShape(mustCountShape(t))
	if err != nil || !backend.plans[0].Equal(want) || !eager.Plan().Equal(before) {
		t.Fatalf("Count changed the source or retained eager columns: %v", err)
	}
	if _, err := eager.All(ctx); err != nil {
		t.Fatal(err)
	}
	copy := eager
	if got, err := copy.Count(ctx); err != nil || got != 2 || backend.callCount() != 3 {
		t.Fatalf("warm Count = %d, %v; queries=%d", got, err, backend.callCount())
	}
	// An independently evaluated ordinary source is not this eager snapshot.
	source := newQuerySet[relationObjectTestPost](backend, eager.sourceDescriptor, before.WithoutRelationProjections())
	if _, err := source.evaluation.evaluate(ctx, func(context.Context) ([]relationObjectTestPost, error) {
		return []relationObjectTestPost{{ID: 1}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	separate := (RelatedSelect[relationObjectTestPost, relationObjectTestAuthor]{state: eager.targets[0].(preparedRelatedTarget[relationObjectTestPost, relationObjectTestAuthor]).state}).Select(source)
	if got, err := separate.Count(ctx); err != nil || got != 2 || backend.callCount() != 4 {
		t.Fatalf("eager Count borrowed ordinary source cache: %d, %v", got, err)
	}
}

func mustCountShape(t *testing.T) query.ResultShape {
	t.Helper()
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func TestForwardSelectCountContextConfigurationAndWarmEmpty(t *testing.T) {
	backend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &selectRelatedRows{}, nil
	}}
	eager := requiredSelectQuery(t, backend)
	if _, err := eager.All(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := eager.Count(t.Context()); err != nil || got != 0 || backend.callCount() != 1 {
		t.Fatalf("warm empty Count = %d, %v", got, err)
	}
	cause := errors.New("binding")
	invalid := eager.WithConfigurationError(cause)
	if _, err := invalid.Count(t.Context()); !errors.Is(err, cause) {
		t.Fatalf("binding failure lost: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, q := range []RelatedSelectQuery[relationObjectTestPost]{eager, invalid, {}} {
		if got, err := q.Count(ctx); got != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation precedence = %d, %v", got, err)
		}
		for _, nilContext := range []context.Context{nil, (*resultTestTypedNilContext)(nil)} {
			if got, err := q.Count(nilContext); got != 0 || err == nil || errors.Is(err, cause) {
				t.Fatalf("nil context precedence = %d, %v", got, err)
			}
		}
	}
	if backend.callCount() != 1 {
		t.Fatal("invalid or warm Count performed I/O")
	}
}

func TestForwardSelectCountFailuresCloseRowsAndPermitRetry(t *testing.T) {
	scanErr, rowsErr, closeErr, backendErr := errors.New("scan"), errors.New("rows"), errors.New("close"), errors.New("backend")
	for _, test := range []struct {
		name       string
		configure  func(*resultTestRows, context.CancelFunc)
		backendErr error
		want       []error
	}{
		{"scan rows close", func(r *resultTestRows, _ context.CancelFunc) {
			r.scanErr, r.iterationErr, r.closeErr = scanErr, rowsErr, closeErr
		}, nil, []error{scanErr, rowsErr, closeErr}},
		{"cancel during scan", func(r *resultTestRows, cancel context.CancelFunc) {
			r.onNext = func(int) { cancel() }
			r.closeErr = closeErr
		}, nil, []error{context.Canceled, closeErr}},
		{"backend with rows", func(r *resultTestRows, _ context.CancelFunc) { r.closeErr = closeErr }, backendErr, []error{backendErr, closeErr}},
		{"missing aggregate", func(r *resultTestRows, _ context.CancelFunc) { r.values = nil }, nil, []error{&query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}}},
		{"duplicate aggregate", func(r *resultTestRows, _ context.CancelFunc) { r.values = append(r.values, []any{int64(3)}) }, nil, []error{&query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failed := &resultTestRows{values: [][]any{{int64(2)}}}
			test.configure(failed, cancel)
			backend := &selectRelatedBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return failed, test.backendErr
				}
				return &resultTestRows{values: [][]any{{int64(2)}}}, nil
			}}
			eager := requiredSelectQuery(t, backend)
			got, err := eager.Count(ctx)
			if got != 0 {
				t.Fatal("failed Count returned a value")
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Fatalf("%v lost %v", err, want)
				}
			}
			if failed.closeCalls != 1 {
				t.Fatalf("close calls = %d", failed.closeCalls)
			}
			if _, ready := eager.evaluation.cachedValues(); ready {
				t.Fatal("failure populated cache")
			}
			if got, err := eager.Count(t.Context()); got != 2 || err != nil {
				t.Fatalf("retry = %d, %v", got, err)
			}
		})
	}
}

func TestForwardSelectColdCountDoesNotWaitForRunningAll(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	backend := &selectRelatedBackend{query: func(_ int, ctx context.Context, plan query.Plan) (db.Rows, error) {
		if plan.ResultShape().IsCountAll() {
			return &resultTestRows{values: [][]any{{int64(1)}}}, nil
		}
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}, nil
	}}
	eager := requiredSelectQuery(t, backend)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := eager.All(ctx); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("All did not enter backend")
	}
	countContext, countCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer countCancel()
	if got, err := eager.Count(countContext); err != nil || got != 1 {
		t.Fatalf("Count blocked on All: %d, %v", got, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("All leaked")
	}
}
