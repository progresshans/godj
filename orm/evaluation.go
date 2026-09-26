package orm

import (
	"context"
	"errors"
	"sync"
)

// evaluationState owns the successful canonical values of one immutable query.
// Callers clone its result before publication. Derived queries get a new state;
// copies of the same query share one flight and the same successful cache.
type evaluationState[M any] struct {
	mu         sync.Mutex
	ready      bool
	values     []M
	attachment any
	flight     *evaluationFlight
}

type evaluationFlight struct {
	done chan struct{}
	err  error
}

func newEvaluationState[M any]() *evaluationState[M] {
	return &evaluationState[M]{}
}

// cachedValues returns canonical values only after a successful full
// evaluation. Callers must clone them before returning them to application code.
func (state *evaluationState[M]) cachedValues() ([]M, bool) {
	values, _, ready := state.cachedResult()
	return values, ready
}

// An attachment is immutable metadata belonging to exactly these canonical
// values. Values and their relation graph are published under the same lock.
func (state *evaluationState[M]) cachedResult() ([]M, any, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.ready {
		return nil, nil, false
	}
	return state.values, state.attachment, true
}

func (state *evaluationState[M]) evaluate(ctx context.Context, load func(context.Context) ([]M, error)) ([]M, error) {
	values, _, err := state.evaluateAttached(ctx, func(ctx context.Context) ([]M, any, error) {
		values, err := load(ctx)
		return values, nil, err
	})
	return values, err
}

func (state *evaluationState[M]) evaluateAttached(ctx context.Context, load func(context.Context) ([]M, any, error)) ([]M, any, error) {
	for {
		// A live waiter may retry a canceled owner. Check its own context again
		// before claiming the next flight, including when both signals are ready.
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		state.mu.Lock()
		if state.ready {
			values := state.values
			attachment := state.attachment
			state.mu.Unlock()
			return values, attachment, nil
		}
		if flight := state.flight; flight != nil {
			state.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-flight.done:
			}
			if flight.err == nil || errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
				continue
			}
			// Every existing waiter observes the same non-context failure. A
			// later independent evaluation may retry; failures are never cached.
			return nil, nil, flight.err
		}

		flight := &evaluationFlight{done: make(chan struct{})}
		state.flight = flight
		state.mu.Unlock()
		return state.evaluateFlight(ctx, flight, load)
	}
}

func (state *evaluationState[M]) evaluateFlight(
	ctx context.Context,
	flight *evaluationFlight,
	load func(context.Context) ([]M, any, error),
) (values []M, attachment any, err error) {
	completed := false
	defer func() {
		state.mu.Lock()
		// A panicking loader still releases its flight, but only a normal,
		// successful return can publish canonical values. Waiters may retry.
		if completed && err == nil {
			state.values = values
			state.attachment = attachment
			state.ready = true
		}
		flight.err = err
		state.flight = nil
		close(flight.done)
		state.mu.Unlock()
	}()
	values, attachment, err = load(ctx)
	completed = true
	return values, attachment, err
}
