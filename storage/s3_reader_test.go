package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type s3BodyProbe struct {
	read   func([]byte) (int, error)
	close  func() error
	closes atomic.Int64
}

func (body *s3BodyProbe) Read(p []byte) (int, error) { return body.read(p) }
func (body *s3BodyProbe) Close() error {
	body.closes.Add(1)
	if body.close != nil {
		return body.close()
	}
	return nil
}

func s3WireResponse(request *http.Request, content []byte, version string, checksum bool) (*http.Response, *s3BodyProbe) {
	reader := bytes.NewReader(bytes.Clone(content))
	body := &s3BodyProbe{read: reader.Read}
	header := http.Header{}
	header.Set("Content-Length", strconv.Itoa(len(content)))
	header.Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
	header.Set("Etag", `"same-opaque-etag"`)
	if version != "" {
		header.Set("X-Amz-Version-Id", version)
	}
	if checksum {
		header.Set("X-Amz-Checksum-Sha256", s3TestChecksum(content))
		header.Set("X-Amz-Checksum-Type", "FULL_OBJECT")
	}
	return &http.Response{StatusCode: 200, Header: header, ContentLength: int64(len(content)), Body: body, Request: request}, body
}

func TestS3OpenBindsMetadataToItsSingleGETAndOwnsReaders(t *testing.T) {
	var mu sync.Mutex
	latest := []byte("original content")
	var requests []string
	var bodies []*s3BodyProbe
	config := s3TestConfig()
	config.Transport = s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, request.Method)
		response, body := s3WireResponse(request, latest, "", true)
		bodies = append(bodies, body)
		return response, nil
	})
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	first, err := backend.Open(t.Context(), "private.bin")
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := first.(Reader)
	if !ok || metadata.Info().Size() != int64(len(latest)) || metadata.Info().ContentMetadata().Version == "" {
		t.Fatal("GET metadata was discarded")
	}
	if _, ok := first.(SeekableReader); ok {
		t.Fatal("unversioned live name became a seekable snapshot")
	}
	mu.Lock()
	latest = []byte("replacement")
	mu.Unlock()
	second, err := backend.Open(t.Context(), "private.bin")
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	for index, reader := range []io.ReadCloser{first, second} {
		want := []string{"original content", "replacement"}[index]
		data, err := io.ReadAll(reader)
		if err != nil || string(data) != want {
			t.Fatal("same-name reuse or backend close changed an open reader", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, &Error{Code: "closed"}) {
			t.Fatal("closed remote reader remained usable")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0] != "GET" || requests[1] != "GET" || bodies[0].closes.Load() != 1 || bodies[1].closes.Load() != 1 {
		t.Fatal("Open used a separate lookup, shared cursor or wrong body lifetime")
	}
}

func TestS3VersionedSeekUsesOriginalObjectAcrossReplacementAndClose(t *testing.T) {
	original := []byte("0123456789")
	var mu sync.Mutex
	var versions, spans []string
	var bodies []*s3BodyProbe
	config := s3TestConfig()
	config.Transport = s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		version, span := request.URL.Query().Get("versionId"), request.Header.Get("Range")
		versions, spans = append(versions, version), append(spans, span)
		if len(versions) > 1 && version != "original-version" {
			t.Error("seek reopened the live name")
		}
		start := 0
		if span != "" {
			if _, err := fmt.Sscanf(span, "bytes=%d-", &start); err != nil {
				t.Error(err)
			}
		}
		response, body := s3WireResponse(request, original[start:], "original-version", span == "")
		if span != "" {
			response.StatusCode = 206
			response.Header.Set("Content-Range", fmt.Sprintf("bytes %d-9/10", start))
		}
		bodies = append(bodies, body)
		return response, nil
	})
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := backend.Open(t.Context(), "same-name.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	reader, ok := opened.(SeekableReader)
	if !ok {
		t.Fatal("versioned reader lost seeking")
	}
	info := reader.Info()
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Open(t.Context(), "same-name.bin"); !errors.Is(err, &Error{Code: "closed"}) {
		t.Fatal("backend close did not stop new opens")
	}
	for _, at := range []int64{2, 7, 0} {
		if position, err := reader.Seek(at, io.SeekStart); err != nil || position != at {
			t.Fatal("version seek failed", err)
		}
		data, err := io.ReadAll(reader)
		if err != nil || !bytes.Equal(data, original[at:]) || reader.Info() != info {
			t.Fatal("version-bound partial read changed content or metadata", err)
		}
	}
	if offset, err := reader.Seek(math.MaxInt64, io.SeekStart); err != nil || offset != math.MaxInt64 {
		t.Fatal("bounded beyond-end seek failed", err)
	}
	if _, err := reader.Seek(1, io.SeekCurrent); !errors.Is(err, &Error{Code: "invalid_seek"}) {
		t.Fatal("seek offset overflow accepted")
	}
	if n, err := reader.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatal("beyond-end seek started a request")
	}
	if _, err := reader.Seek(math.MinInt64, io.SeekCurrent); !errors.Is(err, &Error{Code: "invalid_seek"}) {
		t.Fatal("negative seek overflow accepted")
	}
	if _, err := reader.Seek(0, 9); !errors.Is(err, &Error{Code: "invalid_seek"}) {
		t.Fatal("invalid whence accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(versions) != 4 || versions[0] != "" || spans[1] != "bytes=2-" || spans[2] != "bytes=7-" || spans[3] != "" {
		t.Fatal("range requests were missing or replayed a mutable key", len(versions))
	}
	for _, body := range bodies {
		if body.closes.Load() != 1 {
			t.Fatal("seek leaked a superseded response body")
		}
	}
}

func TestS3ReaderRejectsCorruptionBadMetadataAndRangeRebinding(t *testing.T) {
	for _, mode := range []string{"wrong_checksum", "truncated", "extra", "missing_length", "negative_length", "max_length", "bad_checksum_header", "range_version", "range_span", "range_length", "read_error", "close_error", "no_progress"} {
		t.Run(mode, func(t *testing.T) {
			cause := errors.New("private stream failure")
			var calls atomic.Int64
			var mu sync.Mutex
			var bodies []*s3BodyProbe
			config := s3TestConfig()
			config.Transport = s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
				call := calls.Add(1)
				response, body := s3WireResponse(request, []byte("abcd"), "version-one", true)
				if strings.HasPrefix(mode, "range_") && call > 1 {
					response, body = s3WireResponse(request, []byte("bcd"), "version-one", false)
					response.StatusCode = 206
					response.Header.Set("Content-Range", "bytes 1-3/4")
				}
				switch mode {
				case "wrong_checksum":
					response.Header.Set("X-Amz-Checksum-Sha256", s3TestChecksum([]byte("different")))
				case "truncated":
					response.Header.Set("Content-Length", "5")
				case "extra":
					response.Header.Set("Content-Length", "3")
				case "missing_length":
					response.Header.Del("Content-Length")
				case "negative_length":
					response.Header.Set("Content-Length", "-1")
				case "max_length":
					response.Header.Set("Content-Length", strconv.FormatInt(math.MaxInt64, 10))
				case "bad_checksum_header":
					response.Header.Set("X-Amz-Checksum-Sha256", "not a digest")
				case "range_version":
					if call > 1 {
						response.Header.Set("X-Amz-Version-Id", "replacement-version")
					}
				case "range_span":
					if call > 1 {
						response.Header.Set("Content-Range", "bytes 0-2/4")
					}
				case "range_length":
					if call > 1 {
						response.Header.Set("Content-Length", "4")
					}
				case "read_error":
					body.read = func([]byte) (int, error) { return 0, cause }
				case "close_error":
					body.close = func() error { return cause }
				case "no_progress":
					body.read = func([]byte) (int, error) { return 0, nil }
				}
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
				return response, nil
			})
			backend, err := NewS3(config)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			reader, openErr := backend.Open(t.Context(), "private.bin")
			metadataFailure := mode == "missing_length" || mode == "negative_length" || mode == "bad_checksum_header"
			if metadataFailure {
				if reader != nil || openErr == nil {
					t.Fatal("invalid remote metadata became an open capability")
				}
			} else {
				if openErr != nil {
					t.Fatal("test did not reach reader failure", openErr)
				}
				if strings.HasPrefix(mode, "range_") {
					if _, err := reader.(SeekableReader).Seek(1, io.SeekStart); err != nil {
						t.Fatal(err)
					}
				}
				_, readErr := io.ReadAll(reader)
				closeErr := reader.Close()
				if mode == "close_error" {
					if readErr != nil || !errors.Is(closeErr, cause) {
						t.Fatal("remote Close failure was lost", readErr, closeErr)
					}
				} else if readErr == nil {
					t.Fatal("corrupt remote content or range binding became success", mode)
				}
				if mode == "read_error" && !errors.Is(readErr, cause) {
					t.Fatal("stream failure cause was discarded")
				}
				if mode == "no_progress" && !errors.Is(readErr, io.ErrNoProgress) {
					t.Fatal("remote reader did not bound empty reads")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for _, body := range bodies {
				if body.closes.Load() != 1 {
					t.Fatal("failed GET leaked or double-closed a response body", body.closes.Load())
				}
			}
		})
	}
}

func TestS3ReadCloseCancelsAnInFlightNetworkRead(t *testing.T) {
	entered := make(chan struct{})
	var body *s3BodyProbe
	config := s3TestConfig()
	config.Transport = s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		response, probe := s3WireResponse(request, []byte("a"), "", false)
		body = probe
		body.read = func([]byte) (int, error) {
			close(entered)
			<-request.Context().Done()
			return 0, request.Context().Err()
		}
		return response, nil
	})
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	reader, err := backend.Open(t.Context(), "blocked.bin")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := reader.Read(make([]byte, 1)); finished <- err }()
	<-entered
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("Close did not cancel its own response", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock response Read")
	}
	if body.closes.Load() != 1 {
		t.Fatal("cancellation lost response ownership")
	}
}

func TestS3SignedDownloadIsExplicitEscapedAndBounded(t *testing.T) {
	config := s3TestConfig()
	config.Prefix, config.ExpectedBucketOwner = "tenant", "123456789012"
	var requests atomic.Int64
	config.Transport = s3RoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("signing must not perform object lookup")
	})
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	private, err := NewRegistry(Registration{Alias: "private", Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := private.URL(t.Context(), "private", "image.webp"); !errors.Is(err, &Error{Code: "url_unavailable"}) {
		t.Fatal("S3 silently enabled public URLs")
	}
	resolver, err := backend.SignedURL(90 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(Registration{Alias: "downloads", Backend: backend, URL: resolver})
	if err != nil {
		t.Fatal(err)
	}
	name := "한글 %2Fname.webp"
	signed, err := registry.URL(t.Context(), "downloads", name)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/private-bucket/tenant/"+name || query.Get("X-Amz-Expires") != "90" || query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || query.Get("X-Amz-SignedHeaders") != "host" || query.Get("X-Amz-Signature") == "" || query.Get("response-content-type") != "application/octet-stream" || query.Get("response-content-disposition") != "attachment" || query.Get("response-cache-control") != "private, no-store" || query.Get("x-amz-expected-bucket-owner") != "123456789012" || strings.Contains(signed, "synthetic-private-secret") || requests.Load() != 0 {
		t.Fatal("signed download lost name, owner, expiry or safe response binding")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolver.URL(ctx, name); !errors.Is(err, context.Canceled) {
		t.Fatal("signed URL ignored context")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.URL(t.Context(), name); !errors.Is(err, &Error{Code: "closed"}) {
		t.Fatal("signed URL outlived its backend's new-operation lifetime")
	}
}

func TestS3MissingObjectsAndPermissionFailuresRemainDistinct(t *testing.T) {
	for _, mode := range []string{"missing", "denied", "missing_bucket"} {
		t.Run(mode, func(t *testing.T) {
			backend := s3TestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "denied" {
					s3TestError(w, 403, "AccessDenied")
					return
				}
				if mode == "missing_bucket" {
					s3TestError(w, 404, "NoSuchBucket")
					return
				}
				if r.Method == http.MethodDelete {
					w.WriteHeader(204)
					return
				}
				s3TestError(w, 404, "NoSuchKey")
			}, nil)
			if reader, err := backend.Open(t.Context(), "missing.bin"); reader != nil || err == nil || errors.Is(err, fs.ErrNotExist) != (mode == "missing") {
				t.Fatal("object absence and permission/bucket errors were conflated", err)
			}
			if info, err := backend.Stat(t.Context(), "missing.bin"); info.Valid() || err == nil || mode == "denied" && errors.Is(err, fs.ErrNotExist) {
				t.Fatal("HEAD metadata invented an existing object", err)
			}
			err := backend.Delete(t.Context(), "missing.bin")
			if (err == nil) != (mode == "missing") {
				t.Fatal("Delete hid a bucket/permission error or rejected an absent key", err)
			}
		})
	}
}

func TestS3RemoteVersionMetadataBindsServiceAndNeverPromotesETag(t *testing.T) {
	versions := map[string]string{}
	for _, test := range []struct{ name, endpoint, version string }{
		{"first", "https://first.example.test", "version-one"},
		{"second", "https://second.example.test", "version-one"},
		{"mutable", "https://first.example.test", ""},
		{"null", "https://first.example.test", "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := s3TestConfig()
			config.Endpoint = test.endpoint
			config.Transport = s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
				response, _ := s3WireResponse(request, []byte(test.name), test.version, false)
				return response, nil
			})
			backend, err := NewS3(config)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			reader, err := backend.Open(t.Context(), "same.bin")
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			versions[test.name] = reader.(Reader).Info().ContentMetadata().Version
		})
	}
	if versions["first"] == "" || versions["second"] == "" || versions["first"] == versions["second"] || versions["mutable"] != "" || versions["null"] != "" {
		t.Fatal("remote validators conflate service identity or invent a strong mutable version")
	}
}

func TestS3SeekCloseFailurePreservesCauseAndClosesBodyOnce(t *testing.T) {
	cause := errors.New("synthetic seek close failure")
	var body *s3BodyProbe
	var calls atomic.Int64
	config := s3TestConfig()
	config.Transport = s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		response, probe := s3WireResponse(request, []byte("content"), "version-one", false)
		probe.close = func() error { return cause }
		body = probe
		return response, nil
	})
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	opened, err := backend.Open(t.Context(), "same.bin")
	if err != nil {
		t.Fatal(err)
	}
	reader := opened.(SeekableReader)
	if _, err := reader.Seek(1, io.SeekStart); !errors.Is(err, cause) {
		t.Fatal("seek hid old body close failure", err)
	}
	if _, err := reader.Read(make([]byte, 2)); !errors.Is(err, cause) {
		t.Fatal("failed seek resumed another request", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if body.closes.Load() != 1 || calls.Load() != 1 {
		t.Fatal("seek failure double-closed a body or started a GET")
	}
}

func TestS3ReadersReleaseConnectionsOpenedAfterBackendClose(t *testing.T) {
	var mu sync.Mutex
	connections := map[net.Conn]http.ConnState{}
	changed := make(chan struct{}, 16)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, status := "original content", http.StatusOK
		w.Header().Set("X-Amz-Version-Id", "original-version")
		if r.Header.Get("Range") != "" {
			if r.Header.Get("Range") != "bytes=1-" || r.URL.Query().Get("versionId") != "original-version" {
				t.Error("late reader GET lost its version")
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 1-%d/%d", len(content)-1, len(content)))
			content, status = content[1:], http.StatusPartialContent
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, content)
	}))
	server.Config.ConnState = func(connection net.Conn, state http.ConnState) {
		mu.Lock()
		if state == http.StateClosed {
			delete(connections, connection)
		} else {
			connections[connection] = state
		}
		mu.Unlock()
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	server.Start()
	defer server.Close()
	config := s3TestConfig()
	config.Endpoint, config.AllowLoopbackHTTP = server.URL, true
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	reader, err := backend.Open(t.Context(), "same.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.(SeekableReader).Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if content, err := io.ReadAll(reader); err != nil || string(content) != "riginal content" {
		t.Fatal("late version GET", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		mu.Lock()
		remaining := len(connections)
		mu.Unlock()
		if remaining == 0 {
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatal("closed remote reader retained owned HTTP connections after backend Close")
		}
	}
}
