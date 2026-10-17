package db

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/query"
)

// Savepointer is the optional nested-transaction capability of a borrowed
// Session. It does not begin or commit an independent transaction. The callback
// is invoked once after SAVEPOINT succeeds and receives a child Session whose
// lifetime ends before this method returns. A successful release is provisional
// until the parent transaction commits.
//
// Callback errors and cancellation roll back the child scope. If rollback or
// release cannot be confirmed, the parent must reject further work and must not
// commit even if its caller suppresses the returned error. Implementations must
// not retry callback, control statements or an uncertain outcome.
type Savepointer interface {
	SessionValidator
	Savepoint(context.Context, func(Session) error) error
}

// WithSavepoint runs a nested scope on the supplied borrowed session. A root
// backend or adapter without the required capability is explicitly rejected.
// The callback must use its child handle, close its cursors and join work using
// that handle before returning; the parent handle is unavailable inside it.
// Native backends reject entry while a parent cursor or batch stream is open.
// Retained handles and rowsets report backend invalid_plan after scope exit.
func WithSavepoint(ctx context.Context, session Session, callback func(Session) error) error {
	if nilSavepointValue(ctx) {
		return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidPlan, Detail: "savepoint context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if nilSavepointValue(session) {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "savepoint session is nil"}
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidPlan, Detail: "savepoint callback is nil"}
	}
	source, ok := session.(Savepointer)
	if !ok || nilSavepointValue(source) {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "session does not support savepoints"}
	}
	if err := source.ValidateSession(ctx); err != nil {
		return err
	}
	return source.Savepoint(ctx, callback)
}

func nilSavepointValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
