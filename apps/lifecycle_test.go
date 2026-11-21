package apps_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/schema/ir"
)

func appRegistry(t *testing.T, labels ...string) apps.Registry {
	t.Helper()
	definitions := make([]apps.Config, len(labels))
	for index, label := range labels {
		definitions[index] = apps.Config{Name: "example.com/application/" + label, Label: label}
	}
	registry, err := apps.New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestAppRegistryUsesTheCanonicalSchemaNamespace(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"application", "_internal", "_", "app_123", "Application", "앱", "1app", " app ", "app-name", ""} {
		_, schemaErr := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: label})
		registry, registryErr := apps.New([]apps.Config{{Name: "example.com/app", Label: label}})
		if (schemaErr == nil) != (registryErr == nil) {
			t.Fatalf("schema and installed app disagreed for %q: schema=%v registry=%v", label, schemaErr, registryErr)
		}
		if registryErr == nil {
			if app, ok := registry.Lookup(label); !ok || app.Label != label {
				t.Fatal("registry rewrote canonical identity")
			}
		}
	}
}

func TestAppStartupOwnsDependencyOrderAndDetachedInputs(t *testing.T) {
	t.Parallel()
	registry := appRegistry(t, "dependent", "independent", "_foundation")
	var calls []string
	contexts := make(map[string]context.Context)
	hooks := []apps.Startup{
		{Label: "dependent", DependsOn: []string{"_foundation"}},
		{Label: "_foundation"},
		{Label: "independent"},
	}
	for index := range hooks {
		hooks[index].Start = func(ctx context.Context, app apps.Config) (apps.Stop, error) {
			if actual, ok := registry.Lookup(app.Label); !ok || actual != app {
				t.Error("startup received a different app identity")
			}
			contexts[app.Label] = ctx
			calls = append(calls, "start:"+app.Label)
			if app.Label == "independent" {
				hooks[0].DependsOn[0] = "not_installed"
				hooks[0].Start = func(context.Context, apps.Config) (apps.Stop, error) {
					t.Error("caller replaced a frozen callback")
					return nil, nil
				}
			}
			return func(shutdown context.Context) error {
				if ctx.Err() != context.Canceled || shutdown.Err() != nil {
					t.Error("shutdown did not cancel app work before cleanup")
				}
				calls = append(calls, "stop:"+app.Label)
				return nil
			}, nil
		}
	}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	lifetime, err := apps.Start(parent, registry, apps.StartupConfig{Hooks: hooks})
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"independent", "_foundation", "dependent"}
	for index, app := range lifetime.Order() {
		if app.Label != wantOrder[index] {
			t.Fatalf("startup order: %+v", lifetime.Order())
		}
	}
	order := lifetime.Order()
	order[0].Label = "changed"
	if lifetime.Order()[0].Label != "independent" {
		t.Fatal("caller changed lifecycle order")
	}
	cancel()
	if len(calls) != 3 {
		t.Fatal("parent cancellation implicitly called shutdown")
	}
	for _, ctx := range contexts {
		if ctx.Err() != context.Canceled {
			t.Fatal("app context lost parent cancellation")
		}
	}
	if err := lifetime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"start:independent", "start:_foundation", "start:dependent", "stop:dependent", "stop:_foundation", "stop:independent"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("lifecycle order: %v", calls)
	}
	if err := lifetime.Shutdown(context.Background()); err != nil || len(calls) != 6 {
		t.Fatal("completed shutdown repeated callbacks", err)
	}
}

func TestAppStartupValidatesTheCompleteGraphBeforeCallbacks(t *testing.T) {
	t.Parallel()
	registry := appRegistry(t, "a", "b", "independent")
	for _, test := range []struct {
		name    string
		hooks   []apps.Startup
		timeout time.Duration
		code    apps.ErrorCode
	}{
		{"unknown_app", []apps.Startup{{Label: "absent"}}, 0, apps.CodeInvalidConfig},
		{"duplicate_app", []apps.Startup{{Label: "a"}, {Label: "a"}}, 0, apps.CodeInvalidConfig},
		{"unknown_dependency", []apps.Startup{{Label: "a", DependsOn: []string{"absent"}}}, 0, apps.CodeInvalidConfig},
		{"duplicate_dependency", []apps.Startup{{Label: "a", DependsOn: []string{"b", "b"}}}, 0, apps.CodeInvalidConfig},
		{"self_cycle", []apps.Startup{{Label: "a", DependsOn: []string{"a"}}}, 0, apps.CodeDependencyCycle},
		{"cycle_after_ready_app", []apps.Startup{{Label: "a", DependsOn: []string{"b"}}, {Label: "b", DependsOn: []string{"a"}}}, 0, apps.CodeDependencyCycle},
		{"negative_rollback_timeout", nil, -time.Second, apps.CodeInvalidConfig},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			start := func(context.Context, apps.Config) (apps.Stop, error) { calls++; return nil, nil }
			for index := range test.hooks {
				test.hooks[index].Start = start
			}
			test.hooks = append(test.hooks, apps.Startup{Label: "independent", Start: start})
			lifetime, err := apps.Start(t.Context(), registry, apps.StartupConfig{Hooks: test.hooks, RollbackTimeout: test.timeout})
			if lifetime != nil || !errors.Is(err, &apps.Error{Code: test.code}) || calls != 0 {
				t.Fatalf("invalid startup reached callbacks: handle=%v error=%v calls=%d", lifetime, err, calls)
			}
		})
	}
}

func TestAppStartupRollsBackPartialErrorsPanicsGoexitAndCancellation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"error", "partial_error", "panic", "goexit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			startupFailure := errors.New("startup-private-secret")
			cleanupFailure := errors.New("cleanup-private-secret")
			var stopped []string
			stop := func(label string) apps.Stop {
				return func(ctx context.Context) error {
					if ctx.Err() != nil {
						t.Error("rollback reused canceled startup context")
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Error("rollback has no finite deadline")
					}
					stopped = append(stopped, label)
					if label == "a" {
						return cleanupFailure
					}
					return nil
				}
			}
			lifetime, err := apps.Start(parent, appRegistry(t, "a", "b", "c"), apps.StartupConfig{RollbackTimeout: time.Minute, Hooks: []apps.Startup{
				{Label: "a", Start: func(context.Context, apps.Config) (apps.Stop, error) { return stop("a"), nil }},
				{Label: "b", Start: func(context.Context, apps.Config) (apps.Stop, error) {
					switch mode {
					case "error":
						return nil, startupFailure
					case "partial_error":
						return stop("b"), startupFailure
					case "panic":
						panic("panic-private-secret")
					case "goexit":
						runtime.Goexit()
					case "cancel":
						cancel()
						return stop("b"), nil
					}
					return nil, nil
				}},
				{Label: "c", Start: func(context.Context, apps.Config) (apps.Stop, error) {
					t.Error("startup continued after failure")
					return nil, nil
				}},
			}})
			if lifetime != nil || !errors.Is(err, &apps.Error{Code: apps.CodeStartupFailed}) || !errors.Is(err, cleanupFailure) {
				t.Fatalf("partial startup published a handle or lost cleanup failure: %v", err)
			}
			if (mode == "error" || mode == "partial_error") && !errors.Is(err, startupFailure) {
				t.Fatal("startup cause was lost")
			}
			if (mode == "panic" || mode == "goexit") && !errors.Is(err, &apps.Error{Code: apps.CodeCallbackAborted, AppLabel: "b"}) {
				t.Fatal("abnormal callback exit was lost")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation was lost")
			}
			want := []string{"a"}
			if mode == "partial_error" || mode == "cancel" {
				want = []string{"b", "a"}
			}
			if !reflect.DeepEqual(stopped, want) {
				t.Fatalf("rollback order: %v, want %v", stopped, want)
			}
			assertAppFailureRedacted(t, err)
		})
	}
}

func TestAppStartupCancellationJoinsItsCallbackBeforeRollback(t *testing.T) {
	t.Parallel()
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var closed atomic.Int32
	finished := make(chan error, 1)
	registry := appRegistry(t, "a")
	go func() {
		lifetime, err := apps.Start(parent, registry, apps.StartupConfig{Hooks: []apps.Startup{{Label: "a", Start: func(ctx context.Context, _ apps.Config) (apps.Stop, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return func(context.Context) error { closed.Add(1); return nil }, nil
		}}}})
		if lifetime != nil {
			err = errors.New("canceled startup published a lifecycle")
		}
		finished <- err
	}()
	<-entered
	cancel()
	<-canceled
	select {
	case <-finished:
		t.Fatal("cancellation abandoned a live callback")
	default:
	}
	close(release)
	if err := <-finished; !errors.Is(err, context.Canceled) || closed.Load() != 1 {
		t.Fatalf("canceled startup did not join and clean: %v, closes=%d", err, closed.Load())
	}
}

func TestAppShutdownJoinsCallbacksAndRefusesRecursiveOrConcurrentEntry(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"success", "error", "panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			var lifetime *apps.Lifecycle
			failure := errors.New("shutdown-private-secret")
			var err error
			lifetime, err = apps.Start(t.Context(), appRegistry(t, "a", "b"), apps.StartupConfig{Hooks: []apps.Startup{
				{Label: "a", Start: func(context.Context, apps.Config) (apps.Stop, error) {
					return func(context.Context) error { calls.Add(1); return nil }, nil
				}},
				{Label: "b", Start: func(context.Context, apps.Config) (apps.Stop, error) {
					return func(ctx context.Context) error {
						calls.Add(1)
						if err := lifetime.Shutdown(ctx); !errors.Is(err, &apps.Error{Code: apps.CodeShutdownInProgress}) {
							t.Error("recursive shutdown did not fail promptly")
						}
						close(entered)
						<-release
						switch mode {
						case "error":
							return failure
						case "panic":
							panic("shutdown-panic-private-secret")
						case "goexit":
							runtime.Goexit()
						}
						return nil
					}, nil
				}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- lifetime.Shutdown(context.Background()) }()
			<-entered
			if err := lifetime.Shutdown(context.Background()); !errors.Is(err, &apps.Error{Code: apps.CodeShutdownInProgress}) {
				t.Fatal("concurrent shutdown did not fail promptly")
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if err := lifetime.Wait(canceled); !errors.Is(err, context.Canceled) {
				t.Fatal("Wait ignored caller cancellation", err)
			}
			close(release)
			err = <-finished
			if calls.Load() != 2 {
				t.Fatal("shutdown skipped or repeated an app")
			}
			if mode == "success" && err != nil {
				t.Fatal(err)
			}
			if mode == "error" && !errors.Is(err, failure) {
				t.Fatal("shutdown cause was lost")
			}
			if (mode == "panic" || mode == "goexit") && !errors.Is(err, &apps.Error{Code: apps.CodeCallbackAborted, AppLabel: "b"}) {
				t.Fatal("shutdown abort was lost")
			}
			if mode != "success" {
				assertAppFailureRedacted(t, err)
				var owned *apps.Error
				if !errors.As(err, &owned) {
					t.Fatal("missing typed failure")
				}
				owned.Detail = "caller changed result"
				if strings.Contains(fmt.Sprint(lifetime.Shutdown(context.Background())), "caller changed result") {
					t.Fatal("caller changed stored shutdown result")
				}
			}
			if next := lifetime.Wait(canceled); (next == nil) != (mode == "success") || errors.Is(next, context.Canceled) {
				t.Fatal("completed shutdown lost precedence over observer cancellation", next)
			}
			if calls.Load() != 2 {
				t.Fatal("observation repeated cleanup")
			}
			if strings.Contains(fmt.Sprintf("%#v", lifetime), "private-secret") {
				t.Fatal("lifecycle formatting exposed callback cause")
			}
		})
	}
}

func TestAppLifecycleRejectsInvalidContextsAndSupportsEmptyRegistries(t *testing.T) {
	t.Parallel()
	if lifetime, err := apps.Start(nil, apps.Registry{}, apps.StartupConfig{}); lifetime != nil || !errors.Is(err, &apps.Error{Field: "context"}) {
		t.Fatalf("nil context: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if lifetime, err := apps.Start(ctx, apps.Registry{}, apps.StartupConfig{}); lifetime != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
	for _, lifetime := range []*apps.Lifecycle{nil, {}} {
		if err := lifetime.Shutdown(t.Context()); !errors.Is(err, &apps.Error{Code: apps.CodeInvalidConfig}) {
			t.Fatal("invalid lifecycle shutdown", err)
		}
		if err := lifetime.Wait(t.Context()); !errors.Is(err, &apps.Error{Code: apps.CodeInvalidConfig}) {
			t.Fatal("invalid lifecycle wait", err)
		}
	}
	lifetime, err := apps.Start(t.Context(), apps.Registry{}, apps.StartupConfig{})
	if err != nil || len(lifetime.Order()) != 0 {
		t.Fatal("empty lifecycle", err)
	}
	if err := lifetime.Shutdown(nil); !errors.Is(err, &apps.Error{Code: apps.CodeInvalidConfig}) {
		t.Fatal("nil shutdown context", err)
	}
	if err := lifetime.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("invalid shutdown changed state", err)
	}
	if err := lifetime.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	noHooks, err := apps.Start(t.Context(), appRegistry(t, "a", "b"), apps.StartupConfig{Hooks: []apps.Startup{{Label: "a", DependsOn: []string{"b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if order := noHooks.Order(); len(order) != 2 || order[0].Label != "b" || order[1].Label != "a" {
		t.Fatal("dependency-only apps lost startup order")
	}
	if err := noHooks.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestAppLifecyclesDoNotShareContextsOrShutdownState(t *testing.T) {
	t.Parallel()
	registry := appRegistry(t, "shared_definition")
	var contexts []context.Context
	var stopped []int
	config := apps.StartupConfig{Hooks: []apps.Startup{{Label: "shared_definition", Start: func(ctx context.Context, _ apps.Config) (apps.Stop, error) {
		index := len(contexts)
		contexts = append(contexts, ctx)
		return func(context.Context) error { stopped = append(stopped, index); return nil }, nil
	}}}}
	first, err := apps.Start(t.Context(), registry, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := apps.Start(t.Context(), registry, config)
	if err != nil {
		_ = first.Shutdown(context.Background())
		t.Fatal(err)
	}
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if contexts[0].Err() != context.Canceled || contexts[1].Err() != nil || !reflect.DeepEqual(stopped, []int{0}) {
		t.Fatal("one lifecycle shut down another")
	}
	if err := second.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if contexts[1].Err() != context.Canceled || !reflect.DeepEqual(stopped, []int{0, 1}) {
		t.Fatal("independent shutdown state was lost")
	}
}

func assertAppFailureRedacted(t *testing.T, err error) {
	t.Helper()
	for _, format := range []string{"%v", "%+v", "%#v", "%q", "%x"} {
		if strings.Contains(fmt.Sprintf(format, err), "private-secret") {
			t.Fatal("error formatting exposed callback data")
		}
	}
	encoded, encodeErr := json.Marshal(err)
	if encodeErr != nil || strings.Contains(string(encoded), "private-secret") {
		t.Fatal("error JSON exposed callback data", encodeErr)
	}
}
