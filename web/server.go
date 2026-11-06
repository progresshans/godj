package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultShutdownTimeout   = 5 * time.Second
)

// ServerOptions bounds HTTP header reads and graceful cleanup.
type ServerOptions struct {
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
}

// Server runs one Application on a caller-owned listener. A Server is
// one-shot; construct another instance to serve again.
type Server struct {
	application *Application
	options     ServerOptions
	started     atomic.Bool
}

// NewServer validates server startup options without opening a listener.
func NewServer(application *Application, options ServerOptions) (*Server, error) {
	if application == nil {
		return nil, invalidServerField("application", "application is nil", nil)
	}
	options, err := options.normalized()
	if err != nil {
		return nil, err
	}
	return &Server{application: application, options: options}, nil
}

// Validate checks options without opening resources or requiring an
// application. Project startup can reject configuration before its callbacks.
func (options ServerOptions) Validate() error {
	_, err := options.normalized()
	return err
}

func (options ServerOptions) normalized() (ServerOptions, error) {
	if options.ReadHeaderTimeout == 0 {
		options.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if options.ShutdownTimeout == 0 {
		options.ShutdownTimeout = defaultShutdownTimeout
	}
	if options.ReadHeaderTimeout < 0 {
		return ServerOptions{}, invalidServerField("read_header_timeout", "timeout must be positive", nil)
	}
	if options.ShutdownTimeout < 0 {
		return ServerOptions{}, invalidServerField("shutdown_timeout", "timeout must be positive", nil)
	}
	if options.MaxHeaderBytes < 0 {
		return ServerOptions{}, invalidServerField("max_header_bytes", "limit must not be negative", nil)
	}
	return options, nil
}

// Serve blocks until the listener fails or ctx requests shutdown. Graceful
// shutdown uses a separate bounded cleanup context; a timeout force-closes
// active connections. Serve then joins all admitted application calls,
// including response-stream cleanup, before returning the shutdown error.
// Handlers and readers must cooperate with cancellation; closing a connection
// cannot forcibly stop Go code. The caller may close application resources
// only after Serve returns.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if s == nil {
		return invalidServerField("server", "server is nil", nil)
	}
	if ctx == nil {
		return invalidServerField("context", "context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if listenerIsNil(listener) {
		return invalidServerField("listener", "listener is nil", nil)
	}
	if !s.started.CompareAndSwap(false, true) {
		return invalidServerField("server", "server has already been started", nil)
	}

	requests := &serverRequests{application: s.application, done: make(chan struct{})}
	defer requests.join()
	httpServer := &http.Server{
		Handler:           requests,
		ReadHeaderTimeout: s.options.ReadHeaderTimeout,
		MaxHeaderBytes:    s.options.MaxHeaderBytes,
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- httpServer.Serve(listener)
	}()

	select {
	case serveErr := <-serveResult:
		return errors.Join(
			normalizeServeError(serveErr),
			s.drain(httpServer),
		)
	case <-ctx.Done():
		shutdownErr := s.drain(httpServer)
		serveErr := <-serveResult
		return errors.Join(shutdownErr, normalizeServeError(serveErr))
	}
}

// net/http.Close closes connections without waiting for their handlers. This
// admission barrier prevents a late connection goroutine from entering the
// application after join and keeps existing calls alive through stream Close.
type serverRequests struct {
	application *Application
	mu          sync.Mutex
	closing     bool
	active      int
	done        chan struct{}
}

func (requests *serverRequests) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requests.mu.Lock()
	if requests.closing {
		requests.mu.Unlock()
		http.Error(writer, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	requests.active++
	requests.mu.Unlock()
	defer func() {
		requests.mu.Lock()
		requests.active--
		if requests.closing && requests.active == 0 {
			close(requests.done)
		}
		requests.mu.Unlock()
	}()
	requests.application.ServeHTTP(writer, request)
}

func (requests *serverRequests) join() {
	requests.mu.Lock()
	requests.closing = true
	if requests.active == 0 {
		close(requests.done)
	}
	requests.mu.Unlock()
	<-requests.done
}

func (s *Server) drain(httpServer *http.Server) error {
	shutdownContext, cancel := context.WithTimeout(context.Background(), s.options.ShutdownTimeout)
	shutdownErr := httpServer.Shutdown(shutdownContext)
	cancel()
	if shutdownErr == nil {
		return nil
	}
	return errors.Join(
		invalidServerField("shutdown", "graceful shutdown failed", shutdownErr),
		httpServer.Close(),
	)
}

func listenerIsNil(listener net.Listener) bool {
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

func normalizeServeError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return invalidServerField("listener", "HTTP server stopped", err)
}
