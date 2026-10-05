package orm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEvaluationInvalidationPreventsOldFlightPublication(t *testing.T) {
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			state := newEvaluationState[int]()
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			type result struct {
				values []int
				graph  any
				err    error
			}
			owner, waiter := make(chan result, 1), make(chan result, 1)
			failure := errors.New("read failed before invalidation")
			go func() {
				values, graph, err := state.evaluateAttached(t.Context(), func(context.Context) ([]int, any, error) {
					close(started)
					<-release
					if mode == "failure" {
						return nil, nil, failure
					}
					return []int{1}, "old graph", nil
				})
				owner <- result{values, graph, err}
			}()
			awaitSignal(t, started, "old evaluation")
			state.invalidate()
			waitContext, entered := newEnteredContext(t.Context())
			var calls atomic.Int64
			go func() {
				values, graph, err := state.evaluateAttached(waitContext, func(context.Context) ([]int, any, error) {
					calls.Add(1)
					return []int{2}, "new graph", nil
				})
				waiter <- result{values, graph, err}
			}()
			// Done is called only after the new reader has joined the old
			// flight. Neither scheduler timing nor a sleep selects the race.
			awaitSignal(t, entered, "reader after invalidation")
			unblock()
			first := awaitValue(t, owner, "old owner completion")
			if mode == "failure" {
				if !errors.Is(first.err, failure) || first.values != nil || first.graph != nil {
					t.Fatal("old failure changed", first)
				}
			} else if first.err != nil || len(first.values) != 1 || first.values[0] != 1 || first.graph != "old graph" {
				t.Fatal("original reader lost its snapshot", first)
			}
			fresh := awaitValue(t, waiter, "current reader completion")
			if fresh.err != nil || len(fresh.values) != 1 || fresh.values[0] != 2 || fresh.graph != "new graph" || calls.Load() != 1 {
				t.Fatal("old result or error poisoned the current generation", fresh, calls.Load())
			}
			if values, graph, ready := state.cachedResult(); !ready || len(values) != 1 || values[0] != 2 || graph != "new graph" {
				t.Fatal("current values and graph were not cached together", values, graph, ready)
			}
			state.invalidate()
			if values, graph, ready := state.cachedResult(); ready || values != nil || graph != nil {
				t.Fatal("cached values and graph were not invalidated together")
			}
		})
	}
}
