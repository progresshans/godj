package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	projectserver "github.com/progresshans/godj/project/server"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

func TestProjectServerValidatesBeforeAcquiringResources(t *testing.T) {
	var calls atomic.Int32
	valid := projectserver.Config{
		Start: func(context.Context) (projectserver.Runtime, error) {
			calls.Add(1)
			return projectserver.Runtime{}, nil
		},
		Listen: func(context.Context) (net.Listener, error) { calls.Add(1); return nil, nil },
	}
	for _, test := range []struct {
		name   string
		change func(*projectserver.Config)
	}{
		{"start", func(c *projectserver.Config) { c.Start = nil }},
		{"listener", func(c *projectserver.Config) { c.Listen = nil }},
		{"cleanup timeout", func(c *projectserver.Config) { c.CleanupTimeout = -1 }},
		{"header timeout", func(c *projectserver.Config) { c.HTTP.ReadHeaderTimeout = -1 }},
		{"HTTP shutdown timeout", func(c *projectserver.Config) { c.HTTP.ShutdownTimeout = -1 }},
		{"header limit", func(c *projectserver.Config) { c.HTTP.MaxHeaderBytes = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.change(&config)
			if err := projectserver.Run(t.Context(), config); !errors.Is(err, &projectserver.Error{Code: projectserver.CodeInvalidConfig}) {
				t.Fatal("invalid configuration was admitted", err)
			}
		})
	}
	if err := projectserver.Run(nil, valid); !errors.Is(err, &projectserver.Error{Code: projectserver.CodeInvalidConfig}) {
		t.Fatal("nil context was admitted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := projectserver.Run(ctx, valid); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("preflight performed resource I/O", calls.Load(), err)
	}
}

func TestProjectServerOwnsPartialStartupAndAbnormalCallbacks(t *testing.T) {
	for _, stage := range []string{"start", "listen", "ready"} {
		for _, mode := range []string{"error", "panic", "goexit"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				application := testApplication(t, nil)
				cause := errors.New("private-startup-secret")
				var resources, stops, starts, listens, ready atomic.Int32
				var listener *trackedListener
				var applicationContext context.Context
				fail := func() error {
					switch mode {
					case "panic":
						panic("private-panic-value")
					case "goexit":
						runtime.Goexit()
					}
					return cause
				}
				config := projectserver.Config{
					Start: func(ctx context.Context) (projectserver.Runtime, error) {
						starts.Add(1)
						applicationContext = ctx
						resources.Add(1)
						transferred := false
						defer func() {
							if !transferred {
								resources.Add(-1)
							}
						}()
						owned := projectserver.Runtime{Application: application, Stop: func(cleanup context.Context) error {
							stops.Add(1)
							resources.Add(-1)
							if ctx.Err() == nil || cleanup.Err() != nil {
								return errors.New("wrong application/cleanup context lifetime")
							}
							return nil
						}}
						var err error
						if stage == "start" {
							err = fail()
						}
						transferred = true
						return owned, err
					},
					Listen: func(context.Context) (net.Listener, error) {
						listens.Add(1)
						listener = newListener(t)
						transferred := false
						defer func() {
							if !transferred {
								_ = listener.Close()
							}
						}()
						var err error
						if stage == "listen" {
							err = fail()
						}
						transferred = true
						return listener, err
					},
					Ready: func(context.Context, projectserver.ListenerInfo) error { ready.Add(1); return fail() },
				}
				err := projectserver.Run(t.Context(), config)
				code := map[string]projectserver.Code{"start": projectserver.CodeStartFailed, "listen": projectserver.CodeListenFailed, "ready": projectserver.CodeReadyFailed}[stage]
				if !errors.Is(err, &projectserver.Error{Code: code}) || resources.Load() != 0 || applicationContext.Err() == nil || starts.Load() != 1 {
					t.Fatal("partial startup lifetime or stage differs", resources.Load(), err)
				}
				wantStops := int32(1)
				if stage == "start" && mode != "error" {
					wantStops = 0
				}
				if stops.Load() != wantStops || stage == "start" && listens.Load() != 0 || stage != "ready" && ready.Load() != 0 {
					t.Fatal("failed startup advanced or repeated cleanup", stops.Load(), listens.Load(), ready.Load())
				}
				if listener != nil && listener.closes.Load() != 1 {
					t.Fatal("listener was not closed exactly once", listener.closes.Load())
				}
				if mode == "error" && !errors.Is(err, cause) || mode != "error" && !errors.Is(err, &projectserver.Error{Code: projectserver.CodeCallbackAborted}) {
					t.Fatal("callback outcome was lost", err)
				}
				assertClosedDiagnostic(t, err, "private-startup-secret", "private-panic-value")
			})
		}
	}
}

func TestProjectServerRejectsMissingApplicationAndListener(t *testing.T) {
	for _, mode := range []string{"application", "nil_listener", "typed_nil_listener"} {
		t.Run(mode, func(t *testing.T) {
			application := testApplication(t, nil)
			var stopped, listened, ready atomic.Int32
			err := projectserver.Run(t.Context(), projectserver.Config{
				Start: func(context.Context) (projectserver.Runtime, error) {
					if mode == "application" {
						application = nil
					}
					return projectserver.Runtime{Application: application, Stop: func(context.Context) error { stopped.Add(1); return nil }}, nil
				},
				Listen: func(context.Context) (net.Listener, error) {
					listened.Add(1)
					if mode == "typed_nil_listener" {
						return (*trackedListener)(nil), nil
					}
					return nil, nil
				},
				Ready: func(context.Context, projectserver.ListenerInfo) error { ready.Add(1); return nil },
			})
			code := projectserver.CodeListenFailed
			if mode == "application" {
				code = projectserver.CodeStartFailed
			}
			if !errors.Is(err, &projectserver.Error{Code: code}) || stopped.Load() != 1 || ready.Load() != 0 || mode == "application" && listened.Load() != 0 {
				t.Fatal("missing resource was admitted or cleanup was omitted", err)
			}
		})
	}
}

func TestProjectServerJoinsCanceledStartupBeforeCleanup(t *testing.T) {
	for _, stage := range []string{"start", "listen", "ready"} {
		t.Run(stage, func(t *testing.T) {
			application := testApplication(t, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, canceled, returned, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseCallback := func() { once.Do(func() { close(release) }) }
			t.Cleanup(releaseCallback)
			block := func(ctx context.Context) error {
				defer close(returned)
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return ctx.Err()
			}
			var stops atomic.Int32
			var listener *trackedListener
			config := projectserver.Config{
				Start: func(ctx context.Context) (projectserver.Runtime, error) {
					owned := projectserver.Runtime{Application: application, Stop: func(context.Context) error {
						select {
						case <-returned:
						default:
							return errors.New("cleanup overlapped callback")
						}
						stops.Add(1)
						return nil
					}}
					if stage == "start" {
						return owned, block(ctx)
					}
					return owned, nil
				},
				Listen: func(ctx context.Context) (net.Listener, error) {
					listener = newListener(t)
					if stage == "listen" {
						return listener, block(ctx)
					}
					return listener, nil
				},
				Ready: func(ctx context.Context, _ projectserver.ListenerInfo) error { return block(ctx) },
			}
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() { defer close(done); result <- projectserver.Run(ctx, config) }()
			t.Cleanup(func() { releaseCallback(); cancel(); wait(t, done) })
			wait(t, entered)
			cancel()
			wait(t, canceled)
			select {
			case err := <-result:
				t.Fatal("canceled invocation abandoned its callback", err)
			case <-time.After(30 * time.Millisecond):
			}
			if stops.Load() != 0 {
				t.Fatal("cleanup started while callback remained active")
			}
			releaseCallback()
			if err := receive(t, result); !errors.Is(err, context.Canceled) || stops.Load() != 1 {
				t.Fatal("canceled callback cleanup differs", stops.Load(), err)
			}
			if listener != nil && listener.closes.Load() != 1 {
				t.Fatal("partial listener survived cancellation")
			}
		})
	}
}

func TestProjectServerPreservesPrimaryAndCleanupFailures(t *testing.T) {
	for _, mode := range []string{"error", "panic", "goexit", "listener_goexit"} {
		t.Run(mode, func(t *testing.T) {
			application := testApplication(t, nil)
			primary, stopCause, closeCause := errors.New("private-ready-cause"), errors.New("private-stop-cause"), errors.New("private-close-cause")
			listener := newListener(t)
			listener.failure = closeCause
			if mode == "listener_goexit" {
				listener.goexit = true
			}
			var stopped atomic.Int32
			err := projectserver.Run(t.Context(), projectserver.Config{
				Start: func(context.Context) (projectserver.Runtime, error) {
					return projectserver.Runtime{Application: application, Stop: func(ctx context.Context) error {
						stopped.Add(1)
						if ctx.Err() != nil || listener.closes.Load() != 1 {
							return errors.New("cleanup context or order differs")
						}
						if mode == "panic" {
							panic("private-cleanup-panic")
						}
						if mode == "goexit" {
							runtime.Goexit()
						}
						return stopCause
					}}, nil
				},
				Listen: func(context.Context) (net.Listener, error) { return listener, nil },
				Ready:  func(context.Context, projectserver.ListenerInfo) error { return primary },
			})
			if stopped.Load() != 1 || listener.closes.Load() != 1 || !errors.Is(err, primary) || !errors.Is(err, &projectserver.Error{Code: projectserver.CodeStopFailed}) || !errors.Is(err, &projectserver.Error{Code: projectserver.CodeListenerCloseFailed}) {
				t.Fatal("primary/cleanup failure was replaced or cleanup skipped", err)
			}
			if mode != "listener_goexit" && !errors.Is(err, closeCause) || (mode == "error" || mode == "listener_goexit") && !errors.Is(err, stopCause) {
				t.Fatal("underlying failure was lost", err)
			}
			if mode != "error" && !errors.Is(err, &projectserver.Error{Code: projectserver.CodeCallbackAborted}) {
				t.Fatal("abnormal cleanup was not reported", err)
			}
			assertClosedDiagnostic(t, err, "private-ready-cause", "private-stop-cause", "private-close-cause", "private-cleanup-panic")
		})
	}
}

func TestProjectServerDrainsRequestsBeforeCancelingApplicationResources(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(fmt.Sprint("forced=", forced), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, release, requestCanceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseRequest := func() { once.Do(func() { close(release) }) }
			t.Cleanup(releaseRequest)
			var appContext context.Context
			var stopped atomic.Int32
			application := testApplication(t, func(request *web.Request) (web.Response, error) {
				close(entered)
				if forced {
					<-request.Context().Done()
					close(requestCanceled)
				}
				<-release
				if appContext.Err() != nil {
					return web.Response{}, errors.New("application canceled before request cleanup")
				}
				return web.NewResponse(http.StatusOK, nil, []byte("drained"))
			})
			listener := newListener(t)
			ready := make(chan struct{})
			deadline := 2 * time.Second
			if forced {
				deadline = 20 * time.Millisecond
			}
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				result <- projectserver.Run(ctx, projectserver.Config{
					HTTP: web.ServerOptions{ShutdownTimeout: deadline},
					Start: func(lifetime context.Context) (projectserver.Runtime, error) {
						appContext = lifetime
						return projectserver.Runtime{Application: application, Stop: func(cleanup context.Context) error {
							stopped.Add(1)
							if lifetime.Err() == nil || cleanup.Err() != nil {
								return errors.New("wrong shutdown context")
							}
							return nil
						}}, nil
					},
					Listen: func(context.Context) (net.Listener, error) { return listener, nil },
					Ready:  func(context.Context, projectserver.ListenerInfo) error { close(ready); return nil },
				})
			}()
			t.Cleanup(func() { releaseRequest(); cancel(); wait(t, done) })
			wait(t, ready)
			client := make(chan error, 1)
			clientFinished := make(chan struct{})
			t.Cleanup(func() { releaseRequest(); cancel(); wait(t, clientFinished) })
			go func() {
				defer close(clientFinished)
				httpClient := &http.Client{Timeout: 5 * time.Second}
				response, err := httpClient.Get("http://" + listener.Addr().String() + "/")
				if response != nil {
					payload, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr != nil {
						err = readErr
					} else if response.StatusCode != 200 || string(payload) != "drained" {
						err = errors.New("request did not drain")
					}
				}
				client <- err
			}()
			wait(t, entered)
			cancel()
			wait(t, listener.closed)
			if forced {
				wait(t, requestCanceled)
			}
			select {
			case err := <-result:
				t.Fatal("Run returned with an active request", err)
			case <-time.After(60 * time.Millisecond):
			}
			if appContext.Err() != nil || stopped.Load() != 0 {
				t.Fatal("application resources stopped before HTTP drain")
			}
			releaseRequest()
			requestErr, serveErr := receive(t, client), receive(t, result)
			if !forced && (requestErr != nil || serveErr != nil) || forced && (!errors.Is(serveErr, context.DeadlineExceeded) || requestErr == nil) {
				t.Fatal("HTTP drain result differs", requestErr, serveErr)
			}
			if stopped.Load() != 1 || listener.closes.Load() != 1 || appContext.Err() == nil {
				t.Fatal("resources did not stop exactly once after drain")
			}
		})
	}
}

func TestProjectServerCleanupRetainsValuesWithAnIndependentDeadline(t *testing.T) {
	type valueKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), valueKey{}, "invocation"))
	defer cancel()
	application := testApplication(t, nil)
	listener := newListener(t)
	var stopped atomic.Int32
	err := projectserver.Run(ctx, projectserver.Config{
		CleanupTimeout: 20 * time.Millisecond,
		Start: func(lifetime context.Context) (projectserver.Runtime, error) {
			return projectserver.Runtime{Application: application, Stop: func(cleanup context.Context) error {
				stopped.Add(1)
				if lifetime.Err() == nil || cleanup.Err() != nil || cleanup.Value(valueKey{}) != "invocation" {
					return errors.New("cleanup inherited cancellation or lost values")
				}
				if _, ok := cleanup.Deadline(); !ok {
					return errors.New("cleanup has no deadline")
				}
				<-cleanup.Done()
				return cleanup.Err()
			}}, nil
		},
		Listen: func(context.Context) (net.Listener, error) { return listener, nil },
		Ready:  func(context.Context, projectserver.ListenerInfo) error { cancel(); return nil },
	})
	if stopped.Load() != 1 || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, &projectserver.Error{Code: projectserver.CodeStopFailed}) {
		t.Fatal("independent cleanup deadline was replaced by parent cancellation", err)
	}
}

func TestIndependentProjectServersDoNotShareContextsOrResources(t *testing.T) {
	type instanceKey struct{}
	applications := []*web.Application{
		testApplication(t, func(*web.Request) (web.Response, error) { return web.NewResponse(200, nil, []byte("first")) }),
		testApplication(t, func(*web.Request) (web.Response, error) { return web.NewResponse(200, nil, []byte("second")) }),
	}
	listeners := []*trackedListener{newListener(t), newListener(t)}
	var contexts [2]context.Context
	var stops [2]atomic.Int32
	ready := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	finished := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	results := [2]chan error{make(chan error, 1), make(chan error, 1)}
	var cancels [2]context.CancelFunc
	config := projectserver.Config{
		Start: func(ctx context.Context) (projectserver.Runtime, error) {
			index := ctx.Value(instanceKey{}).(int)
			contexts[index] = ctx
			return projectserver.Runtime{Application: applications[index], Stop: func(context.Context) error { stops[index].Add(1); return nil }}, nil
		},
		Listen: func(ctx context.Context) (net.Listener, error) { return listeners[ctx.Value(instanceKey{}).(int)], nil },
		Ready: func(ctx context.Context, info projectserver.ListenerInfo) error {
			index := ctx.Value(instanceKey{}).(int)
			if info.Address != listeners[index].Addr().String() {
				return errors.New("listener crossed invocations")
			}
			close(ready[index])
			return nil
		},
	}
	for index := range 2 {
		ctx, cancel := context.WithCancel(context.WithValue(t.Context(), instanceKey{}, index))
		cancels[index] = cancel
		go func() { defer close(finished[index]); results[index] <- projectserver.Run(ctx, config) }()
	}
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
		for _, done := range finished {
			wait(t, done)
		}
	})
	for _, started := range ready {
		wait(t, started)
	}
	cancels[0]()
	if err := receive(t, results[0]); err != nil {
		t.Fatal(err)
	}
	if contexts[0].Err() == nil || contexts[1].Err() != nil || stops[0].Load() != 1 || stops[1].Load() != 0 {
		t.Fatal("one project's shutdown affected the other application")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listeners[1].Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(payload) != "second" {
		t.Fatal("remaining server lost its own application", err)
	}
	cancels[1]()
	if err := receive(t, results[1]); err != nil || stops[1].Load() != 1 {
		t.Fatal("second project cleanup", err)
	}
}

func testApplication(t *testing.T, handler web.Handler) *web.Application {
	t.Helper()
	configured, err := settings.New(settings.Definition{ProjectName: "server-test", InstalledApps: []apps.Config{{Name: "example.com/probe", Label: "probe"}}})
	if err != nil {
		t.Fatal(err)
	}
	if handler == nil {
		handler = func(*web.Request) (web.Response, error) { return web.NewResponse(http.StatusOK, nil, []byte("ready")) }
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "probe:root", Method: "GET", Path: "/", Handler: handler}}})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

type trackedListener struct {
	net.Listener
	closes  atomic.Int32
	closed  chan struct{}
	once    sync.Once
	failure error
	goexit  bool
}

func newListener(t *testing.T) *trackedListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return &trackedListener{Listener: listener, closed: make(chan struct{})}
}

func (listener *trackedListener) Close() error {
	listener.closes.Add(1)
	err := listener.Listener.Close()
	listener.once.Do(func() { close(listener.closed) })
	if listener.goexit {
		runtime.Goexit()
	}
	return errors.Join(err, listener.failure)
}

func assertClosedDiagnostic(t *testing.T, err error, secrets ...string) {
	t.Helper()
	wire, encodeErr := json.Marshal(err)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	diagnostic := fmt.Sprintf("%v %+v %#v", err, err, err) + string(wire)
	for _, secret := range secrets {
		if strings.Contains(diagnostic, secret) {
			t.Fatal("callback detail escaped closed diagnostics")
		}
	}
}

func wait(t *testing.T, done <-chan struct{}) { t.Helper(); receive(t, done) }
func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("project server operation timed out")
		var zero T
		return zero
	}
}
