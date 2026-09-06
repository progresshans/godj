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
	mu     sync.Mutex
	ready  bool
	values []M
	flight *evaluationFlight
}

type evaluationFlight struct {
	done chan struct{}
	err  error
}

func newEvaluationState[M any]() *evaluationState[M] {
	return &evaluationState[M]{}
}

func (state *evaluationState[M]) evaluate(ctx context.Context, load func(context.Context) ([]M, error)) ([]M, error) {
	for {
		// A live waiter may retry a canceled owner. Check its own context again
		// before claiming the next flight, including when both signals are ready.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state.mu.Lock()
		if state.ready {
			values := state.values
			state.mu.Unlock()
			return values, nil
		}
		if flight := state.flight; flight != nil {
			state.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
			}
			if flight.err == nil || errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
				continue
			}
			// Every existing waiter observes the same non-context failure. A
			// later independent evaluation may retry; failures are never cached.
			return nil, flight.err
		}

		flight := &evaluationFlight{done: make(chan struct{})}
		state.flight = flight
		state.mu.Unlock()
		values, err := load(ctx)

		state.mu.Lock()
		if err == nil {
			state.values = values
			state.ready = true
		}
		flight.err = err
		state.flight = nil
		close(flight.done)
		state.mu.Unlock()
		return values, err
	}
}
