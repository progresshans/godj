package orm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestQuerySetAllCachesSuccessAndDeepClonesEveryBoundary(t *testing.T) {
	note := "from backend"
	rows := &cacheTestRows{values: []cacheTestModel{{ID: 1, Note: &note}}}
	backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		if call != 0 {
			return nil, fmt.Errorf("unexpected backend call %d", call)
		}
		return rows, nil
	}}
	querySet := newCacheTestManager().Using(backend)
	directCopy := querySet

	first, err := querySet.All(context.Background())
	if err != nil {
		t.Fatalf("first All() error = %v", err)
	}
	if len(first) != 1 || first[0].Note == nil || *first[0].Note != "from backend" {
		t.Fatalf("first All() = %#v", first)
	}
	first[0].ID = 99
	*first[0].Note = "caller mutation"
	first = append(first, cacheTestModel{ID: 2})
	note = "backend alias mutation"

	second, err := directCopy.All(context.Background())
	if err != nil {
		t.Fatalf("direct-copy All() error = %v", err)
	}
	if len(second) != 1 || second[0].ID != 1 || second[0].Note == nil || *second[0].Note != "from backend" {
		t.Fatalf("cached All() was aliased: %#v", second)
	}
	*second[0].Note = "second caller mutation"
	third, err := querySet.All(context.Background())
	if err != nil {
		t.Fatalf("third All() error = %v", err)
	}
	if third[0].Note == second[0].Note || *third[0].Note != "from backend" {
		t.Fatalf("warm callers shared nullable pointer: second=%p third=%p value=%#v", second[0].Note, third[0].Note, third)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("backend calls = %d, want 1", got)
	}

	t.Run("empty success is cached", func(t *testing.T) {
		emptyBackend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return &cacheTestRows{}, nil
		}}
		empty := newCacheTestManager().Using(emptyBackend)
		for index := 0; index < 2; index++ {
			values, err := empty.All(context.Background())
			if err != nil || values == nil || len(values) != 0 {
				t.Fatalf("empty All() = (%#v, %v), want non-nil empty success", values, err)
			}
		}
		if got := emptyBackend.callCount(); got != 1 {
			t.Fatalf("empty backend calls = %d, want 1", got)
		}
	})
}

func TestQuerySetDirectCopySharesStateWhileEverySuccessfulChainGetsFreshState(t *testing.T) {
	backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		note := fmt.Sprintf("call-%d", call+1)
		return &cacheTestRows{values: []cacheTestModel{{ID: int64(call + 1), Note: &note}}}, nil
	}}
	base := newCacheTestManager().Using(backend)
	assertCacheTestID(t, base, 1)
	directCopy := base
	assertCacheTestID(t, directCopy, 1)

	// Even zero-argument transformations are independent logical handles.
	assertCacheTestID(t, base.Filter(), 2)
	assertCacheTestID(t, base.OrderBy(), 3)
	limited, err := base.Limit(10)
	if err != nil {
		t.Fatalf("Limit() error = %v", err)
	}
	assertCacheTestID(t, limited, 4)
	sameLimit, err := limited.Limit(10)
	if err != nil {
		t.Fatalf("same Limit() error = %v", err)
	}
	assertCacheTestID(t, sameLimit, 5)
	assertCacheTestID(t, base.Fresh(), 6)
	assertCacheTestID(t, base, 1)
	if got := backend.callCount(); got != 6 {
		t.Fatalf("backend calls = %d, want 6", got)
	}
}

func TestQuerySetAllDirectCopyConcurrentSingleflightAndCallerIsolation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	backend := &cacheTestBackend{query: func(call int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		if call != 0 {
			return nil, fmt.Errorf("duplicate backend call %d", call)
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		note := "canonical"
		return &cacheTestRows{values: []cacheTestModel{{ID: 7, Note: &note}}}, nil
	}}
	querySet := newCacheTestManager().Using(backend)

	const callers = 32
	type result struct {
		pointer *string
		err     error
	}
	results := make(chan result, callers)
	run := func(copyOfQuerySet QuerySet[cacheTestModel], ctx context.Context, value int) {
		models, err := copyOfQuerySet.All(ctx)
		if err != nil {
			results <- result{err: err}
			return
		}
		if len(models) != 1 || models[0].Note == nil || *models[0].Note != "canonical" {
			results <- result{err: fmt.Errorf("models = %#v", models)}
			return
		}
		pointer := models[0].Note
		*models[0].Note = fmt.Sprintf("caller-%d", value)
		results <- result{pointer: pointer}
	}
	go run(querySet, context.Background(), 0)
	awaitSignal(t, started, "owner backend start")
	entered := make([]<-chan struct{}, 0, callers-1)
	for index := 1; index < callers; index++ {
		copyOfQuerySet := querySet
		waiterCtx, waiterEntered := newEnteredContext(context.Background())
		entered = append(entered, waiterEntered)
		go run(copyOfQuerySet, waiterCtx, index)
	}
	for _, waiterEntered := range entered {
		awaitEntered(t, waiterEntered)
	}
	close(release)
	seen := make(map[*string]struct{}, callers)
	for index := 0; index < callers; index++ {
		result := awaitValue(t, results, "singleflight result")
		if result.err != nil {
			t.Errorf("All() error = %v", result.err)
			continue
		}
		if _, exists := seen[result.pointer]; exists {
			t.Errorf("two callers received the same nullable pointer %p", result.pointer)
		}
		seen[result.pointer] = struct{}{}
	}
	if len(seen) != callers {
		t.Fatalf("isolated pointer count = %d, want %d", len(seen), callers)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("backend calls = %d, want 1", got)
	}
}

func TestQuerySetAllFailureAndCancellationStateTransitions(t *testing.T) {
	t.Run("general owner error is shared by waiters and later retried", func(t *testing.T) {
		failure := errors.New("owner failure")
		started := make(chan struct{})
		release := make(chan struct{})
		backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				close(started)
				<-release
				return nil, failure
			}
			return rowsForIDs(9), nil
		}}
		querySet := newCacheTestManager().Using(backend)
		ownerResult := make(chan error, 1)
		go func() {
			_, err := querySet.All(context.Background())
			ownerResult <- err
		}()
		awaitSignal(t, started, "owner backend start")
		waiterResult := make(chan error, 1)
		waiterCtx, waiterEntered := newEnteredContext(context.Background())
		go func() {
			_, err := querySet.All(waiterCtx)
			waiterResult <- err
		}()
		awaitEntered(t, waiterEntered)
		close(release)
		if err := awaitValue(t, ownerResult, "owner failure result"); !errors.Is(err, failure) {
			t.Fatalf("owner error = %v, want %v", err, failure)
		}
		if err := awaitValue(t, waiterResult, "waiter failure result"); !errors.Is(err, failure) {
			t.Fatalf("waiter error = %v, want shared %v", err, failure)
		}
		assertCacheTestID(t, querySet, 9)
		assertCacheTestID(t, querySet, 9)
		if got := backend.callCount(); got != 2 {
			t.Fatalf("backend calls = %d, want failure + retry = 2", got)
		}
	})

	t.Run("owner cancellation lets a live waiter retry", func(t *testing.T) {
		started := make(chan struct{})
		backend := &cacheTestBackend{query: func(call int, ctx context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return rowsForIDs(12), nil
		}}
		querySet := newCacheTestManager().Using(backend)
		ownerCtx, cancelOwner := context.WithCancel(context.Background())
		ownerResult := make(chan error, 1)
		go func() {
			_, err := querySet.All(ownerCtx)
			ownerResult <- err
		}()
		awaitSignal(t, started, "canceled owner backend start")
		waiterResult := make(chan struct {
			values []cacheTestModel
			err    error
		}, 1)
		waiterCtx, waiterEntered := newEnteredContext(context.Background())
		go func() {
			values, err := querySet.All(waiterCtx)
			waiterResult <- struct {
				values []cacheTestModel
				err    error
			}{values: values, err: err}
		}()
		awaitEntered(t, waiterEntered)
		cancelOwner()
		if err := awaitValue(t, ownerResult, "canceled owner result"); !errors.Is(err, context.Canceled) {
			t.Fatalf("owner error = %v, want context.Canceled", err)
		}
		waiter := awaitValue(t, waiterResult, "retrying waiter result")
		if waiter.err != nil || len(waiter.values) != 1 || waiter.values[0].ID != 12 {
			t.Fatalf("live waiter = (%#v, %v), want retried success", waiter.values, waiter.err)
		}
		if got := backend.callCount(); got != 2 {
			t.Fatalf("backend calls = %d, want canceled owner + retry", got)
		}
	})

	t.Run("owner cancellation joined with close lets a live waiter retry", func(t *testing.T) {
		closeFailure := errors.New("canceled owner close failure")
		ownerReachedEnd := make(chan struct{})
		releaseOwner := make(chan struct{})
		ownerCtx, cancelOwner := context.WithCancel(context.Background())
		defer cancelOwner()
		ownerRows := &resultTestRows{
			values:   [][]any{{int64(11), nil}},
			closeErr: closeFailure,
			onNext: func(call int) {
				if call == 2 {
					close(ownerReachedEnd)
					<-releaseOwner
					cancelOwner()
				}
			},
		}
		backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				return ownerRows, nil
			}
			return rowsForIDs(12), nil
		}}
		querySet := newCacheTestManager().Using(backend)
		ownerResult := make(chan error, 1)
		go func() {
			_, err := querySet.All(ownerCtx)
			ownerResult <- err
		}()
		awaitSignal(t, ownerReachedEnd, "canceled owner final Next")

		waiterResult := make(chan struct {
			values []cacheTestModel
			err    error
		}, 1)
		waiterCtx, waiterEntered := newEnteredContext(context.Background())
		go func() {
			values, err := querySet.All(waiterCtx)
			waiterResult <- struct {
				values []cacheTestModel
				err    error
			}{values: values, err: err}
		}()
		awaitEntered(t, waiterEntered)
		close(releaseOwner)

		ownerErr := awaitValue(t, ownerResult, "canceled owner close result")
		for _, want := range []error{context.Canceled, closeFailure} {
			if !errors.Is(ownerErr, want) {
				t.Errorf("owner error %v does not preserve %v", ownerErr, want)
			}
		}
		if ownerRows.closeCalls != 1 {
			t.Fatalf("canceled owner Close() calls = %d, want 1", ownerRows.closeCalls)
		}
		waiter := awaitValue(t, waiterResult, "retrying waiter after close failure")
		if waiter.err != nil || len(waiter.values) != 1 || waiter.values[0].ID != 12 {
			t.Fatalf("live waiter = (%#v, %v), want retried success", waiter.values, waiter.err)
		}
		if got := backend.callCount(); got != 2 {
			t.Fatalf("backend calls = %d, want canceled owner + retry", got)
		}
	})

	t.Run("waiter cancellation does not cancel owner", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		backend := &cacheTestBackend{query: func(_ int, ctx context.Context, _ query.Plan) (db.Rows, error) {
			close(started)
			select {
			case <-release:
				return rowsForIDs(15), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}
		querySet := newCacheTestManager().Using(backend)
		ownerResult := make(chan error, 1)
		go func() {
			_, err := querySet.All(context.Background())
			ownerResult <- err
		}()
		awaitSignal(t, started, "owner backend start")
		waiterBaseCtx, cancelWaiter := context.WithCancel(context.Background())
		waiterCtx, waiterEntered := newEnteredContext(waiterBaseCtx)
		waiterResult := make(chan error, 1)
		go func() {
			_, err := querySet.All(waiterCtx)
			waiterResult <- err
		}()
		awaitEntered(t, waiterEntered)
		cancelWaiter()
		if err := awaitValue(t, waiterResult, "canceled waiter result"); !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error = %v, want context.Canceled", err)
		}
		select {
		case err := <-ownerResult:
			t.Fatalf("waiter cancellation completed owner early: %v", err)
		default:
		}
		close(release)
		if err := awaitValue(t, ownerResult, "owner completion"); err != nil {
			t.Fatalf("owner error = %v", err)
		}
		assertCacheTestID(t, querySet, 15)
		if got := backend.callCount(); got != 1 {
			t.Fatalf("backend calls = %d, want 1", got)
		}
	})

	t.Run("canceled waiter cannot claim a new flight", func(t *testing.T) {
		backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return rowsForIDs(1), nil
		}}
		querySet := newCacheTestManager().Using(backend)
		flight := &evaluationFlight{done: make(chan struct{}), err: context.Canceled}
		querySet.evaluation.flight = flight
		ctx, entered := newThirdErrCancellationContext()
		result := make(chan error, 1)
		go func() {
			_, err := querySet.All(ctx)
			result <- err
		}()
		awaitEntered(t, entered)
		querySet.evaluation.mu.Lock()
		querySet.evaluation.flight = nil
		close(flight.done)
		querySet.evaluation.mu.Unlock()
		err := awaitValue(t, result, "canceled waiter result")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("All() error = %v, want context.Canceled", err)
		}
		if got := backend.callCount(); got != 0 {
			t.Fatalf("canceled waiter started %d backend call(s)", got)
		}
	})
}

func TestQuerySetColdAndWarmTerminalSemantics(t *testing.T) {
	noteOne, noteTwo, noteThree := "one", "two", "three"
	values := []cacheTestModel{{ID: 1, Note: &noteOne}, {ID: 2, Note: &noteTwo}, {ID: 3, Note: &noteThree}}
	var rowsMu sync.Mutex
	var issuedRows []*cacheTestRows
	var aggregateRows *resultTestRows
	backend := &cacheTestBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		limitedValues := values
		if limit, ok := plan.Limit(); ok && limit < len(limitedValues) {
			limitedValues = limitedValues[:limit]
		}
		if plan.ResultShape().Kind() == query.ResultAggregate {
			rows := &resultTestRows{values: [][]any{{int64(len(limitedValues))}}}
			rowsMu.Lock()
			aggregateRows = rows
			rowsMu.Unlock()
			return rows, nil
		}
		rows := &cacheTestRows{values: append([]cacheTestModel(nil), limitedValues...)}
		rowsMu.Lock()
		issuedRows = append(issuedRows, rows)
		rowsMu.Unlock()
		return rows, nil
	}}
	metadata := (cacheTestDescriptor{}).Metadata()
	ordered := newCacheTestManager().Using(backend).OrderBy(NewIntegerField[cacheTestModel](metadata.Fields[0]).Asc())
	ctx := context.Background()

	count, err := ordered.Count(ctx)
	if err != nil || count != 3 {
		t.Fatalf("cold Count() = (%d, %v), want (3, nil)", count, err)
	}
	rowsMu.Lock()
	countRows := aggregateRows
	rowsMu.Unlock()
	if countRows == nil || countRows.scanCalls != 1 || countRows.closeCalls != 1 {
		t.Fatalf("cold Count() aggregate lifecycle = %#v, want one scalar scan and close", countRows)
	}
	exists, err := ordered.Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("cold Exists() = (%v, %v), want true", exists, err)
	}
	at, ok, err := ordered.At(ctx, 1)
	if err != nil || !ok || at.ID != 2 {
		t.Fatalf("cold At(1) = (%#v, %v, %v)", at, ok, err)
	}
	first, ok, err := ordered.First(ctx)
	if err != nil || !ok || first.ID != 1 {
		t.Fatalf("cold First() = (%#v, %v, %v)", first, ok, err)
	}
	var iterated []int64
	if err := ordered.Iterate(ctx, func(value cacheTestModel) error {
		iterated = append(iterated, value.ID)
		return nil
	}); err != nil {
		t.Fatalf("cold Iterate() error = %v", err)
	}
	if fmt.Sprint(iterated) != "[1 2 3]" {
		t.Fatalf("Iterate() IDs = %v", iterated)
	}
	if got := backend.callCount(); got != 5 {
		t.Fatalf("cold terminal backend calls = %d, want 5", got)
	}
	countShape := backend.plan(0).ResultShape()
	countExpressions := countShape.Expressions()
	if countShape.Kind() != query.ResultAggregate || len(countExpressions) != 1 ||
		!countExpressions[0].Equal(query.CountAllResult()) {
		t.Fatalf("cold Count() result shape = %#v, want COUNT(*) aggregate", countShape)
	}
	assertPlanLimit(t, backend.plan(0), 0, false)
	assertPlanLimit(t, backend.plan(1), 1, true)
	assertPlanLimit(t, backend.plan(2), 2, true)
	assertPlanLimit(t, backend.plan(3), 1, true)
	assertPlanLimit(t, backend.plan(4), 0, false)

	all, err := ordered.All(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("All() = (%#v, %v)", all, err)
	}
	if got := backend.callCount(); got != 6 {
		t.Fatalf("All() backend calls = %d, want 6", got)
	}
	count, err = ordered.Count(ctx)
	if err != nil || count != 3 {
		t.Fatalf("warm Count() = (%d, %v)", count, err)
	}
	exists, err = ordered.Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("warm Exists() = (%v, %v)", exists, err)
	}
	at, ok, err = ordered.At(ctx, 1)
	if err != nil || !ok || at.ID != 2 {
		t.Fatalf("warm At() = (%#v, %v, %v)", at, ok, err)
	}
	*at.Note = "warm caller mutation"
	atAgain, ok, err := ordered.At(ctx, 1)
	if err != nil || !ok || atAgain.Note == nil || *atAgain.Note != "two" || atAgain.Note == at.Note {
		t.Fatalf("warm At() alias isolation = (%#v, %v, %v), previous pointer %p", atAgain, ok, err, at.Note)
	}
	first, ok, err = ordered.First(ctx)
	if err != nil || !ok || first.ID != 1 {
		t.Fatalf("warm First() = (%#v, %v, %v)", first, ok, err)
	}
	if got := backend.callCount(); got != 6 {
		t.Fatalf("warm terminals performed backend I/O: %d calls", got)
	}
	if err := ordered.Iterate(ctx, func(cacheTestModel) error { return nil }); err != nil {
		t.Fatalf("warm Iterate() error = %v", err)
	}
	if got := backend.callCount(); got != 7 {
		t.Fatalf("warm Iterate() calls = %d, want cache bypass call 7", got)
	}
	if values, err := ordered.All(ctx); err != nil || len(values) != 3 {
		t.Fatalf("cache after Iterate() = (%#v, %v)", values, err)
	}
	if got := backend.callCount(); got != 7 {
		t.Fatalf("Iterate() replaced cache: calls = %d", got)
	}

	limited, err := ordered.Fresh().Limit(0)
	if err != nil {
		t.Fatalf("Limit(0) error = %v", err)
	}
	if exists, err := limited.Exists(ctx); err != nil || exists {
		t.Fatalf("Limit(0) Exists() = (%v, %v), want false", exists, err)
	}
	if _, ok, err := limited.At(ctx, 0); err != nil || ok {
		t.Fatalf("Limit(0) At() = (ok=%v, err=%v), want not found", ok, err)
	}
	if _, ok, err := limited.First(ctx); err != nil || ok {
		t.Fatalf("Limit(0) First() = (ok=%v, err=%v), want not found", ok, err)
	}
	for index := 7; index < 10; index++ {
		assertPlanLimit(t, backend.plan(index), 0, true)
	}
}

func TestQuerySetTerminalValidationAndRowsErrorsDoNotProduceFalseCache(t *testing.T) {
	t.Run("contexts are checked before warm cache", func(t *testing.T) {
		backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return rowsForIDs(1), nil
		}}
		metadata := (cacheTestDescriptor{}).Metadata()
		querySet := newCacheTestManager().Using(backend).OrderBy(NewIntegerField[cacheTestModel](metadata.Fields[0]).Asc())
		if _, err := querySet.All(context.Background()); err != nil {
			t.Fatalf("populate All() error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assertCanceledTerminals(t, querySet, ctx)
		if got := backend.callCount(); got != 1 {
			t.Fatalf("canceled warm terminal backend calls = %d, want 1", got)
		}
	})

	t.Run("invalid terminal arguments perform no IO", func(t *testing.T) {
		backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return rowsForIDs(1), nil
		}}
		unordered := newCacheTestManager().Using(backend)
		if _, _, err := unordered.At(context.Background(), -1); !errors.Is(err, &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidIndex}) {
			t.Fatalf("At(-1) error = %v", err)
		}
		if _, _, err := unordered.At(context.Background(), 0); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnorderedQuery}) {
			t.Fatalf("unordered At() error = %v", err)
		}
		if _, _, err := unordered.First(context.Background()); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnorderedQuery}) {
			t.Fatalf("unordered First() error = %v", err)
		}
		if err := unordered.Iterate(context.Background(), nil); !errors.Is(err, &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidValue}) {
			t.Fatalf("nil Iterate() error = %v", err)
		}
		if _, err := unordered.All(nil); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
			t.Fatalf("nil-context All() error = %v", err)
		}
		if got := backend.callCount(); got != 0 {
			t.Fatalf("invalid terminal backend calls = %d", got)
		}
	})

	t.Run("scan rows and close errors are joined and retryable", func(t *testing.T) {
		scanErr := errors.New("scan failure")
		rowsErr := errors.New("rows failure")
		closeErr := errors.New("close failure")
		firstRows := &cacheTestRows{values: []cacheTestModel{{ID: 1}}, scanErr: scanErr, rowsErr: rowsErr, closeErr: closeErr}
		backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				return firstRows, nil
			}
			return rowsForIDs(2), nil
		}}
		querySet := newCacheTestManager().Using(backend)
		_, err := querySet.All(context.Background())
		for _, target := range []error{scanErr, rowsErr, closeErr} {
			if !errors.Is(err, target) {
				t.Errorf("All() error %v does not preserve %v", err, target)
			}
		}
		if firstRows.closeCalls.Load() != 1 {
			t.Fatalf("failed rows Close() calls = %d, want 1", firstRows.closeCalls.Load())
		}
		assertCacheTestID(t, querySet, 2)
		assertCacheTestID(t, querySet, 2)
		if got := backend.callCount(); got != 2 {
			t.Fatalf("retry/cache backend calls = %d, want 2", got)
		}
	})

	t.Run("close failure after successful scan is not cached", func(t *testing.T) {
		closeErr := errors.New("close after scan failure")
		failedRows := rowsForIDs(1)
		failedRows.closeErr = closeErr
		backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				return failedRows, nil
			}
			return rowsForIDs(2), nil
		}}
		querySet := newCacheTestManager().Using(backend)
		if _, err := querySet.All(context.Background()); !errors.Is(err, closeErr) {
			t.Fatalf("first All() error = %v, want close failure", err)
		}
		assertCacheTestID(t, querySet, 2)
		assertCacheTestID(t, querySet, 2)
		if got := backend.callCount(); got != 2 {
			t.Fatalf("close failure was cached: calls = %d, want 2", got)
		}
	})

	t.Run("query error rows are closed and both errors survive", func(t *testing.T) {
		queryErr := errors.New("query failure")
		closeErr := errors.New("query rows close failure")
		rows := &cacheTestRows{closeErr: closeErr}
		backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return rows, queryErr
		}}
		_, err := newCacheTestManager().Using(backend).All(context.Background())
		if !errors.Is(err, queryErr) || !errors.Is(err, closeErr) {
			t.Fatalf("All() error = %v, want joined query and close errors", err)
		}
		if rows.closeCalls.Load() != 1 {
			t.Fatalf("query-error rows Close() calls = %d, want 1", rows.closeCalls.Load())
		}
	})

	t.Run("nil rows success is a backend contract error", func(t *testing.T) {
		backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return nil, nil
		}}
		_, err := newCacheTestManager().Using(backend).All(context.Background())
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatalf("All() error = %v, want backend invalid_plan", err)
		}
	})

	t.Run("iterate preserves callback rows and close errors", func(t *testing.T) {
		callbackErr := errors.New("callback failure")
		rowsErr := errors.New("rows failure")
		closeErr := errors.New("close failure")
		rows := &cacheTestRows{values: []cacheTestModel{{ID: 1}}, rowsErr: rowsErr, closeErr: closeErr}
		backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
		err := newCacheTestManager().Using(backend).Iterate(context.Background(), func(cacheTestModel) error {
			return callbackErr
		})
		for _, target := range []error{callbackErr, rowsErr, closeErr} {
			if !errors.Is(err, target) {
				t.Errorf("Iterate() error %v does not preserve %v", err, target)
			}
		}
		if rows.closeCalls.Load() != 1 {
			t.Fatalf("Iterate rows Close() calls = %d, want 1", rows.closeCalls.Load())
		}
	})

	t.Run("every cold terminal preserves rows and close errors", func(t *testing.T) {
		metadata := (cacheTestDescriptor{}).Metadata()
		ordering := NewIntegerField[cacheTestModel](metadata.Fields[0]).Asc()
		tests := []struct {
			name string
			run  func(QuerySet[cacheTestModel]) error
		}{
			{name: "count", run: func(qs QuerySet[cacheTestModel]) error { _, err := qs.Count(context.Background()); return err }},
			{name: "exists", run: func(qs QuerySet[cacheTestModel]) error { _, err := qs.Exists(context.Background()); return err }},
			{name: "at", run: func(qs QuerySet[cacheTestModel]) error { _, _, err := qs.At(context.Background(), 0); return err }},
			{name: "first", run: func(qs QuerySet[cacheTestModel]) error { _, _, err := qs.First(context.Background()); return err }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				rowsErr := errors.New(test.name + " rows failure")
				closeErr := errors.New(test.name + " close failure")
				modelRows := rowsForIDs(1)
				modelRows.rowsErr = rowsErr
				modelRows.closeErr = closeErr
				aggregateRows := &resultTestRows{
					values:       [][]any{{int64(1)}},
					iterationErr: rowsErr,
					closeErr:     closeErr,
				}
				backend := &cacheTestBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
					if plan.ResultShape().Kind() == query.ResultAggregate {
						return aggregateRows, nil
					}
					return modelRows, nil
				}}
				querySet := newCacheTestManager().Using(backend).OrderBy(ordering)
				err := test.run(querySet)
				if !errors.Is(err, rowsErr) || !errors.Is(err, closeErr) {
					t.Fatalf("terminal error = %v, want joined rows and close errors", err)
				}
				if test.name == "count" {
					if aggregateRows.scanCalls != 1 || aggregateRows.closeCalls != 1 {
						t.Fatalf("Count aggregate rows lifecycle = scan %d close %d, want 1/1", aggregateRows.scanCalls, aggregateRows.closeCalls)
					}
					if modelRows.scanCalls.Load() != 0 || modelRows.closeCalls.Load() != 0 {
						t.Fatalf("Count used model rows: scan %d close %d, want 0/0", modelRows.scanCalls.Load(), modelRows.closeCalls.Load())
					}
				} else if modelRows.closeCalls.Load() != 1 {
					t.Fatalf("Close() calls = %d, want 1", modelRows.closeCalls.Load())
				}
			})
		}
	})
}

type cacheTestModel struct {
	ID   int64
	Note *string
}

type cacheTestDescriptor struct{}

func (cacheTestDescriptor) Metadata() ir.Model {
	return ir.Model{
		Name:    "cache_test_model",
		GoName:  "CacheTestModel",
		DBTable: "cache_test_model",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldChar, Nullable: true},
		},
	}
}

func (cacheTestDescriptor) Scan(row db.Row) (cacheTestModel, error) {
	var value cacheTestModel
	if err := row.Scan(&value.ID, &value.Note); err != nil {
		return cacheTestModel{}, err
	}
	return value, nil
}

func (cacheTestDescriptor) CloneModel(value cacheTestModel) cacheTestModel {
	clone := value
	if value.Note != nil {
		note := *value.Note
		clone.Note = &note
	}
	return clone
}

func newCacheTestManager() Manager[cacheTestModel] {
	return NewManager[cacheTestModel](cacheTestDescriptor{})
}

type cacheTestBackend struct {
	mu    sync.Mutex
	calls []query.Plan
	query func(int, context.Context, query.Plan) (db.Rows, error)
}

func (backend *cacheTestBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.mu.Lock()
	call := len(backend.calls)
	backend.calls = append(backend.calls, plan)
	queryFn := backend.query
	backend.mu.Unlock()
	return queryFn(call, ctx, plan)
}

func (backend *cacheTestBackend) callCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.calls)
}

func (backend *cacheTestBackend) plan(index int) query.Plan {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.calls[index]
}

type cacheTestRows struct {
	values     []cacheTestModel
	position   int
	scanErr    error
	rowsErr    error
	closeErr   error
	scanCalls  atomic.Uint64
	closeCalls atomic.Uint64
}

func (rows *cacheTestRows) Next() bool {
	if rows.position >= len(rows.values) {
		return false
	}
	rows.position++
	return true
}

func (rows *cacheTestRows) Scan(destinations ...any) error {
	rows.scanCalls.Add(1)
	if rows.scanErr != nil {
		return rows.scanErr
	}
	if len(destinations) != 2 {
		return fmt.Errorf("destinations = %d, want 2", len(destinations))
	}
	id, ok := destinations[0].(*int64)
	if !ok {
		return fmt.Errorf("ID destination type = %T, want *int64", destinations[0])
	}
	note, ok := destinations[1].(**string)
	if !ok {
		return fmt.Errorf("Note destination type = %T, want **string", destinations[1])
	}
	value := rows.values[rows.position-1]
	*id = value.ID
	*note = value.Note
	return nil
}

func (rows *cacheTestRows) Err() error { return rows.rowsErr }

func (rows *cacheTestRows) Close() error {
	rows.closeCalls.Add(1)
	return rows.closeErr
}

func rowsForIDs(ids ...int64) *cacheTestRows {
	values := make([]cacheTestModel, len(ids))
	for index, id := range ids {
		values[index] = cacheTestModel{ID: id}
	}
	return &cacheTestRows{values: values}
}

func assertCacheTestID(t *testing.T, querySet QuerySet[cacheTestModel], want int64) {
	t.Helper()
	values, err := querySet.All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(values) != 1 || values[0].ID != want {
		t.Fatalf("All() = %#v, want ID %d", values, want)
	}
}

func assertPlanLimit(t *testing.T, plan query.Plan, want int, wantSet bool) {
	t.Helper()
	got, gotSet := plan.Limit()
	if gotSet != wantSet || gotSet && got != want {
		t.Fatalf("plan limit = (%d, %v), want (%d, %v)", got, gotSet, want, wantSet)
	}
}

func assertCanceledTerminals(t *testing.T, querySet QuerySet[cacheTestModel], ctx context.Context) {
	t.Helper()
	if _, err := querySet.All(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("All() error = %v, want context.Canceled", err)
	}
	if _, err := querySet.Count(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Count() error = %v, want context.Canceled", err)
	}
	if _, err := querySet.Exists(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Exists() error = %v, want context.Canceled", err)
	}
	if _, _, err := querySet.At(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("At() error = %v, want context.Canceled", err)
	}
	if _, _, err := querySet.First(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("First() error = %v, want context.Canceled", err)
	}
	if err := querySet.Iterate(ctx, func(cacheTestModel) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("Iterate() error = %v, want context.Canceled", err)
	}
}

type enteredContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func newEnteredContext(parent context.Context) (*enteredContext, <-chan struct{}) {
	ctx := &enteredContext{Context: parent, entered: make(chan struct{})}
	return ctx, ctx.entered
}

func awaitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	awaitSignal(t, entered, "evaluation-flight waiter entry")
}

func awaitSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func awaitValue[T any](t *testing.T, values <-chan T, label string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		var zero T
		return zero
	}
}

func (ctx *enteredContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
}

type thirdErrCancellationContext struct {
	calls       atomic.Uint64
	done        chan struct{}
	entered     chan struct{}
	doneOnce    sync.Once
	enteredOnce sync.Once
}

func newThirdErrCancellationContext() (*thirdErrCancellationContext, <-chan struct{}) {
	ctx := &thirdErrCancellationContext{done: make(chan struct{}), entered: make(chan struct{})}
	return ctx, ctx.entered
}

func (ctx *thirdErrCancellationContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *thirdErrCancellationContext) Done() <-chan struct{} {
	ctx.enteredOnce.Do(func() { close(ctx.entered) })
	return ctx.done
}
func (ctx *thirdErrCancellationContext) Value(any) any { return nil }
func (ctx *thirdErrCancellationContext) Err() error {
	if ctx.calls.Add(1) < 3 {
		return nil
	}
	ctx.doneOnce.Do(func() { close(ctx.done) })
	return context.Canceled
}
