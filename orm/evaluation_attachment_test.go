package orm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEvaluationPublishesModelAndGraphInOneFlight(t *testing.T) {
	state := newEvaluationState[int]()
	started, release := make(chan struct{}), make(chan struct{})
	graph := &struct{ row int }{row: 19}
	var calls atomic.Int32
	load := func(context.Context) ([]int, any, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []int{19}, graph, nil
	}
	var callers sync.WaitGroup
	for i := 0; i < 16; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			rows, attachment, err := state.evaluateAttached(t.Context(), load)
			if err != nil || len(rows) != 1 || rows[0] != graph.row || attachment != graph {
				t.Errorf("incomplete publication: %v %v %v", rows, attachment, err)
			}
		}()
	}
	<-started
	if rows, attachment, ready := state.cachedResult(); ready || rows != nil || attachment != nil {
		t.Fatal("incomplete flight exposed a partial graph")
	}
	close(release)
	callers.Wait()
	if calls.Load() != 1 {
		t.Fatal("graph and model values were evaluated independently")
	}
}

func TestEvaluationDoesNotRetainFailedGraph(t *testing.T) {
	for _, failure := range []error{errors.New("child failure"), context.Canceled} {
		state := newEvaluationState[int]()
		_, _, err := state.evaluateAttached(t.Context(), func(context.Context) ([]int, any, error) { return []int{1}, "partial", failure })
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if rows, attachment, ready := state.cachedResult(); ready || rows != nil || attachment != nil {
			t.Fatal("failed graph was cached")
		}
		rows, attachment, err := state.evaluateAttached(t.Context(), func(context.Context) ([]int, any, error) { return []int{2}, "complete", nil })
		if err != nil || len(rows) != 1 || rows[0] != 2 || attachment != "complete" {
			t.Fatal("retry retained failed graph", err)
		}
	}
}
