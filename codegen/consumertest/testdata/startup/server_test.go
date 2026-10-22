package consumer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/notes"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/apps"
	projectserver "github.com/progresshans/godj/project/server"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/web"
)

func TestProjectServerOwnsGeneratedAppsThroughRequestDrain(t *testing.T) {
	configured, err := settings.New(settings.Definition{ProjectName: "served-project", InstalledApps: project.InstalledApps()})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := database.SQLite(filepath.Join(t.TempDir(), "served.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	prepareDatabase(t, selected)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready, entered := make(chan string, 1), make(chan struct{})
	release, done := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	var clientDone <-chan struct{}
	t.Cleanup(func() {
		releaseRequest()
		cancel()
		serverWait(t, done)
		if clientDone != nil {
			serverWait(t, clientDone)
		}
	})
	var backend database.Backend
	var listener *serverListener
	var appContext context.Context
	var opened, closed, workers atomic.Int32
	var cleanupOrder []string
	result := make(chan error, 1)
	go func() {
		defer close(done)
		result <- projectserver.Run(ctx, projectserver.Config{
			HTTP: web.ServerOptions{ShutdownTimeout: 5 * time.Second},
			Start: func(startup context.Context) (projectserver.Runtime, error) {
				lifecycle, err := apps.Start(startup, configured.Apps(), apps.StartupConfig{Hooks: []apps.Startup{
					{Label: "notes", DependsOn: []string{"_storage"}, Start: func(lifetime context.Context, _ apps.Config) (apps.Stop, error) {
						appContext = lifetime
						ownedBackend := backend
						workerDone := make(chan struct{})
						workers.Add(1)
						go func() { defer close(workerDone); defer workers.Add(-1); <-lifetime.Done() }()
						return func(cleanup context.Context) error {
							select {
							case <-workerDone:
							case <-cleanup.Done():
								return cleanup.Err()
							}
							if _, err := notes.NoteObjects.Using(ownedBackend).Count(cleanup); err != nil {
								return err
							}
							cleanupOrder = append(cleanupOrder, "notes")
							return nil
						}, nil
					}},
					{Label: "_storage", Start: func(openContext context.Context, _ apps.Config) (apps.Stop, error) {
						ownedBackend, err := database.Open(openContext, selected)
						if err != nil {
							return nil, err
						}
						backend = ownedBackend
						opened.Add(1)
						return func(context.Context) error {
							cleanupOrder = append(cleanupOrder, "_storage")
							closed.Add(1)
							return ownedBackend.Close()
						}, nil
					}},
				}})
				if err != nil {
					return projectserver.Runtime{}, err
				}
				transferred := false
				defer func() {
					if !transferred {
						_ = lifecycle.Shutdown(context.WithoutCancel(startup))
					}
				}()
				application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{
					Name: "notes:create", Method: "POST", Path: "/notes/", Handler: func(request *web.Request) (web.Response, error) {
						close(entered)
						<-release
						if appContext.Err() != nil || workers.Load() != 1 {
							return web.Response{}, errors.New("app stopped during request")
						}
						if _, err := notes.NoteObjects.Create(request.Context(), backend, notes.NewNoteCreate("written during drain")); err != nil {
							return web.Response{}, err
						}
						return web.NewResponse(http.StatusCreated, nil, []byte("created"))
					},
				}}})
				transferred = true
				return projectserver.Runtime{Application: application, Stop: lifecycle.Shutdown}, err
			},
			Listen: func(listenContext context.Context) (net.Listener, error) {
				var config net.ListenConfig
				opened, err := config.Listen(listenContext, "tcp", "127.0.0.1:0")
				if err != nil {
					return nil, err
				}
				listener = &serverListener{Listener: opened, closed: make(chan struct{})}
				return listener, nil
			},
			Ready: func(_ context.Context, info projectserver.ListenerInfo) error {
				ready <- "http://" + info.Address + "/notes/"
				return nil
			},
		})
	}()
	var address string
	select {
	case address = <-ready:
	case err := <-result:
		t.Fatal("project stopped before readiness", err)
	case <-time.After(10 * time.Second):
		t.Fatal("project readiness timed out")
	}
	type responseResult struct {
		status int
		body   string
		err    error
	}
	response := make(chan responseResult, 1)
	clientFinished := make(chan struct{})
	clientDone = clientFinished
	go func() {
		defer close(clientFinished)
		client := &http.Client{Timeout: 10 * time.Second}
		reply, err := client.Post(address, "text/plain", nil)
		if err != nil {
			response <- responseResult{err: err}
			return
		}
		payload, err := io.ReadAll(reply.Body)
		_ = reply.Body.Close()
		response <- responseResult{reply.StatusCode, string(payload), err}
	}()
	serverWait(t, entered)
	cancel()
	serverWait(t, listener.closed)
	if appContext.Err() != nil || workers.Load() != 1 || closed.Load() != 0 {
		t.Fatal("server cancellation stopped installed apps before their request drained")
	}
	releaseRequest()
	var reply responseResult
	select {
	case reply = <-response:
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP request did not drain")
	}
	if reply.err != nil || reply.status != 201 || reply.body != "created" {
		t.Fatal("generated write during drain failed", reply)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not finish cleanup")
	}
	if opened.Load() != 1 || closed.Load() != 1 || workers.Load() != 0 || listener.closes.Load() != 1 || !reflect.DeepEqual(cleanupOrder, []string{"notes", "_storage"}) {
		t.Fatal("server did not release the generated apps in dependency order", cleanupOrder)
	}
	if _, err := notes.NoteObjects.Using(backend).Count(t.Context()); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatal("server retained the app database", err)
	}
	reopened, err := database.Open(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := notes.NoteObjects.Using(reopened).All(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID <= 0 || rows[0].Title != "written during drain" {
		t.Fatal("server shutdown lost the generated model write", err)
	}
}

type serverListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
	closes atomic.Int32
}

func (listener *serverListener) Close() error {
	listener.closes.Add(1)
	err := listener.Listener.Close()
	listener.once.Do(func() { close(listener.closed) })
	return err
}

func serverWait(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(10 * time.Second):
		t.Fatal("project server lifetime timed out")
	}
}
