// Package server owns one project's application, listener and shutdown order.
// Project code supplies settings, startup resources and the web application;
// no settings, migrations, credentials or global app registrations are inferred.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/progresshans/godj/internal/callback"
	"github.com/progresshans/godj/web"
)

// Runtime transfers application ownership to Run when Start returns. Stop
// releases its resources after HTTP request/stream cleanup has finished. A
// nonnil Stop is called exactly once, including a partial Start with an error.
// Start owns all resources until it returns and must clean them on abnormal
// exit. An application without resources may omit Stop.
type Runtime struct {
	Application *web.Application
	Stop        func(context.Context) error
}

// ListenerInfo is a detached observation for a successful listener. Publishing
// it does not verify that a remote client can reach the application.
type ListenerInfo struct {
	Network string
	Address string
}

// Config describes one invocation. Run copies these values; mutable state
// captured by callbacks remains caller-owned. Start completes before Listen,
// allowing authorization and application policy to reject network publication.
// Listen transfers any nonnil listener even if it also returns an error.
// Ready publishes the bound address before HTTP serving starts and must not
// synchronously wait for an HTTP request to that listener.
type Config struct {
	Start  func(context.Context) (Runtime, error)
	Listen func(context.Context) (net.Listener, error)
	Ready  func(context.Context, ListenerInfo) error
	HTTP   web.ServerOptions
	// CleanupTimeout applies to cooperative Runtime.Stop after HTTP drain.
	// Zero selects ten seconds. It does not forcibly stop callback Go code.
	CleanupTimeout time.Duration
}

func (Config) Format(state fmt.State, _ rune)  { fmt.Fprint(state, "server.Config{redacted}") }
func (Runtime) Format(state fmt.State, _ rune) { fmt.Fprint(state, "server.Runtime{redacted}") }
func (Config) MarshalJSON() ([]byte, error)    { return []byte(`{}`), nil }
func (Runtime) MarshalJSON() ([]byte, error)   { return []byte(`{}`), nil }

// Run owns the complete Start -> Listen -> Ready -> Serve -> Stop lifetime.
// Its application context follows caller cancellation during startup. Once
// ready, caller cancellation initiates HTTP drain; the application context
// stays live until every admitted request has returned. Stop then receives an
// independent cleanup deadline and retains caller context values.
// Every callback is joined even after cancellation, panic or Goexit. A callback
// that ignores cancellation can delay return; resources are never released
// while it is still running. Successful readiness followed by cancellation is
// graceful shutdown unless serving or cleanup fails.
func Run(ctx context.Context, config Config) (result error) {
	if ctx == nil || config.Start == nil || config.Listen == nil || config.CleanupTimeout < 0 {
		return failure(CodeInvalidConfig, nil)
	}
	if err := config.HTTP.Validate(); err != nil {
		return failure(CodeInvalidConfig, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = 10 * time.Second
	}
	applicationContext, cancelApplication := context.WithCancelCause(context.WithoutCancel(ctx))
	forwarded := make(chan struct{})
	stopForwarding := context.AfterFunc(ctx, func() {
		cancelApplication(context.Cause(ctx))
		close(forwarded)
	})
	var detachOnce sync.Once
	detach := func() {
		detachOnce.Do(func() {
			if !stopForwarding() {
				<-forwarded
			}
		})
	}
	var runtime Runtime
	var listener *ownedListener
	defer func() {
		detach()
		// The HTTP owner has joined its application calls before this defer.
		// On startup failure, close a partially acquired listener first.
		if listener != nil {
			if err := listener.Close(); err != nil {
				result = errors.Join(result, failure(CodeListenerCloseFailed, err))
			}
		}
		cancelApplication(result)
		if runtime.Stop != nil {
			cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), config.CleanupTimeout)
			defer cancelCleanup()
			if _, err := callback.Invoke(func() (struct{}, error) { return struct{}{}, runtime.Stop(cleanupContext) }, failure(CodeCallbackAborted, nil)); err != nil {
				result = errors.Join(result, failure(CodeStopFailed, err))
			}
		}
	}()
	var err error
	runtime, err = callback.Invoke(func() (Runtime, error) { return config.Start(applicationContext) }, failure(CodeCallbackAborted, nil))
	if err != nil {
		return startupFailure(ctx, CodeStartFailed, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	httpServer, err := web.NewServer(runtime.Application, config.HTTP)
	if err != nil {
		return failure(CodeStartFailed, err)
	}
	opened, err := callback.Invoke(func() (net.Listener, error) { return config.Listen(applicationContext) }, failure(CodeCallbackAborted, nil))
	if !nilListener(opened) {
		listener = &ownedListener{Listener: opened}
	}
	if err != nil {
		return startupFailure(ctx, CodeListenFailed, err)
	}
	if listener == nil {
		return failure(CodeListenFailed, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.Ready != nil {
		_, err := callback.Invoke(func() (struct{}, error) {
			address := listener.Addr()
			if address == nil {
				return struct{}{}, failure(CodeListenFailed, nil)
			}
			return struct{}{}, config.Ready(applicationContext, ListenerInfo{Network: address.Network(), Address: address.String()})
		}, failure(CodeCallbackAborted, nil))
		if err != nil {
			return startupFailure(ctx, CodeReadyFailed, err)
		}
	}
	// Cancellation after successful readiness is normal shutdown. It may race
	// this detach before the first HTTP request, but cannot cancel app workers
	// during the drain of an admitted request.
	detach()
	if ctx.Err() != nil {
		return nil
	}
	if err := httpServer.Serve(ctx, listener); err != nil {
		// Cancellation may arrive between the preceding check and Serve's
		// startup check. Errors from an actual drain keep their own causes.
		if err == ctx.Err() {
			return nil
		}
		return failure(CodeServeFailed, err)
	}
	return nil
}

func startupFailure(ctx context.Context, code Code, err error) error {
	return failure(code, errors.Join(err, ctx.Err()))
}

type ownedListener struct {
	net.Listener
	once     sync.Once
	closeErr error
}

func (listener *ownedListener) Close() error {
	listener.once.Do(func() {
		_, listener.closeErr = callback.Invoke(func() (struct{}, error) { return struct{}{}, listener.Listener.Close() }, failure(CodeCallbackAborted, nil))
	})
	return listener.closeErr
}

func nilListener(listener net.Listener) bool {
	if listener == nil {
		return true
	}
	value := reflect.ValueOf(listener)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
