package web_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/web"
)

type streamProbe struct {
	reader                 io.Reader
	read                   func([]byte) (int, error)
	close                  func() error
	reads, closes, maxRead atomic.Int64
}

func (p *streamProbe) Read(buffer []byte) (int, error) {
	p.reads.Add(1)
	for old := p.maxRead.Load(); int64(len(buffer)) > old; old = p.maxRead.Load() {
		if p.maxRead.CompareAndSwap(old, int64(len(buffer))) {
			break
		}
	}
	if p.read != nil {
		return p.read(buffer)
	}
	return p.reader.Read(buffer)
}
func (p *streamProbe) Close() error {
	p.closes.Add(1)
	if p.close != nil {
		return p.close()
	}
	return nil
}

func streamCall(application http.Handler, writer http.ResponseWriter, request *http.Request) (failure any) {
	defer func() { failure = recover() }()
	application.ServeHTTP(writer, request)
	return nil
}

func TestStreamResponseLazinessOwnershipAndBufferedBoundary(t *testing.T) {
	var opened atomic.Int64
	var borrowed *web.Request
	var source *streamProbe
	payload := strings.Repeat("content-", 9000)
	header := http.Header{"Content-Type": {"application/octet-stream"}, "X-Original": {"original"}}
	response, err := web.NewStreamResponse(200, header, func(ctx context.Context) (web.Stream, error) {
		opened.Add(1)
		if borrowed.HTTP() != nil || borrowed.Context().Err() == nil || borrowed.Settings().ProjectName() != "" || ctx.Err() != nil {
			return web.Stream{}, errors.New("borrowed request lifetime was extended")
		}
		source = &streamProbe{reader: strings.NewReader(payload)}
		return web.Stream{Reader: source, Size: int64(len(payload))}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	header.Set("X-Original", "changed")
	if !response.Streaming() || response.Header().Get("X-Original") != "original" {
		t.Fatal("stream description not immutable")
	}
	if data, err := response.Body(); len(data) != 0 || !errors.Is(err, &web.Error{Code: web.CodeBodyNotBuffered}) || opened.Load() != 0 {
		t.Fatal("stream Body performed I/O or pretended empty", err)
	}
	derivedHeader := response.Header()
	derivedHeader.Set("Set-Cookie", "public-cookie=value")
	derived, err := response.WithHeaders(derivedHeader)
	if err != nil {
		t.Fatal(err)
	}
	derivedHeader.Set("Set-Cookie", "changed")
	application := newTestApplication(t, web.Config{MaxResponseBytes: 1, MaxStreamBytes: int64(len(payload)), Routes: []web.Route{{Name: "articles:stream", Method: "GET", Path: "/", Handler: func(request *web.Request) (web.Response, error) { borrowed = request; return derived, nil }}}})
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		if failure := streamCall(application, recorder, httptest.NewRequest("GET", "http://example.test/", nil)); failure != nil {
			t.Fatal(failure)
		}
		if recorder.Code != 200 || recorder.Body.String() != payload || recorder.Header().Get("Content-Length") != strconv.Itoa(len(payload)) || recorder.Header().Get("Set-Cookie") != "public-cookie=value" {
			t.Fatal("stream response changed bytes/headers", recorder.Code)
		}
		if source.closes.Load() != 1 || source.maxRead.Load() > 32<<10 || source.reads.Load() < 2 {
			t.Fatal("reader lifetime/bounded reads", source.closes.Load(), source.maxRead.Load())
		}
	}
	if opened.Load() != 2 || response.Header().Get("Set-Cookie") != "" {
		t.Fatal("response reuse shared cursor or header state")
	}
	if _, err := (web.Response{}).Body(); !errors.Is(err, &web.Error{Code: web.CodeInvalidResponse}) {
		t.Fatal("zero response was buffered", err)
	}
}

func TestStreamResponseConfigurationAndMiddlewareDoNotOpenRejectedBodies(t *testing.T) {
	calls := 0
	open := func(context.Context) (web.Stream, error) {
		calls++
		return web.Stream{Reader: io.NopCloser(strings.NewReader("never")), Size: 5}, nil
	}
	for _, status := range []int{0, 199, 204, 304, 600} {
		if _, err := web.NewStreamResponse(status, nil, open); err == nil {
			t.Fatal("bodyless/invalid status accepted", status)
		}
	}
	if _, err := web.NewStreamResponse(200, nil, nil); err == nil {
		t.Fatal("nil opener accepted")
	}
	for _, name := range []string{"Content-Length", "content-length", "TRANSFER-ENCODING", "Trailer", "Connection", "Keep-Alive", "Proxy-Connection", "Upgrade", "Bad Header"} {
		if _, err := web.NewStreamResponse(200, http.Header{name: {"bad"}}, open); err == nil {
			t.Fatal("invalid stream framing accepted", name)
		}
	}
	response, err := web.NewStreamResponse(200, nil, open)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := response.WithHeaders(http.Header{"content-length": {"5"}}); err == nil {
		t.Fatal("derived headers forged framing")
	}
	if _, err := web.NewApplication(web.Config{Settings: testSettings(t), MaxStreamBytes: -1}); err == nil {
		t.Fatal("negative stream limit accepted")
	}
	for _, mode := range []string{"handler_error", "middleware_error", "replacement", "panic", "repeated_next"} {
		t.Run(mode, func(t *testing.T) {
			handler := func(*web.Request) (web.Response, error) {
				if mode == "handler_error" {
					return response, errors.New("rejected")
				}
				return response, nil
			}
			middleware := func(next web.Handler) web.Handler {
				return func(request *web.Request) (web.Response, error) {
					result, err := next(request)
					switch mode {
					case "middleware_error":
						return result, errors.New("rejected")
					case "replacement":
						return web.NewResponse(403, nil, []byte("denied"))
					case "panic":
						panic("private panic content")
					case "repeated_next":
						_, _ = next(request)
					}
					return result, err
				}
			}
			application := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:stream", Method: "GET", Path: "/", Handler: handler}}, Middleware: []web.Middleware{middleware}})
			recorder := httptest.NewRecorder()
			application.ServeHTTP(recorder, httptest.NewRequest("GET", "http://example.test/", nil))
			expected := 500
			if mode == "replacement" {
				expected = 403
			}
			if recorder.Code != expected || calls != 0 {
				t.Fatal("rejected/replaced response opened content", recorder.Code, calls)
			}
		})
	}
	if calls != 0 {
		t.Fatal("construction performed I/O")
	}
}

type shortStreamWriter struct {
	*httptest.ResponseRecorder
	fail       bool
	panicWrite bool
}

func (w shortStreamWriter) Write(p []byte) (int, error) {
	if w.panicWrite {
		panic("private writer panic")
	}
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p[:len(p)/2])
}

func TestStreamFailuresCloseReadersAndCannotFinishSuccessfulFrames(t *testing.T) {
	const chunk = 32 << 10
	for _, mode := range []string{"open_error", "nil_reader", "typed_nil_reader", "negative_size", "declared_limit", "short_initial", "long_initial", "short_late", "long_late", "unknown_limit", "read_error", "read_panic", "no_progress", "invalid_read_count", "joined_eof_error", "opener_panic", "close_error", "close_panic", "writer_error", "writer_short", "writer_panic"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			payload := strings.Repeat("s", chunk*3)
			probe := &streamProbe{reader: strings.NewReader(payload)}
			size := int64(len(payload))
			limit := int64(len(payload) + 1)
			var openErr error
			var returned io.ReadCloser = probe
			switch mode {
			case "open_error":
				openErr = errors.New("private open details")
			case "nil_reader":
				returned = nil
			case "typed_nil_reader":
				returned = (*streamProbe)(nil)
			case "negative_size":
				size = -2
			case "declared_limit":
				limit = 1
			case "short_initial":
				probe.read = func(p []byte) (int, error) { return copy(p, "short"), io.EOF }
				size = 100
			case "long_initial":
				probe.reader = strings.NewReader("longer")
				size = 1
			case "short_late":
				size = int64(len(payload) + 1)
			case "long_late":
				size = chunk * 2
			case "unknown_limit":
				size = -1
				limit = chunk * 2
			case "read_error":
				probe.read = func([]byte) (int, error) { return 0, errors.New("private read details") }
			case "read_panic":
				probe.read = func([]byte) (int, error) { panic("private reader panic") }
			case "no_progress":
				probe.read = func([]byte) (int, error) { return 0, nil }
			case "invalid_read_count":
				probe.read = func(p []byte) (int, error) { return len(p) + 1, nil }
			case "joined_eof_error":
				probe.read = func([]byte) (int, error) { return 0, errors.Join(io.EOF, errors.New("private failure")) }
				size = 0
			case "close_error":
				probe.close = func() error { return errors.New("private close details") }
			case "close_panic":
				probe.close = func() error { panic("private close panic") }
			}
			response, err := web.NewStreamResponse(200, http.Header{"Content-Type": {"application/octet-stream"}}, func(context.Context) (web.Stream, error) {
				if mode == "opener_panic" {
					panic("private opener panic")
				}
				return web.Stream{Reader: returned, Size: size}, openErr
			})
			if err != nil {
				t.Fatal(err)
			}
			application := newTestApplication(t, web.Config{MaxStreamBytes: limit, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Routes: []web.Route{{Name: "articles:stream", Method: "GET", Path: "/", Handler: func(*web.Request) (web.Response, error) { return response, nil }}}})
			recorder := httptest.NewRecorder()
			var writer http.ResponseWriter = recorder
			switch mode {
			case "writer_error":
				writer = shortStreamWriter{ResponseRecorder: recorder, fail: true}
			case "writer_short":
				writer = shortStreamWriter{ResponseRecorder: recorder}
			case "writer_panic":
				writer = shortStreamWriter{ResponseRecorder: recorder, panicWrite: true}
			}
			failure := streamCall(application, writer, httptest.NewRequest("GET", "http://example.test/", nil))
			switch mode {
			case "short_late", "long_late", "unknown_limit", "writer_error", "writer_short", "writer_panic":
				if failure != http.ErrAbortHandler || recorder.Code != 200 || strings.Contains(recorder.Body.String(), "Internal Server Error") {
					t.Fatal("partial stream was completed or replaced", failure, recorder.Code)
				}
				if size >= 0 && int64(recorder.Body.Len()) >= size {
					t.Fatal("invalid source delivered an apparently complete Content-Length body")
				}
			case "close_error", "close_panic":
				if failure != nil || recorder.Code != 200 || recorder.Body.String() != payload || !strings.Contains(logs.String(), "stream cleanup failed") {
					t.Fatal("cleanup overwrote delivered result or was hidden", failure, recorder.Code)
				}
			default:
				if failure != nil || recorder.Code != 500 || recorder.Body.String() != "Internal Server Error\n" {
					t.Fatal("pre-header error not sanitized", failure, recorder.Code, recorder.Body.Len())
				}
			}
			wantClose := int64(1)
			if mode == "nil_reader" || mode == "typed_nil_reader" || mode == "opener_panic" {
				wantClose = 0
			}
			if probe.closes.Load() != wantClose {
				t.Fatal("reader not closed exactly once", probe.closes.Load(), wantClose)
			}
			if strings.Contains(logs.String(), "private") || strings.Contains(recorder.Body.String(), "private") {
				t.Fatal("stream diagnostics leaked private content")
			}
			if (mode == "declared_limit" || mode == "negative_size" || mode == "open_error") && probe.reads.Load() != 0 {
				t.Fatal("invalid source was consumed")
			}
		})
	}
}

func TestStreamHeadUnknownLengthAndIndependentConcurrentReaders(t *testing.T) {
	var opens, closes, reads atomic.Int64
	payload := "hello"
	response, err := web.NewStreamResponse(200, nil, func(context.Context) (web.Stream, error) {
		opens.Add(1)
		reader := strings.NewReader(payload)
		probe := &streamProbe{read: func(p []byte) (int, error) { reads.Add(1); return reader.Read(p) }, close: func() error { closes.Add(1); return nil }}
		return web.Stream{Reader: probe, Size: -1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := func(*web.Request) (web.Response, error) { return response, nil }
	application := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:get", Method: "GET", Path: "/", Handler: handler}, {Name: "articles:head", Method: "HEAD", Path: "/", Handler: handler}}})
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, httptest.NewRequest("HEAD", "http://example.test/", nil))
	if recorder.Code != 200 || recorder.Body.Len() != 0 || recorder.Header().Get("Content-Length") != "" || opens.Load() != 1 || closes.Load() != 1 || reads.Load() != 0 {
		t.Fatal("HEAD read content or invented length")
	}
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Go(func() {
			recorder := httptest.NewRecorder()
			application.ServeHTTP(recorder, httptest.NewRequest("GET", "http://example.test/", nil))
			if recorder.Code != 200 || recorder.Body.String() != payload || recorder.Header().Get("Content-Length") != "" {
				t.Error("concurrent response shared cursor")
			}
		})
	}
	group.Wait()
	if opens.Load() != 13 || closes.Load() != 13 {
		t.Fatal("concurrent response leaked resources", opens.Load(), closes.Load())
	}
}

func TestStreamNetworkFailureAndCancellationCloseTheTransport(t *testing.T) {
	t.Run("declared_length_mismatch", func(t *testing.T) {
		probe := &streamProbe{reader: strings.NewReader(strings.Repeat("x", 3*(32<<10)))}
		response, err := web.NewStreamResponse(200, nil, func(context.Context) (web.Stream, error) { return web.Stream{Reader: probe, Size: 2 * (32 << 10)}, nil })
		if err != nil {
			t.Fatal(err)
		}
		app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:file", Method: "GET", Path: "/", Handler: func(*web.Request) (web.Response, error) { return response, nil }}}})
		server := httptest.NewServer(app)
		defer server.Close()
		client := server.Client()
		client.Timeout = 5 * time.Second
		result, err := client.Get(server.URL + "/")
		if err != nil {
			t.Fatal("expected a sent prefix", err)
		}
		data, readErr := io.ReadAll(result.Body)
		_ = result.Body.Close()
		if result.StatusCode != 200 || len(data) == 0 || len(data) >= 2*(32<<10) || readErr == nil || probe.closes.Load() != 1 {
			t.Fatal("network accepted a false complete frame", result.StatusCode, len(data), readErr, probe.closes.Load())
		}
	})
	t.Run("canceled_reader", func(t *testing.T) {
		closed := make(chan struct{}, 1)
		var closes atomic.Int64
		response, err := web.NewStreamResponse(200, nil, func(ctx context.Context) (web.Stream, error) {
			first := true
			reader := &streamProbe{read: func(p []byte) (int, error) {
				if first {
					first = false
					return copy(p, strings.Repeat("x", 32<<10)), nil
				}
				<-ctx.Done()
				return 0, ctx.Err()
			}, close: func() error { closes.Add(1); closed <- struct{}{}; return nil }}
			return web.Stream{Reader: reader, Size: -1}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:file", Method: "GET", Path: "/", Handler: func(*web.Request) (web.Response, error) { return response, nil }}}})
		server := httptest.NewServer(app)
		defer server.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		client := server.Client()
		client.Timeout = 5 * time.Second
		result, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var prefix [1]byte
		if _, err := result.Body.Read(prefix[:]); err != nil {
			t.Fatal(err)
		}
		cancel()
		_ = result.Body.Close()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatal("canceled stream reader was not closed")
		}
		if closes.Load() != 1 {
			t.Fatal("canceled reader closed more than once")
		}
	})
}

func TestStreamDoesNotExtendMultipartUploadLifetime(t *testing.T) {
	policy := uploads.DefaultConfig()
	policy.MemoryBytes = 0
	policy.TempDir = t.TempDir()
	var incoming uploads.File
	app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:stream", Method: "POST", Path: "/", Handler: func(request *web.Request) (web.Response, error) {
		parsed, err := request.Multipart(policy)
		if err != nil {
			return web.Response{}, err
		}
		incoming = parsed.Files()["file"][0]
		return web.NewStreamResponse(200, nil, func(ctx context.Context) (web.Stream, error) {
			reader, err := incoming.Open(ctx)
			return web.Stream{Reader: reader, Size: incoming.Size()}, err
		})
	}}}})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "request.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "request bytes"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://example.test/", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != 500 || recorder.Body.String() != "Internal Server Error\n" {
		t.Fatal("upload used after borrowed request", recorder.Code)
	}
	if _, err := incoming.Open(t.Context()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("upload capability survived", err)
	}
	if entries, err := os.ReadDir(policy.TempDir); err != nil || len(entries) != 0 {
		t.Fatal("upload staging leaked", err)
	}
}
