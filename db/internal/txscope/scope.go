// Package txscope owns borrowed transaction handles, nested savepoints, and
// their resources. Native backends retain the sole commit/rollback owner.
package txscope

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/progresshans/godj/query"
)

const cleanupTimeout = 5 * time.Second

type owner struct {
	mu        sync.Mutex
	root      *Scope
	top       *Scope
	operation *Operation
	control   func(context.Context, string) error
	sequence  uint64
	poison    error
}

// Scope is one handle's lifetime within an already-open native transaction.
// Only the innermost handle may perform I/O. Callers serialize operations and
// join work using a handle before its callback returns.
type Scope struct {
	owner      *owner
	parent     *Scope
	ctx        context.Context
	cancel     func(error)
	name       string
	closing    bool
	ended      bool
	finishDone chan struct{}
	finishErr  error
	resources  []*Resource
	cleanupErr error
	holds      int
}

// New does not begin a transaction or validate the caller context again. In
// particular, acquiring a native coordination fence already commits its owner
// to invoking the callback once, even if cancellation arrived at that boundary.
func New(ctx context.Context, control func(context.Context, string) error) *Scope {
	lifetime, cancel := context.WithCancelCause(ctx)
	scope := &Scope{ctx: lifetime, cancel: cancel, finishDone: make(chan struct{})}
	scope.owner = &owner{root: scope, top: scope, control: control}
	return scope
}

func invalid(detail string) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: detail}
}

func joined(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return errors.Join(first, second)
}

func (scope *Scope) Validate(ctx context.Context) error {
	if scope == nil || scope.owner == nil {
		return invalid("transaction scope is nil or no longer active")
	}
	scope.owner.mu.Lock()
	defer scope.owner.mu.Unlock()
	return scope.validateLocked(ctx)
}

func (scope *Scope) validateLocked(ctx context.Context) error {
	if scope.ended || scope.closing || scope.owner.root.ended || scope.owner.root.closing {
		return invalid("transaction scope is no longer active")
	}
	if scope.owner.poison != nil {
		return scope.owner.poison
	}
	if scope.owner.top != scope {
		return invalid("parent transaction handle is unavailable during a child scope")
	}
	if ctx == nil {
		return invalid("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return scope.lifetimeErrorLocked()
}

func (scope *Scope) lifetimeErrorLocked() error {
	for current := scope; current != nil; current = current.parent {
		if err := context.Cause(current.ctx); err != nil {
			return err
		}
	}
	return nil
}

func (state *owner) poisonLocked(cause error) error {
	if state.poison == nil {
		state.poison = &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionRollbackRequired,
			Detail: "transaction scope cleanup is unconfirmed; the transaction owner must roll back", Cause: cause}
	} else if cause != nil {
		state.poison = joined(state.poison, cause)
	}
	return state.poison
}

// Operation reserves the physical transaction while one native I/O operation
// is in progress. Its context observes both the supplied context and the scope.
type Operation struct {
	scope    *Scope
	ctx      context.Context
	cancel   context.CancelCauseFunc
	stop     func() bool
	done     chan struct{}
	resource *Resource
	ended    bool
}

func (scope *Scope) Begin(ctx context.Context) (*Operation, error) {
	if scope == nil || scope.owner == nil {
		return nil, invalid("transaction scope is nil")
	}
	state := scope.owner
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := scope.validateLocked(ctx); err != nil {
		return nil, err
	}
	if state.operation != nil {
		return nil, invalid("transaction operations must be serialized by the caller")
	}
	op := scope.operationLocked(ctx)
	op.stop = context.AfterFunc(scope.ctx, func() { op.cancel(context.Cause(scope.ctx)) })
	return op, nil
}

func (scope *Scope) operationLocked(ctx context.Context) *Operation {
	combined, cancel := context.WithCancelCause(ctx)
	op := &Operation{scope: scope, ctx: combined, cancel: cancel, done: make(chan struct{})}
	scope.owner.operation = op
	return op
}

func (op *Operation) Context() context.Context { return op.ctx }

// Release also runs during panic unwinding. A transferred rowset keeps its
// context; releasing an already-ended operation cannot revoke that transfer.
func (op *Operation) Release() {
	state := op.scope.owner
	state.mu.Lock()
	op.releaseLocked(true)
	state.mu.Unlock()
}

func (scope *Scope) FinishCallback(err error) error { return joined(err, scope.Finish()) }

// End returns a late lifetime/cancellation failure before a result is exposed.
func (op *Operation) End(err error) error {
	state := op.scope.owner
	state.mu.Lock()
	if !op.ended {
		err = joined(err, op.scope.validateLocked(op.ctx))
	}
	op.releaseLocked(true)
	state.mu.Unlock()
	return err
}

func (op *Operation) releaseLocked(cancel bool) {
	if op.ended {
		return
	}
	op.ended = true
	if op.stop != nil {
		op.stop()
	}
	if cancel {
		op.cancel(context.Canceled)
	}
	if op.scope.owner.operation == op {
		op.scope.owner.operation = nil
	}
	close(op.done)
}

// Resource registers cleanup while its creating operation still owns the
// transaction. Cleanup runs once, before savepoint control or root termination,
// under the same I/O reservation. Resources are closed in reverse creation order.
func (op *Operation) Resource(close func(context.Context) error) (*Resource, error) {
	state := op.scope.owner
	state.mu.Lock()
	defer state.mu.Unlock()
	if op.ended || state.operation != op {
		return nil, invalid("transaction operation has ended")
	}
	if close == nil {
		return nil, invalid("transaction resource cleanup is nil")
	}
	if err := op.scope.validateLocked(op.ctx); err != nil {
		return nil, err
	}
	resource := &Resource{scope: op.scope, close: close, done: make(chan struct{})}
	op.scope.resources = append(op.scope.resources, resource)
	return resource, nil
}

type Resource struct {
	scope     *Scope
	close     func(context.Context) error
	interrupt func()
	closing   bool
	closed    bool
	done      chan struct{}
	err       error
}

func (resource *Resource) Close() error {
	state := resource.scope.owner
	for {
		state.mu.Lock()
		if resource.closed {
			err := resource.err
			state.mu.Unlock()
			return err
		}
		if resource.closing {
			done := resource.done
			state.mu.Unlock()
			<-done
			continue
		}
		if active := state.operation; active != nil {
			// Closing a rowset interrupts a concurrent Next/Scan on that same
			// rowset. It must not cancel an unrelated operation's context.
			if active.resource == resource {
				active.cancel(context.Canceled)
				if resource.interrupt != nil {
					resource.interrupt()
				}
			}
			done := active.done
			state.mu.Unlock()
			<-done
			continue
		}
		resource.closing = true
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(resource.scope.ctx), cleanupTimeout)
		op := resource.scope.operationLocked(cleanup)
		state.mu.Unlock()
		return resource.runClose(op, cancel)
	}
}

func (resource *Resource) runClose(op *Operation, cancel context.CancelFunc) (err error) {
	returned := false
	defer func() {
		cancel()
		state := resource.scope.owner
		state.mu.Lock()
		if !returned {
			err = state.poisonLocked(invalid("transaction resource cleanup did not return"))
		}
		resource.err, resource.closed = err, true
		resource.scope.cleanupErr = joined(resource.scope.cleanupErr, err)
		for index, existing := range resource.scope.resources {
			if existing == resource {
				copy(resource.scope.resources[index:], resource.scope.resources[index+1:])
				last := len(resource.scope.resources) - 1
				resource.scope.resources[last] = nil
				resource.scope.resources = resource.scope.resources[:last]
				break
			}
		}
		op.releaseLocked(true)
		close(resource.done)
		state.mu.Unlock()
	}()
	err = resource.close(op.ctx)
	returned = true
	return err
}

// Hold keeps a callback-driven stream within its scope even between native I/O
// calls. Savepoints cannot move a live cursor into another rollback domain.
func (scope *Scope) Hold(ctx context.Context) (func() error, error) {
	if scope == nil || scope.owner == nil {
		return nil, invalid("transaction scope is nil")
	}
	state := scope.owner
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := scope.validateLocked(ctx); err != nil {
		return nil, err
	}
	scope.holds++
	var once sync.Once
	return func() (err error) {
		once.Do(func() {
			state.mu.Lock()
			defer state.mu.Unlock()
			scope.holds--
			err = scope.validateLocked(ctx)
		})
		return err
	}, nil
}

func (scope *Scope) Savepoint(ctx context.Context, callback func(*Scope) error) (err error) {
	if callback == nil {
		return invalid("savepoint callback is nil")
	}
	if scope == nil || scope.owner == nil {
		return invalid("transaction scope is nil")
	}
	state := scope.owner
	state.mu.Lock()
	if err := scope.validateLocked(ctx); err != nil {
		state.mu.Unlock()
		return err
	}
	if state.control == nil {
		state.mu.Unlock()
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "session does not support savepoints"}
	}
	if state.operation != nil || scope.holds != 0 || scope.hasResourcesLocked() {
		state.mu.Unlock()
		return invalid("savepoint requires all parent operations and cursors to finish")
	}
	if state.sequence == ^uint64(0) {
		state.mu.Unlock()
		return invalid("transaction savepoint identifier space is exhausted")
	}
	state.sequence++
	lifetime, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(scope.ctx, func() { cancel(context.Cause(scope.ctx)) })
	child := &Scope{owner: state, parent: scope, ctx: lifetime,
		cancel: func(cause error) { stop(); cancel(cause) },
		name:   "godj_savepoint_" + strconv.FormatUint(state.sequence, 10), finishDone: make(chan struct{})}
	state.top = child
	state.mu.Unlock()
	if err := child.control(ctx, "SAVEPOINT "+child.name); err != nil {
		child.abandon(err)
		return child.finishErr
	}
	returned := false
	defer func() {
		if !returned {
			// Panic and Goexit propagate unchanged, after child rollback.
			_ = child.finish(invalid("savepoint callback did not return"))
		}
	}()
	callbackErr := callback(child)
	err = child.finish(callbackErr)
	returned = true
	return err
}

func (scope *Scope) hasResourcesLocked() bool {
	for _, resource := range scope.resources {
		if !resource.closed {
			return true
		}
	}
	return false
}

func (scope *Scope) control(ctx context.Context, statement string) error {
	state := scope.owner
	state.mu.Lock()
	if state.root.closing || state.root.ended || state.top != scope || state.operation != nil {
		err := state.poisonLocked(invalid("savepoint control lost transaction ownership"))
		state.mu.Unlock()
		return err
	}
	if state.poison != nil {
		err := state.poison
		state.mu.Unlock()
		return err
	}
	if !scope.closing {
		if err := scope.validateLocked(ctx); err != nil {
			state.mu.Unlock()
			return err
		}
	}
	op := scope.operationLocked(ctx)
	state.mu.Unlock()
	defer op.Release()
	err := state.control(op.ctx, statement)
	state.mu.Lock()
	op.releaseLocked(true)
	if err != nil {
		err = state.poisonLocked(err)
	}
	state.mu.Unlock()
	return err
}

func (scope *Scope) abandon(err error) {
	state := scope.owner
	state.mu.Lock()
	scope.closing, scope.ended, scope.finishErr = true, true, err
	scope.cancel(invalid("savepoint scope has ended"))
	if state.top == scope {
		state.top = scope.parent
	}
	close(scope.finishDone)
	state.mu.Unlock()
}

// Finish expires a root handle, closes its resources, and returns any condition
// which prevents its native transaction owner from committing. The owner calls
// this on normal return, panic, and Goexit, before its terminal SQL.
func (scope *Scope) Finish() error {
	if scope == nil || scope.owner == nil {
		return invalid("transaction scope is nil")
	}
	if scope.parent != nil {
		return invalid("only the transaction owner may finish the root scope")
	}
	return scope.finish(nil)
}

func (scope *Scope) finish(primary error) error {
	state := scope.owner
	state.mu.Lock()
	if scope.closing {
		done := scope.finishDone
		state.mu.Unlock()
		<-done
		return joined(primary, scope.finishErr)
	}
	scope.closing = true
	finished := false
	defer func() {
		if !finished {
			// Cleanup panics and Goexit cannot strand the lifecycle latch or
			// prevent the native owner's deferred root rollback.
			state.mu.Lock()
			scope.finishErr = state.poisonLocked(invalid("transaction scope cleanup did not return"))
			scope.cancel(scope.finishErr)
			scope.ended = true
			if scope.parent == nil || state.top == scope {
				state.top = scope.parent
			}
			close(scope.finishDone)
			state.mu.Unlock()
		}
	}()
	primary = joined(joined(primary, scope.lifetimeErrorLocked()), scope.cleanupErr)
	if state.top != scope || scope.holds != 0 || state.operation != nil {
		state.poisonLocked(invalid("transaction callback returned with unfinished child or I/O work"))
	}
	var resources []*Resource
	found := false
	for current := state.top; current != nil; current = current.parent {
		resources = append(append([]*Resource(nil), current.resources...), resources...)
		if state.poison != nil {
			current.cancel(invalid("transaction callback lifetime ended"))
		}
		if current == scope {
			found = true
			break
		}
	}
	if !found {
		resources = append([]*Resource(nil), scope.resources...)
	}
	var running <-chan struct{}
	if active := state.operation; active != nil {
		active.cancel(invalid("transaction callback lifetime ended"))
		running = active.done
	}
	state.mu.Unlock()
	if running != nil {
		<-running
	}
	for index := len(resources) - 1; index >= 0; index-- {
		primary = joined(primary, resources[index].Close())
	}
	state.mu.Lock()
	primary = joined(primary, scope.lifetimeErrorLocked())
	state.mu.Unlock()

	if scope.parent != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(scope.ctx), cleanupTimeout)
		var controlErr error
		if primary != nil {
			controlErr = scope.control(cleanup, "ROLLBACK TO SAVEPOINT "+scope.name)
		}
		// An unconfirmed rollback already poisons control and prevents release.
		if controlErr == nil {
			controlErr = scope.control(cleanup, "RELEASE SAVEPOINT "+scope.name)
		}
		primary = joined(primary, controlErr)
		cancel()
	}
	state.mu.Lock()
	primary = joined(primary, state.poison)
	scope.cancel(invalid("transaction scope has ended"))
	scope.ended, scope.finishErr = true, primary
	if scope.parent == nil || state.top == scope {
		state.top = scope.parent
	}
	close(scope.finishDone)
	finished = true
	state.mu.Unlock()
	return primary
}
