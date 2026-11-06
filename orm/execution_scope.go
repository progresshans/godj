package orm

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/db"
)

type executionScopeKey struct{}

// The context selects I/O affinity, never a model's project origin or cache.
// Scopes are immutable and shadow only the exact original backend. A nested
// stream can therefore pin another source without changing existing handles.
type executionScope struct {
	origin   db.Queryer
	executor db.Queryer
	parent   *executionScope
}

func withExecutionScope(ctx context.Context, origin, executor db.Queryer) (context.Context, error) {
	if interfaceIsNil(origin) || interfaceIsNil(executor) {
		return nil, relationBackendInvalidPlan("batch backend or executor is nil")
	}
	if !reflect.ValueOf(origin).Comparable() {
		return nil, relationBackendInvalidPlan("batch execution requires a comparable backend identity")
	}
	_, originalSession := origin.(db.SessionValidator)
	_, executorSession := executor.(db.SessionValidator)
	if originalSession != executorSession {
		return nil, relationBackendInvalidPlan("batch executor changed the root or borrowed session lifetime")
	}
	parent, _ := ctx.Value(executionScopeKey{}).(*executionScope)
	scoped := context.WithValue(ctx, executionScopeKey{}, &executionScope{origin: origin, executor: executor, parent: parent})
	_, err := executionBackend(scoped, origin)
	return scoped, err
}

// Resolve once at the ORM I/O boundary. Do not route inside a backend: native
// root batch executors delegate to their original backend after stream close.
func executionBackend[B any](ctx context.Context, backend B) (B, error) {
	var zero B
	if interfaceIsNil(ctx) {
		return zero, invalidTerminalContext()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := validateExecutionSession(ctx, backend); err != nil {
		return zero, err
	}
	scope, _ := ctx.Value(executionScopeKey{}).(*executionScope)
	if scope == nil || interfaceIsNil(backend) || !reflect.ValueOf(backend).Comparable() {
		return backend, nil
	}
	for ; scope != nil; scope = scope.parent {
		if any(backend) != any(scope.origin) {
			continue
		}
		result, ok := any(scope.executor).(B)
		if !ok || interfaceIsNil(result) {
			return zero, relationBackendInvalidPlan("batch executor does not support the requested operation")
		}
		if err := validateExecutionSession(ctx, result); err != nil {
			return zero, err
		}
		return result, nil
	}
	return backend, nil
}

func validateExecutionSession(ctx context.Context, backend any) error {
	scope, scoped := backend.(db.SessionValidator)
	if !scoped {
		return nil
	}
	if interfaceIsNil(scope) {
		return relationBackendInvalidPlan("query session validator is nil")
	}
	return joinContextErr(scope.ValidateSession(ctx), ctx)
}
