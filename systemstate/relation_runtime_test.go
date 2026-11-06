package systemstate

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type coordinatedRelationRuntimeBackend struct{ serialRuntimeBackend }

func (b *coordinatedRelationRuntimeBackend) CoordinatedAtomicRelation(ctx context.Context, fn func(db.RelationSession) error) error {
	return b.runAtomic(ctx, func(db.Session) error { return fn(runtimeRelationSession{}) })
}
func (*coordinatedRelationRuntimeBackend) AtomicRelation(context.Context, func(db.RelationSession) error) error {
	return errors.New("uncoordinated fallback must never run")
}

type runtimeRelationSession struct{ serialRuntimeSession }

func (runtimeRelationSession) RelationSetNull(context.Context, query.RelationSetNullPlan) (int64, error) {
	return 1, nil
}

func TestRuntimeRelationAndOrdinaryWritesShareGateAndRejectUnsupportedBackend(t *testing.T) {
	backend := &coordinatedRelationRuntimeBackend{}
	runtime := &Runtime{backend: backend}
	ctx := context.Background()
	var wait sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if i%2 == 0 {
				failures <- runtime.Atomic(ctx, func(db.Session) error { return nil })
			} else {
				failures <- runtime.AtomicRelation(ctx, func(session db.RelationSession) error {
					_, err := session.RelationSetNull(ctx, query.RelationSetNullPlan{})
					return err
				})
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
	if backend.atomicCalls.Load() != 24 || backend.maxActive.Load() != 1 {
		t.Fatal("relation writes bypassed the local cooperative gate", backend.atomicCalls.Load(), backend.maxActive.Load())
	}
	failure := errors.New("confirmed callback rejection")
	calls := 0
	if err := runtime.AtomicRelation(ctx, func(db.RelationSession) error { calls++; return failure }); err != failure || calls != 1 {
		t.Fatal("callback error wrapped or retried", err, calls)
	}
	before := backend.atomicCalls.Load()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	callback := func(db.RelationSession) error { t.Fatal("invalid relation call entered callback"); return nil }
	for _, invoke := range []func() error{func() error { return runtime.AtomicRelation(nil, callback) }, func() error { return runtime.AtomicRelation(ctx, nil) }, func() error { return runtime.AtomicRelation(canceled, callback) }, func() error { return (*Runtime)(nil).AtomicRelation(ctx, callback) }} {
		if err := invoke(); err == nil {
			t.Fatal("invalid relation input accepted")
		}
	}
	if backend.atomicCalls.Load() != before {
		t.Fatal("precondition reached backend")
	}
	unsupported := &serialRuntimeBackend{}
	without := &Runtime{backend: unsupported}
	if err := without.AtomicRelation(ctx, callback); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || unsupported.atomicCalls.Load() != 0 {
		t.Fatal("missing coordinated capability silently fell back", err)
	}
}
