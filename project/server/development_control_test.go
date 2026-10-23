//go:build darwin || linux

package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/internal/runservercontrol"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

func developmentApplication(t *testing.T, handler web.Handler) *web.Application {
	t.Helper()
	configured, err := settings.New(settings.Definition{ProjectName: "development-test", InstalledApps: []apps.Config{{Name: "example.com/probe", Label: "probe"}}})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "probe:root", Method: "GET", Path: "/", Handler: handler}}})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func controlWait[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("development control did not finish")
		var zero T
		return zero
	}
}

func TestDevelopmentControlPreparesBeforeAcceptAndRetiresBeforeRequestDrain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	parent, child := net.Pipe()
	defer parent.Close()
	defer child.Close()
	_ = parent.SetDeadline(time.Now().Add(5 * time.Second))
	started, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls atomic.Int32
	var appContext context.Context
	application := developmentApplication(t, func(*web.Request) (web.Response, error) {
		calls.Add(1)
		close(started)
		<-release
		if appContext.Err() != nil {
			return web.Response{}, errors.New("app canceled during admitted request")
		}
		return web.NewResponse(http.StatusCreated, nil, []byte("committed"))
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- serveControlled(ctx, Config{Start: func(ctx context.Context) (Runtime, error) {
			appContext = ctx
			return Runtime{Application: application, Stop: func(context.Context) error { close(stopped); return nil }}, nil
		}}, listener, child)
	}()
	if err := runservercontrol.Expect(parent, runservercontrol.Prepared); err != nil {
		t.Fatal(err)
	}
	// The OS may queue a TCP connection, but no request reaches the application
	// until the parent explicitly activates this prepared generation.
	request, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer request.Close()
	if _, err := io.WriteString(request, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = request.SetReadDeadline(time.Now().Add(25 * time.Millisecond))
	var unexpected [1]byte
	if n, err := request.Read(unexpected[:]); n != 0 || err == nil {
		t.Fatal("unpublished candidate responded")
	}
	_ = request.SetReadDeadline(time.Time{})
	if calls.Load() != 0 {
		t.Fatal("unpublished candidate admitted a request")
	}
	if runservercontrol.Send(parent, runservercontrol.Activate) != nil || runservercontrol.Expect(parent, runservercontrol.Active) != nil {
		t.Fatal("activate failed")
	}
	controlWait(t, started)
	if runservercontrol.Send(parent, runservercontrol.Retire) != nil || runservercontrol.Expect(parent, runservercontrol.Retired) != nil {
		t.Fatal("retire failed")
	}
	if late, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		late.Close()
		t.Fatal("retired listener still admits connections")
	}
	select {
	case <-stopped:
		t.Fatal("resources stopped before admitted write")
	default:
	}
	close(release)
	_ = request.SetReadDeadline(time.Now().Add(5 * time.Second))
	response, err := io.ReadAll(request)
	if err != nil || !strings.Contains(string(response), "201 Created") || !strings.HasSuffix(string(response), "committed") {
		t.Fatal("admitted response failed", err, string(response))
	}
	if err := controlWait(t, finished); err != nil {
		t.Fatal(err)
	}
	controlWait(t, stopped)
}

func TestDevelopmentControlFailureAndParentLossCloseAllOwnedResources(t *testing.T) {
	for _, phase := range []string{"startup", "ready", "before-publication", "wrong-activation", "active-parent-loss", "operator-cancel"} {
		t.Run(phase, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			parent, child := net.Pipe()
			defer parent.Close()
			defer child.Close()
			_ = parent.SetDeadline(time.Now().Add(5 * time.Second))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var stops atomic.Int32
			private := errors.New("private-secret-startup-cause")
			application := developmentApplication(t, func(*web.Request) (web.Response, error) { return web.NewResponse(http.StatusOK, nil, []byte("ready")) })
			config := Config{Start: func(context.Context) (Runtime, error) {
				result := Runtime{Application: application, Stop: func(context.Context) error { stops.Add(1); return nil }}
				if phase == "startup" {
					return result, private
				}
				return result, nil
			}, Ready: func(context.Context, ListenerInfo) error {
				if phase == "ready" {
					return private
				}
				return nil
			}}
			finished := make(chan error, 1)
			go func() { finished <- serveControlled(ctx, config, listener, child) }()
			if phase != "startup" && phase != "ready" {
				if runservercontrol.Expect(parent, runservercontrol.Prepared) != nil {
					t.Fatal("candidate did not prepare")
				}
				switch phase {
				case "before-publication":
					_ = parent.Close()
				case "wrong-activation":
					_ = runservercontrol.Send(parent, runservercontrol.Retire)
				default:
					if runservercontrol.Send(parent, runservercontrol.Activate) != nil || runservercontrol.Expect(parent, runservercontrol.Active) != nil {
						t.Fatal("activation failed")
					}
					if phase == "operator-cancel" {
						cancel()
					} else {
						_ = parent.Close()
					}
				}
			}
			err = controlWait(t, finished)
			if (err == nil) != (phase == "operator-cancel") || stops.Load() != 1 || err != nil && strings.Contains(err.Error(), "private-secret") {
				t.Fatal("control cleanup/result", err, stops.Load())
			}
			if conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
				conn.Close()
				t.Fatal("failed candidate listener remained")
			}
		})
	}
}
