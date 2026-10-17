package helpdesk

import (
	"context"
	"errors"
	"sync"

	"github.com/progresshans/godj/db"
)

// readSnapshotValue publishes a value only after its one synchronous callback
// and the snapshot owner's cleanup both succeed. Late callbacks are rejected;
// unfinished reads lose their work context when the owner returns.
func readSnapshotValue[R any](ctx context.Context, backend interface {
	db.Queryer
	db.SnapshotReader
}, read func(context.Context, db.Queryer) (R, error)) (R, error) {
	var zero R
	if ctx == nil || nilFormReader(backend) || read == nil {
		return zero, errors.New("helpdesk: read snapshot requires context, backend and callback")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var result R
	var callbackErr error
	entries, completed, sealed := 0, false, false
	err := backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return errors.New("helpdesk: read snapshot callback outlived its owner")
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return errors.New("helpdesk: repeated read snapshot callback")
		}
		mu.Unlock()
		value, failure := read(work, reader)
		mu.Lock()
		result, callbackErr, completed = value, failure, true
		mu.Unlock()
		return failure
	})
	mu.Lock()
	sealed = true
	valid := entries <= 1 && (entries == 0 && err != nil || completed)
	value, failure := result, callbackErr
	mu.Unlock()
	cancel()
	if !valid || failure != nil && (err == nil || !errors.Is(err, failure)) {
		return zero, errors.Join(errors.New("helpdesk: invalid read snapshot ownership"), err, failure)
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		return zero, err
	}
	return value, nil
}
