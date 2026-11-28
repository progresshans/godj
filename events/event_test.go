package events_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/events"
)

func newEvent(t *testing.T) *events.Event[int] {
	t.Helper()
	e, err := events.New[int](events.Config{Name: "document.saved"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Wait(ctx); err != nil {
			t.Error(err)
		}
	})
	return e
}

func code(t *testing.T, err error, want string) {
	t.Helper()
	var failure *events.Error
	if !errors.As(err, &failure) || failure.Code != want {
		t.Fatalf("error=%v want %s", err, want)
	}
}

func TestRegistrationOrderFailureAndRobustDelivery(t *testing.T) {
	e := newEvent(t)
	var order []string
	failure := errors.New("private-callback-detail")
	for _, name := range []string{"first", "second", "third"} {
		_, err := e.Subscribe(name, func(_ context.Context, value int) error {
			order = append(order, name)
			if value != 17 {
				return errors.New("incorrect payload")
			}
			if name == "second" {
				return failure
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	results, err := e.Send(context.Background(), 17)
	if !errors.Is(err, failure) || len(results) != 2 || !slices.Equal(order, []string{"first", "second"}) {
		t.Fatal(results, err, order)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, err), "private") {
			t.Fatal("cause leaked")
		}
	}
	order = nil
	results, err = e.SendRobust(context.Background(), 17)
	if err != nil || len(results) != 3 || results[0].Err != nil || !errors.Is(results[1].Err, failure) || results[2].Err != nil || !slices.Equal(order, []string{"first", "second", "third"}) {
		t.Fatal(results, err, order)
	}
}

func TestSnapshotUnsubscribeResubscribeAndNestedSend(t *testing.T) {
	e := newEvent(t)
	var order []string
	var removed *events.Subscription[int]
	_, err := e.Subscribe("first", func(ctx context.Context, value int) error {
		order = append(order, fmt.Sprintf("first/%d", value))
		if value != 1 {
			return nil
		}
		if !removed.Unsubscribe() || removed.Unsubscribe() {
			return errors.New("unsubscribe was not idempotent")
		}
		if err := removed.Wait(ctx); err != nil {
			return err
		}
		_, err := e.Subscribe("second", func(_ context.Context, value int) error {
			order = append(order, fmt.Sprintf("replacement/%d", value))
			return nil
		})
		if err != nil {
			return err
		}
		_, err = e.Send(ctx, 2)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err = e.Subscribe("second", func(context.Context, int) error { return errors.New("removed receiver ran") })
	if err != nil {
		t.Fatal(err)
	}
	results, err := e.Send(context.Background(), 1)
	if err != nil || len(results) != 1 || !slices.Equal(order, []string{"first/1", "first/2", "replacement/2"}) {
		t.Fatal(order, results, err)
	}
}

func TestSelfAndAncestorWaitDoNotDeadlock(t *testing.T) {
	e := newEvent(t)
	nested := newEvent(t)
	var sub *events.Subscription[int]
	var observations []string
	_, err := nested.Subscribe("nested", func(ctx context.Context, _ int) error {
		for _, wait := range []func(context.Context) error{sub.Wait, e.Wait, nested.Wait} {
			failure := wait(context.WithValue(ctx, "user-key", "retained"))
			var eventError *events.Error
			if !errors.As(failure, &eventError) {
				return errors.New("wait returned no classification")
			}
			observations = append(observations, eventError.Code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, err = e.Subscribe("self", func(ctx context.Context, _ int) error {
		sub.Unsubscribe()
		e.Close()
		_, err := nested.Send(ctx, 2)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := e.Send(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(observations, []string{"would_deadlock", "would_deadlock", "would_deadlock"}) {
		t.Fatal(observations)
	}
	if err := sub.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCloseAndUnsubscribeJoinAdmittedCallbacks(t *testing.T) {
	for _, closeEvent := range []bool{false, true} {
		t.Run(fmt.Sprint(closeEvent), func(t *testing.T) {
			e := newEvent(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var cleaned atomic.Bool
			sub, err := e.Subscribe("active", func(context.Context, int) error { close(entered); <-release; defer cleaned.Store(true); return nil })
			if err != nil {
				t.Fatal(err)
			}
			completed := make(chan error, 1)
			finished := make(chan struct{})
			var releaseOnce sync.Once
			releaseCallback := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(func() {
				releaseCallback()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("admitted callback did not finish")
				}
			})
			go func() { defer close(finished); _, err := e.Send(context.Background(), 1); completed <- err }()
			<-entered
			if closeEvent {
				if !e.Close() || e.Close() {
					t.Fatal("close idempotence")
				}
			} else {
				sub.Unsubscribe()
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := sub.Wait(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal("wait abandoned admitted callback", err)
			}
			if cleaned.Load() {
				t.Fatal("callback unexpectedly completed")
			}
			if closeEvent {
				_, err = e.Send(context.Background(), 2)
				code(t, err, "closed")
			} else {
				results, err := e.Send(context.Background(), 2)
				if err != nil || len(results) != 0 {
					t.Fatal(results, err)
				}
			}
			releaseCallback()
			if err := <-completed; err != nil {
				t.Fatal(err)
			}
			if err := sub.Wait(context.Background()); err != nil || !cleaned.Load() {
				t.Fatal(err)
			}
		})
	}
}

func TestCompletedCallbackContextDoesNotRetainAFalseWaitCycle(t *testing.T) {
	e := newEvent(t)
	var retained context.Context
	sub, err := e.Subscribe("capture", func(ctx context.Context, _ int) error { retained = ctx; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Send(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(retained)
	cancel()
	if err := sub.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("completed callback still marked active", err)
	}
	if err := e.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("completed send still marked active", err)
	}
	sub.Unsubscribe()
	e.Close()
	if err := sub.Wait(retained); err != nil {
		t.Fatal(err)
	}
	if err := e.Wait(retained); err != nil {
		t.Fatal(err)
	}
}

func TestAbnormalAndCanceledCallbacksRunDefersAndStopAdmission(t *testing.T) {
	for _, abort := range []string{"panic", "goexit", "cancel"} {
		t.Run(abort, func(t *testing.T) {
			e := newEvent(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var cleaned, later atomic.Bool
			_, err := e.Subscribe("first", func(context.Context, int) error {
				defer cleaned.Store(true)
				switch abort {
				case "panic":
					panic("private-panic-data")
				case "goexit":
					runtime.Goexit()
				default:
					cancel()
					return ctx.Err()
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Subscribe("later", func(context.Context, int) error { later.Store(true); return nil })
			if err != nil {
				t.Fatal(err)
			}
			results, err := e.SendRobust(ctx, 1)
			if !cleaned.Load() || len(results) == 0 || results[0].Err == nil {
				t.Fatal(results, err)
			}
			if abort == "cancel" {
				if later.Load() || !errors.Is(err, context.Canceled) || !errors.Is(results[0].Err, context.Canceled) {
					t.Fatal(results, err)
				}
			} else if err != nil || !later.Load() || len(results) != 2 {
				t.Fatal(results, err)
			}
		})
	}
}

func TestConcurrentSendsAndSubscriptionChurn(t *testing.T) {
	e := newEvent(t)
	var calls atomic.Int64
	_, err := e.Subscribe("stable", func(context.Context, int) error { calls.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 32)
	for worker := 0; worker < 24; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 32; i++ {
				if _, err := e.Send(context.Background(), worker); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 64; i++ {
			sub, err := e.Subscribe("changing", func(context.Context, int) error { return nil })
			if err != nil {
				failures <- err
				return
			}
			sub.Unsubscribe()
			if err := sub.Wait(context.Background()); err != nil {
				failures <- err
				return
			}
		}
	}()
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if calls.Load() != 24*32 {
		t.Fatal("lost stable deliveries", calls.Load())
	}
}

func TestInvalidInputLimitsAndIndependentOwners(t *testing.T) {
	if _, err := events.New[int](events.Config{Name: "private\nvalue"}); err == nil {
		t.Fatal("invalid event name")
	}
	var zero events.Event[int]
	if _, err := zero.Send(context.Background(), 1); err == nil {
		t.Fatal("zero event")
	}
	e := newEvent(t)
	if _, err := e.Subscribe("invalid/value", func(context.Context, int) error { return nil }); err == nil {
		t.Fatal("invalid receiver")
	}
	if _, err := e.Subscribe("nil", nil); err == nil {
		t.Fatal("nil handler")
	}
	for i := 0; i < events.MaxReceivers; i++ {
		if _, err := e.Subscribe(fmt.Sprintf("r%d", i), func(context.Context, int) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.Subscribe("r0", func(context.Context, int) error { return nil })
	code(t, err, "duplicate_receiver")
	_, err = e.Subscribe("overflow", func(context.Context, int) error { return nil })
	code(t, err, "receiver_limit")
	other := newEvent(t)
	results, err := other.Send(context.Background(), 1)
	if err != nil || len(results) != 0 {
		t.Fatal("registration leaked across owners")
	}
	e.Close()
	_, err = e.Subscribe("closed", func(context.Context, int) error { return nil })
	code(t, err, "closed")
	_, err = e.Send(nil, 1)
	code(t, err, "invalid_invocation")
}

type observingHandler struct {
	mu       sync.Mutex
	records  []slog.Record
	behavior func(context.Context)
}

func (*observingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *observingHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	if h.behavior != nil {
		h.behavior(ctx)
	}
	return errors.New("private-log-failure")
}
func (h *observingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *observingHandler) WithGroup(string) slog.Handler      { return h }

func TestLoggingFailureReentryAndPrivatePayload(t *testing.T) {
	for _, behavior := range []string{"error", "panic", "goexit", "reentry"} {
		t.Run(behavior, func(t *testing.T) {
			h := &observingHandler{}
			e, err := events.New[string](events.Config{Name: "private_payload_event", Logger: slog.New(h)})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			var cleanups, calls atomic.Int64
			h.behavior = func(ctx context.Context) {
				defer cleanups.Add(1)
				switch behavior {
				case "panic":
					panic("credential")
				case "goexit":
					runtime.Goexit()
				case "reentry":
					if _, err := e.SendRobust(ctx, "nested-private-data"); err != nil {
						panic(err)
					}
				}
			}
			_, err = e.Subscribe("receiver", func(context.Context, string) error { calls.Add(1); return errors.New("secret-error-message") })
			if err != nil {
				t.Fatal(err)
			}
			results, err := e.SendRobust(context.Background(), "private-payload-bytes")
			if err != nil || len(results) != 1 || results[0].Err == nil || cleanups.Load() != 2 {
				t.Fatal(results, err, cleanups.Load())
			}
			if behavior == "reentry" && calls.Load() != 3 {
				t.Fatal("recursive log was not suppressed", calls.Load())
			}
			for _, record := range h.records {
				record.Attrs(func(a slog.Attr) bool {
					if strings.Contains(a.Value.String(), "secret") || strings.Contains(a.Value.String(), "private-payload") {
						t.Error("payload or cause logged")
					}
					return true
				})
			}
		})
	}
}
