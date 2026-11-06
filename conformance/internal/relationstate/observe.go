package relationstate

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/progresshans/godj/db/sqlite"
)

// Observation holds exactly one product operation and its independent actual
// database snapshot. There is no shared result cache between contracts.
type Observation[T any] struct {
	Result  T
	DBState DatabaseState
}

var observationSequence atomic.Uint64

// Observe creates and closes a fresh fixture for one explicitly supplied
// operation. Provisioning and final inspection use the unwrapped backend, so
// the operation's recorder can measure only its own I/O window.
func Observe[T any](ctx context.Context, label string, seed DatabaseState, run func(*sqlite.Backend) (T, error)) (Observation[T], error) {
	if ctx == nil || run == nil {
		return Observation[T]{}, fmt.Errorf("observe %s: context or operation is nil", label)
	}
	backend, err := sqlite.OpenMemory(ctx, fmt.Sprintf("godj-relation-observation-%d", observationSequence.Add(1)))
	if err != nil {
		return Observation[T]{}, fmt.Errorf("open %s SQLite fixture: %w", label, err)
	}
	observation, observeErr := func() (Observation[T], error) {
		if err := Provision(ctx, backend, label, seed.Authors, seed.Posts); err != nil {
			return Observation[T]{}, err
		}
		result, err := run(backend)
		if err != nil {
			return Observation[T]{}, err
		}
		state, err := Read(ctx, backend, label)
		if err != nil {
			return Observation[T]{}, err
		}
		return Observation[T]{Result: result, DBState: state}, nil
	}()
	if err := errors.Join(observeErr, backend.Close()); err != nil {
		return Observation[T]{}, err
	}
	return observation, nil
}
