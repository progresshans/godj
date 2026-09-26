package readscope

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type sourceStub struct{ live bool }

func (value *sourceStub) Query(ctx context.Context, _ query.Plan) (db.Rows, error) {
	return nil, value.ValidateSession(ctx)
}
func (value *sourceStub) ValidateSession(ctx context.Context) error {
	if !value.live {
		return &query.Error{Code: query.CodeInvalidPlan}
	}
	return ctx.Err()
}
func (*sourceStub) Insert(context.Context, query.InsertPlan) (int64, error) { return 0, nil }
func (*sourceStub) Update(context.Context, query.UpdatePlan) (int64, error) { return 0, nil }
func (*sourceStub) Delete(context.Context, query.DeletePlan) (int64, error) { return 0, nil }

func TestReadScopeRemovesMutationCapabilityAndExpiresOnCleanupFailure(t *testing.T) {
	source := &sourceStub{live: true}
	cleanupFailure := errors.New("cleanup failure")
	callbackFailure := errors.New("callback failure")
	var retained db.Queryer
	ends := 0
	err := Run(t.Context(), source, func(reader db.Queryer) error {
		retained = reader
		if _, writable := reader.(db.Mutator); writable {
			t.Fatal("snapshot exposed mutation capability")
		}
		return callbackFailure
	}, func() { source.live = false }, func() error {
		ends++
		if source.live {
			t.Fatal("cleanup began before borrowed handle expired")
		}
		return cleanupFailure
	})
	if ends != 1 || !errors.Is(err, callbackFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatal("read-scope failure ownership", ends, err)
	}
	if err := retained.(db.SessionValidator).ValidateSession(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("retained reader remained live", err)
	}
}

func TestReadScopeCancellationPanicAndGoexitEndExactlyOnce(t *testing.T) {
	for _, mode := range []string{"cancel", "panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			source := &sourceStub{live: true}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ends := 0
			var result error
			var panicked any
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { panicked = recover() }()
				result = Run(ctx, source, func(db.Queryer) error {
					switch mode {
					case "cancel":
						cancel()
					case "panic":
						panic("original callback panic")
					case "goexit":
						runtime.Goexit()
					}
					return nil
				}, func() { source.live = false }, func() error { ends++; return nil })
			}()
			<-done
			if source.live || ends != 1 {
				t.Fatal("callback escape retained read scope", source.live, ends)
			}
			if mode == "cancel" && !errors.Is(result, context.Canceled) || mode == "panic" && panicked != "original callback panic" {
				t.Fatal("callback cause changed", result, panicked)
			}
		})
	}
}
