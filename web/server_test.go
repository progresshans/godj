package web_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/web"
)

func TestServerGracefullyDrainsActiveRequest(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	application := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:list", Method: "GET", Path: "/", Handler: func(*web.Request) (web.Response, error) {
			close(entered)
			<-release
			return testResponse(http.StatusOK, "drained")
		},
	}}})
	server, err := web.NewServer(application, web.ServerOptions{ShutdownTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	listener := observeClose(t)
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(ctx, listener) }()

	clientResult := make(chan httpResult, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/")
		if requestErr != nil {
			clientResult <- httpResult{err: requestErr}
			return
		}
		defer response.Body.Close()
		body, readErr := io.ReadAll(response.Body)
		clientResult <- httpResult{status: response.StatusCode, body: string(body), err: readErr}
	}()

	waitFor(t, entered, "handler entry")
	cancel()
	waitFor(t, listener.closed, "listener close")
	close(release)
	client := waitForValue(t, clientResult, "client response")
	if client.err != nil || client.status != http.StatusOK || client.body != "drained" {
		t.Fatalf("client response = %#v", client)
	}
	if err := waitForValue(t, serveResult, "server result"); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	secondListener := observeClose(t)
	defer secondListener.Close()
	if err := server.Serve(context.Background(), secondListener); !errors.Is(err, &web.Error{Code: web.CodeServerState}) {
		t.Fatalf("second Serve() error = %v", err)
	}
}

func TestServerForceClosesAfterShutdownDeadline(t *testing.T) {
	entered := make(chan struct{})
	requestCanceled := make(chan struct{})
	application := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:list", Method: "GET", Path: "/", Handler: func(request *web.Request) (web.Response, error) {
			close(entered)
			<-request.Context().Done()
			close(requestCanceled)
			return testResponse(http.StatusOK, "late")
		},
	}}})
	server, err := web.NewServer(application, web.ServerOptions{ShutdownTimeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	listener := observeClose(t)
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(ctx, listener) }()
	clientResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/")
		if response != nil {
			_ = response.Body.Close()
		}
		clientResult <- requestErr
	}()

	waitFor(t, entered, "handler entry")
	cancel()
	waitFor(t, listener.closed, "listener close")
	serveErr := waitForValue(t, serveResult, "server result")
	if !errors.Is(serveErr, context.DeadlineExceeded) {
		t.Fatalf("Serve() error = %v, want deadline exceeded", serveErr)
	}
	waitFor(t, requestCanceled, "request cancellation")
	_ = waitForValue(t, clientResult, "client result")
}

func TestServerPermanentAcceptErrorForceClosesActiveRequestAfterDrainDeadline(t *testing.T) {
	entered := make(chan struct{})
	requestCanceled := make(chan struct{})
	application := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:list", Method: "GET", Path: "/", Handler: func(request *web.Request) (web.Response, error) {
			close(entered)
			<-request.Context().Done()
			close(requestCanceled)
			return testResponse(http.StatusOK, "late")
		},
	}}})
	server, err := web.NewServer(application, web.ServerOptions{ShutdownTimeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	listener := injectPermanentAcceptError(t)
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(context.Background(), listener) }()
	clientResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/")
		if response != nil {
			_ = response.Body.Close()
		}
		clientResult <- requestErr
	}()

	waitFor(t, entered, "handler entry")
	close(listener.fail)
	waitFor(t, requestCanceled, "request cancellation after permanent accept failure")
	serveErr := waitForValue(t, serveResult, "server result")
	if !errors.Is(serveErr, errPermanentAccept) {
		t.Fatalf("Serve() error = %v, want permanent accept error", serveErr)
	}
	if !errors.Is(serveErr, context.DeadlineExceeded) {
		t.Fatalf("Serve() error = %v, want drain deadline exceeded", serveErr)
	}
	_ = waitForValue(t, clientResult, "client result")
}

func TestServerIsOneShotAndRejectsCanceledStartup(t *testing.T) {
	application := newTestApplication(t, web.Config{})
	server, err := web.NewServer(application, web.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	listener := observeClose(t)
	defer listener.Close()
	if err := server.Serve(ctx, listener); !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve(canceled) error = %v", err)
	}
}

type httpResult struct {
	status int
	body   string
	err    error
}

type observedListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
}

var errPermanentAccept = errors.New("permanent accept failure")

type permanentErrorListener struct {
	net.Listener
	fail     chan struct{}
	accepted bool
	mu       sync.Mutex
}

func injectPermanentAcceptError(t *testing.T) *permanentErrorListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return &permanentErrorListener{Listener: listener, fail: make(chan struct{})}
}

func (l *permanentErrorListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.accepted {
		l.accepted = true
		l.mu.Unlock()
		return l.Listener.Accept()
	}
	l.mu.Unlock()
	<-l.fail
	return nil, errPermanentAccept
}

func observeClose(t *testing.T) *observedListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return &observedListener{Listener: listener, closed: make(chan struct{})}
}

func (l *observedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

func waitFor(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForValue[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}
