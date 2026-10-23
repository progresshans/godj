// Package events provides explicitly owned, synchronous process-local events.
// Events do not open resources, register global handlers, retry delivery, infer
// transaction outcomes or provide durable or cross-process messaging.
package events

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/progresshans/godj/internal/callback"
	"github.com/progresshans/godj/internal/diagnosticlog"
)

const MaxReceivers = 1024

// Error exposes only a closed code and a registered receiver identifier.
// Underlying errors are available to errors.Is/As, never to formatting.
type Error struct {
	Code, Receiver string
	cause          error
}

func (e *Error) Error() string {
	if e == nil {
		return "events: invalid error"
	}
	return "events: " + e.Code
}
func (e *Error) Format(s fmt.State, _ rune) { fmt.Fprint(s, e.Error()) }
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type Config struct {
	// Name and receiver names are public diagnostic identifiers, not payloads.
	Name string
	// Logger is optional; nil disables framework logs. Add project/invocation
	// attributes with Logger.With. The caller owns the handler and its sink.
	Logger *slog.Logger
}

// Event retains receivers strongly until Unsubscribe or Close. Construct with
// New; the zero value is invalid and a constructed Event must not be copied.
// Concurrent sends can enter the same receiver concurrently. The caller owns
// synchronization of receiver captures and payloads. Values are copied using Go
// assignment: maps, slices, pointers and interfaces are not deep-copied.
type Event[T any] struct {
	mu        sync.Mutex
	name      string
	logger    *slog.Logger
	receivers []*Subscription[T]
	active    int
	closed    bool
	done      chan struct{}
}

// Subscription owns one registration, not its callback's external resources.
// Unsubscribe stops future admissions without waiting. Wait drains callbacks
// already admitted, after which the caller may release those resources.
type Subscription[T any] struct {
	event  *Event[T]
	name   string
	handle func(context.Context, T) error
	active int
	closed bool
	done   chan struct{}
}

type Result struct {
	Receiver string
	Err      error
}

func New[T any](config Config) (*Event[T], error) {
	if !validName(config.Name) {
		return nil, &Error{Code: "invalid_config"}
	}
	return &Event[T]{name: config.Name, logger: config.Logger, done: make(chan struct{})}, nil
}

func (*Event[T]) Format(s fmt.State, _ rune)        { fmt.Fprint(s, "events.Event{redacted}") }
func (*Subscription[T]) Format(s fmt.State, _ rune) { fmt.Fprint(s, "events.Subscription{redacted}") }

// Subscribe appends in registration order. Duplicate names are errors. A later
// subscription with a formerly removed name is a new registration at the end.
func (e *Event[T]) Subscribe(name string, handle func(context.Context, T) error) (*Subscription[T], error) {
	if e == nil || e.done == nil || !validName(name) || handle == nil {
		return nil, &Error{Code: "invalid_receiver"}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, &Error{Code: "closed"}
	}
	for _, existing := range e.receivers {
		if existing.name == name {
			return nil, &Error{Code: "duplicate_receiver", Receiver: name}
		}
	}
	if len(e.receivers) == MaxReceivers {
		return nil, &Error{Code: "receiver_limit"}
	}
	s := &Subscription[T]{event: e, name: name, handle: handle, done: make(chan struct{})}
	e.receivers = append(e.receivers, s)
	return s, nil
}

// Send stops at the first receiver failure and returns all attempted receivers.
// Send snapshots registration order. A new receiver is visible on the next send
// (including a nested send); a removed receiver is skipped if not yet admitted.
// Callbacks are joined through cancellation, panic and Goexit. Cancellation is
// cooperative; a handler that ignores it can delay return. No event lock is held
// across application code, so callbacks may register, unsubscribe and send again.
func (e *Event[T]) Send(ctx context.Context, value T) ([]Result, error) {
	return e.send(ctx, value, false)
}

// SendRobust continues after receiver errors, returning them in Results. Its
// second return value reports only invocation, close or cancellation failure.
// For notifications after a confirmed commit, the caller must preserve the DB
// outcome and handle notification results separately; delivery never rolls back.
func (e *Event[T]) SendRobust(ctx context.Context, value T) ([]Result, error) {
	return e.send(ctx, value, true)
}

type frameKey struct{}
type frame struct {
	event, subscription any
	parent              *frame
	active              atomic.Bool
}

func withFrame(ctx context.Context, event, subscription any) (context.Context, *frame) {
	parent, _ := ctx.Value(frameKey{}).(*frame)
	current := &frame{event: event, subscription: subscription, parent: parent}
	current.active.Store(true)
	return context.WithValue(ctx, frameKey{}, current), current
}

func (e *Event[T]) send(ctx context.Context, value T, robust bool) (results []Result, err error) {
	if e == nil || e.done == nil || ctx == nil {
		return nil, &Error{Code: "invalid_invocation"}
	}
	if cause := ctx.Err(); cause != nil {
		return nil, &Error{Code: "canceled", cause: cause}
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, &Error{Code: "closed"}
	}
	snapshot := append([]*Subscription[T](nil), e.receivers...)
	e.active++
	e.mu.Unlock()
	ctx, sendFrame := withFrame(ctx, e, nil)
	defer func() {
		diagnosticlog.End(ctx, e.logger, "event", "send", e.name, err)
		e.mu.Lock()
		e.active--
		if e.closed && e.active == 0 {
			close(e.done)
		}
		e.mu.Unlock()
		sendFrame.active.Store(false)
	}()
	for _, sub := range snapshot {
		if cause := ctx.Err(); cause != nil {
			return results, &Error{Code: "canceled", cause: cause}
		}
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return results, &Error{Code: "closed"}
		}
		if sub.closed {
			e.mu.Unlock()
			continue
		}
		handle := sub.handle
		sub.active++
		e.mu.Unlock()
		callContext, callFrame := withFrame(ctx, e, sub)
		_, callErr := callback.Invoke(func() (struct{}, error) { return struct{}{}, handle(callContext, value) }, &Error{Code: "callback_aborted", Receiver: sub.name})
		if callErr != nil {
			callErr = &Error{Code: "receiver_failed", Receiver: sub.name, cause: callErr}
		}
		diagnosticlog.End(callContext, e.logger, "event", "receive", sub.name, callErr)
		e.mu.Lock()
		sub.active--
		if sub.closed && sub.active == 0 {
			close(sub.done)
		}
		e.mu.Unlock()
		callFrame.active.Store(false)
		results = append(results, Result{Receiver: sub.name, Err: callErr})
		if callErr != nil && !robust {
			return results, callErr
		}
	}
	return results, nil
}

// Unsubscribe is idempotent and safe inside this receiver. The result reports
// whether this call removed the registration. An admitted callback keeps running.
func (s *Subscription[T]) Unsubscribe() bool {
	if s == nil || s.event == nil {
		return false
	}
	e := s.event
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.closed {
		return false
	}
	s.closed = true
	s.handle = nil
	e.receivers = slices.DeleteFunc(e.receivers, func(other *Subscription[T]) bool { return s == other })
	if s.active == 0 {
		close(s.done)
	}
	return true
}

// Close stops admissions and releases registered callback references. It does
// not wait and is safe from a receiver. Wait joins sends already in progress.
func (e *Event[T]) Close() bool {
	if e == nil || e.done == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false
	}
	e.closed = true
	for _, sub := range e.receivers {
		sub.closed = true
		sub.handle = nil
		if sub.active == 0 {
			close(sub.done)
		}
	}
	e.receivers = nil
	if e.active == 0 {
		close(e.done)
	}
	return true
}

// Wait observes Close completion without starting it. Waiting from a callback
// of this event using the supplied/derived context returns would_deadlock.
// Callbacks must retain that context for nested sends/waits; replacing it with
// an unrelated context discards reentrancy detection and cancellation.
func (e *Event[T]) Wait(ctx context.Context) error {
	if e == nil || e.done == nil {
		return &Error{Code: "invalid_invocation"}
	}
	return wait(ctx, e.done, e, nil)
}

// Wait observes Unsubscribe/Close completion. The same context/reentrancy rules
// as Event.Wait apply, including a nested callback waiting on its ancestor.
func (s *Subscription[T]) Wait(ctx context.Context) error {
	if s == nil || s.event == nil {
		return &Error{Code: "invalid_invocation"}
	}
	return wait(ctx, s.done, nil, s)
}

func wait(ctx context.Context, done <-chan struct{}, event, subscription any) error {
	if ctx == nil {
		return &Error{Code: "invalid_invocation"}
	}
	select {
	case <-done:
		return nil
	default:
	}
	for current, _ := ctx.Value(frameKey{}).(*frame); current != nil; current = current.parent {
		if current.active.Load() && (event != nil && current.event == event || subscription != nil && current.subscription == subscription) {
			return &Error{Code: "would_deadlock"}
		}
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		select {
		case <-done:
			return nil
		default:
		}
		return &Error{Code: "canceled", cause: ctx.Err()}
	}
}

func validName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i, c := range []byte(name) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '.' || c == '-') {
			continue
		}
		return false
	}
	return true
}
