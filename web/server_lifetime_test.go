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

func TestServerJoinsApplicationCleanupAfterForcedConnectionClose(t *testing.T) {
	for _, mode := range []string{"handler", "stream_close", "accept_failure"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			cleanupEntered := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})
			var releaseOnce sync.Once
			releaseCleanup := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseCleanup)
			handler := func(request *web.Request) (web.Response, error) {
				close(entered)
				<-request.Context().Done()
				close(cleanupEntered)
				<-release
				close(finished)
				return testResponse(http.StatusOK, "cleanup completed")
			}
			if mode == "stream_close" {
				response, err := web.NewStreamResponse(http.StatusOK, nil, func(ctx context.Context) (web.Stream, error) {
					reader := &streamProbe{
						read: func([]byte) (int, error) {
							close(entered)
							<-ctx.Done()
							return 0, ctx.Err()
						},
						close: func() error {
							close(cleanupEntered)
							<-release
							close(finished)
							return nil
						},
					}
					return web.Stream{Reader: reader, Size: 1}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				handler = func(*web.Request) (web.Response, error) { return response, nil }
			}
			application := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:cleanup", Method: "GET", Path: "/", Handler: handler}}})
			server, err := web.NewServer(application, web.ServerOptions{ShutdownTimeout: 20 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var listener net.Listener
			stop := cancel
			if mode == "accept_failure" {
				failedListener := injectPermanentAcceptError(t)
				listener = failedListener
				var once sync.Once
				stop = func() { once.Do(func() { close(failedListener.fail) }) }
			} else {
				listener = observeClose(t)
			}
			t.Cleanup(stop)
			t.Cleanup(func() { _ = listener.Close() })
			result := make(chan error, 1)
			go func() { result <- server.Serve(ctx, listener) }()
			clientResult := make(chan error, 1)
			go func() {
				response, err := http.Get("http://" + listener.Addr().String() + "/")
				if response != nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				clientResult <- err
			}()
			waitFor(t, entered, "active application")
			stop()
			waitFor(t, cleanupEntered, "application cleanup after forced connection close")
			var serveErr error
			returned := false
			select {
			case serveErr = <-result:
				returned = true
			case <-time.After(200 * time.Millisecond):
			}
			releaseCleanup()
			waitFor(t, finished, "application cleanup completion")
			if !returned {
				serveErr = waitForValue(t, result, "joined server result")
			}
			_ = waitForValue(t, clientResult, "closed HTTP client")
			if returned {
				t.Fatal("Serve returned while application cleanup still owned resources", serveErr)
			}
			if !errors.Is(serveErr, context.DeadlineExceeded) || mode == "accept_failure" && !errors.Is(serveErr, errPermanentAccept) {
				t.Fatal("joining application cleanup lost the shutdown cause", serveErr)
			}
		})
	}
}
