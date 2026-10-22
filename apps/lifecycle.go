package apps

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/progresshans/godj/internal/callback"
)

// Stop releases an app's resources. The callback must honor its context and
// return only when its owned work has finished. It is invoked at most once.
type Stop func(context.Context) error

// Startup attaches a callback and dependency labels to an installed app.
// A nil Start is a valid dependency-only declaration. A callback owns newly
// acquired resources until it returns Stop; if it also returns an error, that
// Stop is still called during rollback.
type Startup struct {
	Label     string
	DependsOn []string
	Start     func(context.Context, Config) (Stop, error)
}

type StartupConfig struct {
	Hooks []Startup
	// RollbackTimeout bounds cooperative startup-failure cleanup independently
	// of startup cancellation. Zero selects ten seconds; negative is invalid.
	RollbackTimeout time.Duration
}

// Lifecycle owns the context and callbacks of one successful startup. It does
// not watch cancellation in a background goroutine: the caller must Shutdown.
// Its zero value is invalid; construct it with Start and do not copy it.
type Lifecycle struct {
	mu          sync.Mutex
	phase       uint8
	done        chan struct{}
	cancel      context.CancelFunc
	stops       []appStop
	order       []Config
	shutdownErr error
}

type appStop struct {
	label string
	stop  Stop
}
type startupStep struct {
	app  Config
	hook Startup
}

// Start snapshots and validates the whole dependency graph before callbacks.
// Among currently ready apps, declaration order wins. Callbacks run serially;
// each callback goroutine is joined even on cancellation, panic or Goexit.
// Cancellation is cooperative and never abandons a callback with live resources.
func Start(ctx context.Context, registry Registry, config StartupConfig) (*Lifecycle, error) {
	if ctx == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.RollbackTimeout < 0 {
		return nil, &Error{Code: CodeInvalidConfig, Field: "rollback_timeout", Detail: "rollback timeout is negative"}
	}
	rollbackTimeout := config.RollbackTimeout
	if rollbackTimeout == 0 {
		rollbackTimeout = 10 * time.Second
	}
	plan, err := startupPlan(registry, config.Hooks)
	if err != nil {
		return nil, err
	}
	lifetimeContext, cancel := context.WithCancel(ctx)
	lifetime := &Lifecycle{done: make(chan struct{}), cancel: cancel}
	rollback := func(cause error) (*Lifecycle, error) {
		canceled := ctx.Err()
		cleanupContext, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cleanupCancel()
		cleanupErr := lifetime.Shutdown(cleanupContext)
		failure := &Error{Code: CodeStartupFailed, Field: "startup", Detail: "app startup did not complete", causes: []error{cause}}
		if appFailure, ok := cause.(*Error); ok && appFailure != nil {
			failure.AppLabel = appFailure.AppLabel
		}
		if canceled != nil {
			failure.causes = append(failure.causes, canceled)
		}
		if cleanupErr != nil {
			failure.causes = append(failure.causes, cleanupErr)
		}
		return nil, failure
	}
	for _, step := range plan {
		if err := lifetimeContext.Err(); err != nil {
			return rollback(err)
		}
		if step.hook.Start != nil {
			stop, err := invokeStartup(lifetimeContext, step.app, step.hook.Start)
			if stop != nil {
				lifetime.stops = append(lifetime.stops, appStop{label: step.app.Label, stop: stop})
			}
			if err != nil {
				return rollback(&Error{Code: CodeStartupFailed, Field: "startup", AppLabel: step.app.Label, Detail: "app startup callback failed", causes: []error{err}})
			}
		}
		lifetime.order = append(lifetime.order, step.app)
	}
	if err := lifetimeContext.Err(); err != nil {
		return rollback(err)
	}
	return lifetime, nil
}

func (*Lifecycle) Format(state fmt.State, _ rune) { fmt.Fprint(state, "apps.Lifecycle{redacted}") }

// Order returns the detached app initialization order, including apps without
// a callback. Shutdown runs the returned Stop functions in reverse order.
func (l *Lifecycle) Order() []Config {
	if l == nil {
		return nil
	}
	return append([]Config(nil), l.order...)
}

// Shutdown cancels the lifetime context and joins every Stop, even if an
// earlier callback fails. Recursive/concurrent calls during shutdown return
// CodeShutdownInProgress immediately; use Wait to observe completion instead.
// Later calls return an independent copy of the completed result.
func (l *Lifecycle) Shutdown(ctx context.Context) error {
	if l == nil || l.done == nil || ctx == nil {
		return &Error{Code: CodeInvalidConfig, Field: "shutdown", Detail: "lifecycle or context is invalid"}
	}
	l.mu.Lock()
	if l.phase == 1 {
		l.mu.Unlock()
		return &Error{Code: CodeShutdownInProgress, Field: "shutdown", Detail: "shutdown is already in progress"}
	}
	if l.phase == 2 {
		result := cloneLifecycleError(l.shutdownErr)
		l.mu.Unlock()
		return result
	}
	l.phase = 1
	l.mu.Unlock()
	l.cancel()
	var failures []error
	for index := len(l.stops) - 1; index >= 0; index-- {
		entry := l.stops[index]
		if err := invokeStop(ctx, entry.label, entry.stop); err != nil {
			failures = append(failures, &Error{Code: CodeShutdownFailed, Field: "shutdown", AppLabel: entry.label, Detail: "app shutdown callback failed", causes: []error{err}})
		}
	}
	var result error
	if len(failures) != 0 {
		result = &Error{Code: CodeShutdownFailed, Field: "shutdown", Detail: "one or more app shutdown callbacks failed", causes: failures}
	}
	l.mu.Lock()
	l.shutdownErr = result
	l.phase = 2
	close(l.done)
	l.mu.Unlock()
	return cloneLifecycleError(result)
}

// Wait observes shutdown completion without starting it. A callback must not
// wait for the lifecycle whose shutdown is currently waiting for that callback.
func (l *Lifecycle) Wait(ctx context.Context) error {
	if l == nil || l.done == nil || ctx == nil {
		return &Error{Code: CodeInvalidConfig, Field: "shutdown", Detail: "lifecycle or context is invalid"}
	}
	select {
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		return cloneLifecycleError(l.shutdownErr)
	case <-ctx.Done():
		select {
		case <-l.done:
			l.mu.Lock()
			defer l.mu.Unlock()
			return cloneLifecycleError(l.shutdownErr)
		default:
		}
		return ctx.Err()
	}
}

func startupPlan(registry Registry, definitions []Startup) ([]startupStep, error) {
	apps := registry.All()
	indexes := make(map[string]int, len(apps))
	for index, app := range apps {
		indexes[app.Label] = index
	}
	hooks := make([]Startup, len(apps))
	seen := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		index, ok := indexes[definition.Label]
		if !ok || seen[definition.Label] {
			return nil, &Error{Code: CodeInvalidConfig, Field: "startup", Detail: "startup must identify one installed app exactly once"}
		}
		seen[definition.Label] = true
		definition.DependsOn = append([]string(nil), definition.DependsOn...)
		dependencies := make(map[string]bool, len(definition.DependsOn))
		for _, dependency := range definition.DependsOn {
			if _, ok := indexes[dependency]; !ok || dependencies[dependency] {
				return nil, &Error{Code: CodeInvalidConfig, Field: "dependencies", AppLabel: definition.Label, Detail: "dependencies must identify distinct installed apps"}
			}
			dependencies[dependency] = true
		}
		hooks[index] = definition
	}
	finished := make(map[string]bool, len(apps))
	plan := make([]startupStep, 0, len(apps))
	for len(plan) < len(apps) {
		selected := -1
		for index, app := range apps {
			if finished[app.Label] {
				continue
			}
			ready := true
			for _, dependency := range hooks[index].DependsOn {
				ready = ready && finished[dependency]
			}
			if ready {
				selected = index
				break
			}
		}
		if selected < 0 {
			return nil, &Error{Code: CodeDependencyCycle, Field: "dependencies", Detail: "app startup dependency graph contains a cycle"}
		}
		finished[apps[selected].Label] = true
		plan = append(plan, startupStep{app: apps[selected], hook: hooks[selected]})
	}
	return plan, nil
}

func invokeStartup(ctx context.Context, app Config, start func(context.Context, Config) (Stop, error)) (Stop, error) {
	return callback.Invoke(func() (Stop, error) { return start(ctx, app) },
		&Error{Code: CodeCallbackAborted, Field: "startup", AppLabel: app.Label, Detail: "callback did not return normally"})
}

func invokeStop(ctx context.Context, label string, stop Stop) error {
	_, err := callback.Invoke(func() (struct{}, error) { return struct{}{}, stop(ctx) },
		&Error{Code: CodeCallbackAborted, Field: "shutdown", AppLabel: label, Detail: "callback did not return normally"})
	return err
}

func cloneLifecycleError(err error) error {
	failure, ok := err.(*Error)
	if !ok || failure == nil {
		return err
	}
	copy := *failure
	copy.causes = make([]error, len(failure.causes))
	for index, cause := range failure.causes {
		copy.causes[index] = cloneLifecycleError(cause)
	}
	return &copy
}
