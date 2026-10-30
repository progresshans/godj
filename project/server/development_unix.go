//go:build darwin || linux

package server

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"

	"github.com/progresshans/godj/internal/runservercontrol"
)

// Serve runs the explicit `serve --listen ADDRESS` development entry point.
// ADDRESS must be a literal loopback address. Config owns application startup,
// readiness and cleanup as in Run; Listen must be nil. A global godj supervisor
// may supply a listener and a control socket through inherited descriptors.
// That mode validates both resources before Start, waits for publication before
// accepting requests, and drains when the supervisor retires or disconnects.
// No environment variable enables inherited resources, and stdout is never a
// readiness protocol. Ready remains an observation of application startup;
// the supervisor separately confirms publication of a prepared generation.
func Serve(ctx context.Context, args []string, config Config) error {
	args = append([]string(nil), args...)
	if len(args) == 1 && args[0] == "serve" {
		args = []string{"serve", "--listen", "127.0.0.1:8000"}
	}
	if ctx == nil || config.Listen != nil || len(args) < 3 || len(args) > 4 ||
		args[0] != "serve" || args[1] != "--listen" ||
		(len(args) == 4 && args[3] != runservercontrol.Argument) {
		return failure(CodeInvalidConfig, nil)
	}
	address, err := netip.ParseAddrPort(args[2])
	if err != nil || !address.Addr().IsLoopback() || address.String() != args[2] {
		return failure(CodeInvalidConfig, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) == 3 {
		config.Listen = func(ctx context.Context) (net.Listener, error) {
			var listen net.ListenConfig
			return listen.Listen(ctx, "tcp", address.String())
		}
		return Run(ctx, config)
	}
	listenerFile := os.NewFile(runservercontrol.ListenerFD, "godj-listener")
	controlFile := os.NewFile(runservercontrol.ControlFD, "godj-control")
	if listenerFile == nil || controlFile == nil {
		if listenerFile != nil {
			_ = listenerFile.Close()
		}
		if controlFile != nil {
			_ = controlFile.Close()
		}
		return failure(CodeControlFailed, nil)
	}
	listener, listenErr := net.FileListener(listenerFile)
	control, controlErr := net.FileConn(controlFile)
	closeErr := errors.Join(listenerFile.Close(), controlFile.Close())
	if listenErr != nil || controlErr != nil || closeErr != nil {
		if listener != nil {
			_ = listener.Close()
		}
		if control != nil {
			_ = control.Close()
		}
		return failure(CodeControlFailed, errors.Join(listenErr, controlErr, closeErr))
	}
	defer control.Close()
	defer listener.Close()
	tcp, tcpOK := listener.(*net.TCPListener)
	_, socketOK := control.(*net.UnixConn)
	if !tcpOK || !socketOK || tcp.Addr().String() != address.String() || address.Port() == 0 {
		return failure(CodeControlFailed, nil)
	}
	return serveControlled(ctx, config, listener, control)
}

// A prepared listener has reached net/http's first Accept, after Start, policy
// validation and Ready all succeeded. Publication opens its admission gate.
// Close is acknowledged only after the underlying listener has been closed;
// the parent can then activate the successor while existing requests drain.
type developmentListener struct {
	net.Listener
	prepared    chan struct{}
	active      chan struct{}
	closed      chan struct{}
	prepareOnce sync.Once
	closeOnce   sync.Once
	closeErr    error
}

func (listener *developmentListener) Accept() (net.Conn, error) {
	listener.prepareOnce.Do(func() { close(listener.prepared) })
	select {
	case <-listener.closed:
		return nil, net.ErrClosed
	case <-listener.active:
	}
	return listener.Listener.Accept()
}

func (listener *developmentListener) Close() error {
	listener.closeOnce.Do(func() {
		listener.closeErr = listener.Listener.Close()
		close(listener.closed)
	})
	return listener.closeErr
}

func serveControlled(parent context.Context, config Config, raw net.Listener, control net.Conn) (result error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listener := &developmentListener{Listener: raw, prepared: make(chan struct{}), active: make(chan struct{}), closed: make(chan struct{})}
	defer listener.Close()
	config.Listen = func(context.Context) (net.Listener, error) { return listener, nil }
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, config); cancel() }()
	// Closing the private socket interrupts a blocked protocol read when the
	// operator or parent context cancels. Join this callback before returning.
	closed := make(chan struct{})
	stopClosing := context.AfterFunc(ctx, func() { _ = control.Close(); close(closed) })
	var detachOnce sync.Once
	detachClosing := func() {
		detachOnce.Do(func() {
			if !stopClosing() {
				<-closed
			}
		})
	}
	defer func() {
		cancel()
		detachClosing()
	}()
	var runResult error
	joined := false
	defer func() {
		cancel()
		if !joined {
			runResult = <-finished
		}
		result = errors.Join(result, runResult)
	}()
	fail := func(err error) error {
		if parent.Err() != nil {
			return nil
		}
		return failure(CodeControlFailed, err)
	}
	select {
	case <-listener.prepared:
	case runResult = <-finished:
		joined = true
		return nil
	case <-ctx.Done():
		return nil
	}
	if err := runservercontrol.Send(control, runservercontrol.Prepared); err != nil {
		return fail(err)
	}
	if err := runservercontrol.Expect(control, runservercontrol.Activate); err != nil {
		return fail(err)
	}
	if err := runservercontrol.Send(control, runservercontrol.Active); err != nil {
		return fail(err)
	}
	close(listener.active)
	if err := runservercontrol.Expect(control, runservercontrol.Retire); err != nil {
		return fail(err)
	}
	// Run's cancellation owns net/http.Shutdown. Its listener Close precedes
	// the potentially long request/stream drain and app Stop callbacks.
	// Keep the control connection open until that close is acknowledged.
	detachClosing()
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	cancel()
	<-listener.closed
	if err := runservercontrol.Send(control, runservercontrol.Retired); err != nil {
		return fail(err)
	}
	return nil
}
