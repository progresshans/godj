package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
)

type s3ReadFunc func([]byte) (int, error)

func (read s3ReadFunc) Read(p []byte) (int, error) { return read(p) }

type s3RoundTripFunc func(*http.Request) (*http.Response, error)

func (call s3RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return call(r) }

func s3TestConfig() S3Config {
	return S3Config{Bucket: "private-bucket", Region: "us-east-1", Endpoint: "https://s3.example.test", UsePathStyle: true,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "synthetic-access", SecretAccessKey: "synthetic-private-secret"}, nil
		})}
}

func s3TestServer(t *testing.T, handler http.HandlerFunc, configure func(*S3Config)) *S3 {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config := s3TestConfig()
	config.Endpoint, config.AllowLoopbackHTTP = server.URL, true
	if configure != nil {
		configure(&config)
	}
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	return backend
}

func s3TestChecksum(data []byte) string {
	digest := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(digest[:])
}

func s3TestPutResponse(w http.ResponseWriter, data []byte) {
	w.Header().Set("X-Amz-Checksum-Sha256", s3TestChecksum(data))
	w.Header().Set("X-Amz-Checksum-Type", "FULL_OBJECT")
	w.Header().Set("Etag", `"opaque-service-etag"`)
	w.WriteHeader(http.StatusOK)
}

func s3TestError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>synthetic private response</Message></Error>", code)
}

func TestS3ConfigurationIsExplicitBoundedAndPrivate(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*S3Config)
	}{
		{"credentials", func(c *S3Config) { c.Credentials = nil }},
		{"anonymous_credentials", func(c *S3Config) { c.Credentials = aws.AnonymousCredentials{} }},
		{"typed_nil_credentials", func(c *S3Config) { var provider aws.CredentialsProviderFunc; c.Credentials = provider }},
		{"bucket_arn", func(c *S3Config) { c.Bucket = "arn:aws:s3:region:account:accesspoint/private" }},
		{"bucket_ip", func(c *S3Config) { c.Bucket = "127.0.0.1" }},
		{"directory_bucket", func(c *S3Config) { c.Bucket = "private--zone--x-s3" }},
		{"bucket_path", func(c *S3Config) { c.Bucket = "private/other" }},
		{"region", func(c *S3Config) { c.Region = "us-east-1.example.test" }},
		{"prefix", func(c *S3Config) { c.Prefix = "private/../other" }},
		{"prefix_slash", func(c *S3Config) { c.Prefix = "private/" }},
		{"owner", func(c *S3Config) { c.ExpectedBucketOwner = "owner" }},
		{"http", func(c *S3Config) { c.Endpoint = "http://127.0.0.1:9000" }},
		{"remote_http", func(c *S3Config) { c.Endpoint = "http://example.test"; c.AllowLoopbackHTTP = true }},
		{"endpoint_path", func(c *S3Config) { c.Endpoint += "/private" }},
		{"endpoint_query", func(c *S3Config) { c.Endpoint += "?token=private" }},
		{"endpoint_userinfo", func(c *S3Config) { c.Endpoint = "https://private:secret@example.test" }},
		{"endpoint_fragment", func(c *S3Config) { c.Endpoint += "#private" }},
		{"timeout", func(c *S3Config) { c.RequestTimeout = -time.Second }},
		{"buffer", func(c *S3Config) { c.MaxBufferedBytes = -1 }},
		{"concurrency", func(c *S3Config) { c.MaxConcurrentSaves = -1 }},
		{"single_put_limit", func(c *S3Config) { c.Limits.MaxFileBytes = 5<<30 + 1 }},
		{"typed_nil_transport", func(c *S3Config) { var transport *http.Transport; c.Transport = transport }},
		{"typed_nil_entropy", func(c *S3Config) { var random *bytes.Reader; c.Random = random }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := s3TestConfig()
			calls := 0
			config.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				calls++
				return aws.Credentials{}, errors.New("private credential failure")
			})
			test.change(&config)
			backend, err := NewS3(config)
			if err == nil || backend != nil || calls != 0 {
				t.Fatal("invalid S3 configuration reached external I/O")
			}
		})
	}
	config := s3TestConfig()
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	resolver, err := backend.SignedURL(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	message := fmt.Sprintf("%+v %#v %v %+v", config, backend, resolver, &Error{Code: "publish_failed", Cause: errors.New("synthetic-private-secret")})
	for _, secret := range []string{"synthetic-private-secret", "private-bucket", "example.test"} {
		if strings.Contains(message, secret) {
			t.Fatal("S3 capability or error formatting exposed configuration")
		}
	}
	for _, expires := range []time.Duration{0, time.Millisecond, -time.Second, 7*24*time.Hour + time.Second} {
		if _, err := backend.SignedURL(expires); !errors.Is(err, &Error{Code: "invalid_url_expiry"}) {
			t.Fatal("invalid signed URL lifetime accepted")
		}
	}
}

func TestS3SaveUsesConditionalChecksumAndReadsSourceOnce(t *testing.T) {
	content := []byte("complete private payload")
	var completed atomic.Bool
	var mu sync.Mutex
	var names []string
	backend := s3TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || !completed.Load() || !bytes.Equal(data, content) || r.Method != http.MethodPut || r.Header.Get("If-None-Match") != "*" || r.Header.Get("X-Amz-Checksum-Sha256") != s3TestChecksum(content) || r.ContentLength != int64(len(content)) || r.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") || r.Header.Get("X-Amz-Expected-Bucket-Owner") != "123456789012" {
			t.Error("S3 PUT did not carry complete content, signature, owner or conditional checksum")
		}
		mu.Lock()
		names = append(names, r.URL.Path)
		call := len(names)
		mu.Unlock()
		if call == 1 {
			s3TestError(w, 412, "PreconditionFailed")
			return
		}
		s3TestPutResponse(w, data)
	}, func(config *S3Config) {
		config.Prefix, config.ExpectedBucketOwner = "tenant", "123456789012"
		config.Random = s3ReadFunc(func(p []byte) (int, error) { clear(p); return len(p), nil })
	})
	input := bytes.NewReader(content)
	reads := 0
	source := s3ReadFunc(func(p []byte) (int, error) {
		reads++
		n, err := input.Read(p)
		if err == io.EOF {
			completed.Store(true)
		}
		return n, err
	})
	info, err := backend.Save(t.Context(), "forms/archive.tar.gz", source, SaveOptions{})
	if err != nil || !info.Valid() || info.Name() != "forms/archive_AAAAAAA.tar.gz" || info.Size() != int64(len(content)) || info.ContentMetadata().Version == "" || reads != 2 {
		t.Fatal("conditional publication lost actual name, bytes or one-pass source", err, reads)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(names) != 2 || names[0] != "/private-bucket/tenant/forms/archive.tar.gz" || names[1] != "/private-bucket/tenant/"+info.Name() {
		t.Fatal("collision made an extra request or escaped the fixed prefix")
	}
}

func TestS3SavePreservesRejectedPublishedAndUncertainOutcomes(t *testing.T) {
	for _, mode := range []string{"permission", "conflict", "checksum_rejected", "server_error", "lost_response", "missing_checksum", "wrong_checksum", "composite_checksum", "redirect", "credential_failure", "empty_credentials"} {
		t.Run(mode, func(t *testing.T) {
			var requests, redirected atomic.Int64
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
			defer other.Close()
			credentialFailure := errors.New("synthetic credential unavailable")
			backend := s3TestServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPut {
					t.Error("failed PUT triggered reconciliation I/O")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				switch mode {
				case "permission":
					s3TestError(w, 403, "AccessDenied")
				case "conflict":
					s3TestError(w, 409, "ConditionalRequestConflict")
				case "checksum_rejected":
					s3TestError(w, 400, "XAmzContentChecksumMismatch")
				case "server_error":
					s3TestError(w, 500, "InternalError")
				case "lost_response":
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
				case "missing_checksum":
					w.WriteHeader(200)
				case "wrong_checksum":
					s3TestPutResponse(w, []byte("different"))
				case "composite_checksum":
					w.Header().Set("X-Amz-Checksum-Sha256", s3TestChecksum([]byte("payload")))
					w.Header().Set("X-Amz-Checksum-Type", "COMPOSITE")
					w.WriteHeader(200)
				case "redirect":
					w.Header().Set("Location", other.URL)
					w.WriteHeader(http.StatusTemporaryRedirect)
				}
			}, func(config *S3Config) {
				if mode == "credential_failure" {
					config.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return aws.Credentials{}, credentialFailure })
				}
				if mode == "empty_credentials" {
					config.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return aws.Credentials{}, nil })
				}
			})
			info, err := backend.Save(t.Context(), "private/result.bin", strings.NewReader("payload"), SaveOptions{})
			want, valid, calls := Uncertain, true, int64(1)
			if mode == "permission" || mode == "conflict" || mode == "checksum_rejected" || mode == "credential_failure" || mode == "empty_credentials" {
				want, valid = NotPublished, false
			}
			if mode == "empty_credentials" {
				calls = 0
			}
			if mode == "credential_failure" {
				calls = 0
				if !errors.Is(err, credentialFailure) {
					t.Fatal("credential cause was lost")
				}
			}
			if mode == "missing_checksum" || mode == "wrong_checksum" || mode == "composite_checksum" {
				want = Published
			}
			var failure *Error
			if !errors.As(err, &failure) || failure.Outcome != want || info.Valid() != valid || requests.Load() != calls || redirected.Load() != 0 {
				t.Fatal("S3 result was retried, redirected or given the wrong publication outcome", err, requests.Load(), failure)
			}
			if valid && (info.Name() != "private/result.bin" || info.Size() != 7 || info.ContentMetadata().Version != "") {
				t.Fatal("unverified receipt claimed verified content metadata")
			}
			if strings.Contains(fmt.Sprintf("%+v %#v", err, err), "synthetic private response") {
				t.Fatal("provider error exposed private service text")
			}
		})
	}
}

func TestS3SaveBoundsInputAndReleasesReservationsOnFailure(t *testing.T) {
	for _, mode := range []string{"too_large", "capacity", "negative", "excess", "error_after_bytes", "joined_eof", "no_progress", "canceled_after_read", "panic", "nil", "typed_nil"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int64
			backend := s3TestServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				data, _ := io.ReadAll(r.Body)
				s3TestPutResponse(w, data)
			}, func(c *S3Config) {
				c.Limits.MaxFileBytes = 3
				c.MaxBufferedBytes = 3
				c.MaxConcurrentSaves = 1
				if mode == "capacity" {
					c.Limits.MaxFileBytes = 4
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("synthetic source failure")
			var source io.Reader = strings.NewReader("four")
			want := "file_too_large"
			switch mode {
			case "capacity":
				want = "capacity_exceeded"
			case "negative", "excess":
				want = "invalid_read_count"
				source = s3ReadFunc(func(p []byte) (int, error) {
					if mode == "negative" {
						return -1, cause
					}
					return len(p) + 1, cause
				})
			case "error_after_bytes", "joined_eof":
				want = "input_read_failed"
				source = s3ReadFunc(func(p []byte) (int, error) {
					n := copy(p, "a")
					if mode == "joined_eof" {
						return n, errors.Join(io.EOF, cause)
					}
					return n, cause
				})
			case "no_progress":
				want = "input_read_failed"
				source = s3ReadFunc(func([]byte) (int, error) { return 0, nil })
			case "canceled_after_read":
				want = "canceled"
				source = s3ReadFunc(func(p []byte) (int, error) { n := copy(p, "a"); cancel(); return n, io.EOF })
			case "panic":
				source = s3ReadFunc(func([]byte) (int, error) { panic(cause) })
			case "nil":
				source, want = nil, "invalid_input"
			case "typed_nil":
				var reader *bytes.Reader
				source, want = reader, "invalid_input"
			}
			if mode == "panic" {
				func() {
					defer func() {
						if recover() != cause {
							t.Fatal("source panic changed")
						}
					}()
					_, _ = backend.Save(ctx, "failure.bin", source, SaveOptions{})
				}()
			} else {
				info, err := backend.Save(ctx, "failure.bin", source, SaveOptions{})
				if info.Valid() || !errors.Is(err, &Error{Code: want, Outcome: NotPublished}) {
					t.Fatal("input failure became a publication", mode, err)
				}
				if (mode == "negative" || mode == "excess" || mode == "error_after_bytes" || mode == "joined_eof") && !errors.Is(err, cause) {
					t.Fatal("input cause was discarded")
				}
			}
			if requests.Load() != 0 {
				t.Fatal("rejected input reached S3")
			}
			if info, err := backend.Save(t.Context(), "valid.bin", strings.NewReader("abc"), SaveOptions{}); err != nil || !info.Valid() {
				t.Fatal("failed source leaked buffered capacity or a save slot", err)
			}
			if requests.Load() != 1 {
				t.Fatal("valid source was retried")
			}
		})
	}
}

func TestS3SaveAdmissionAndCloseWaitForOwnedOperation(t *testing.T) {
	backend := s3TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		s3TestPutResponse(w, data)
	}, func(c *S3Config) { c.MaxConcurrentSaves = 1 })
	entered, release, saved := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	source := s3ReadFunc(func(p []byte) (int, error) { close(entered); <-release; return copy(p, "a"), io.EOF })
	go func() { _, err := backend.Save(t.Context(), "first.bin", source, SaveOptions{}); saved <- err }()
	<-entered
	reads := 0
	if _, err := backend.Save(t.Context(), "second.bin", s3ReadFunc(func([]byte) (int, error) { reads++; return 0, io.EOF }), SaveOptions{}); !errors.Is(err, &Error{Code: "capacity_exceeded", Outcome: NotPublished}) || reads != 0 {
		t.Fatal("saturated S3 backend started another source")
	}
	closed := make(chan error, 1)
	go func() { closed <- backend.Close() }()
	select {
	case <-closed:
		t.Fatal("Close abandoned an active publication")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Save(t.Context(), "closed.bin", strings.NewReader("x"), SaveOptions{}); !errors.Is(err, &Error{Code: "closed"}) {
		t.Fatal("closed S3 accepted publication")
	}
}

func TestStorageCollisionNamesRespectUTF8ComponentAndRemoteKeyBudgets(t *testing.T) {
	name := strings.Repeat("가", 83) + ".tar"
	for _, kind := range []string{"filesystem", "memory", "s3"} {
		t.Run(kind, func(t *testing.T) {
			var backend Backend
			var close func() error
			random := s3ReadFunc(func(p []byte) (int, error) { clear(p); return len(p), nil })
			switch kind {
			case "filesystem":
				b, err := OpenFilesystem(t.Context(), FilesystemConfig{Directory: t.TempDir(), Random: random})
				if err != nil {
					t.Fatal(err)
				}
				backend, close = b, b.Close
			case "memory":
				b, err := NewMemory(MemoryConfig{Random: random})
				if err != nil {
					t.Fatal(err)
				}
				backend, close = b, b.Close
			case "s3":
				var calls atomic.Int64
				b := s3TestServer(t, func(w http.ResponseWriter, r *http.Request) {
					data, _ := io.ReadAll(r.Body)
					if calls.Add(1) == 2 {
						s3TestError(w, 412, "PreconditionFailed")
						return
					}
					s3TestPutResponse(w, data)
				}, func(c *S3Config) { c.Random = random })
				backend, close = b, b.Close
			}
			defer close()
			first, err := backend.Save(t.Context(), name, strings.NewReader("a"), SaveOptions{MaxLength: 100})
			if err != nil || first.Name() != name {
				t.Fatal("valid original UTF8 name changed", err)
			}
			second, err := backend.Save(t.Context(), name, strings.NewReader("b"), SaveOptions{MaxLength: 100})
			if err != nil || second.Name() == name || !utf8.ValidString(second.Name()) || len(second.Name()) > 255 || !strings.HasSuffix(second.Name(), "_AAAAAAA.tar") {
				t.Fatal("collision name exceeded the UTF8 component budget", err)
			}
		})
	}
	requests := make(chan string, 1)
	prefix := strings.Repeat("p", 250) + "/" + strings.Repeat("q", 250) + "/" + strings.Repeat("r", 250) + "/" + strings.Repeat("s", 250)
	backend := s3TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		requests <- strings.TrimPrefix(r.URL.Path, "/private-bucket/")
		s3TestPutResponse(w, data)
	}, func(c *S3Config) { c.Prefix = prefix })
	info, err := backend.Save(t.Context(), "longer-object-name.tar", strings.NewReader("a"), SaveOptions{})
	if err != nil {
		t.Fatal("S3 complete key byte budget was rejected", err)
	}
	requested := <-requests
	if len(requested) > 1024 || requested != prefix+"/"+info.Name() || !strings.HasSuffix(info.Name(), ".tar") {
		t.Fatal("S3 complete key byte budget was ignored", err)
	}
	if _, err := backend.Open(t.Context(), strings.Repeat("x", 250)); !errors.Is(err, &Error{Code: "name_too_long"}) {
		t.Fatal("oversized stored key reached network", err)
	}
}

func TestS3ConcurrentInstancesCannotOverwriteOneName(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if _, exists := objects[r.URL.Path]; exists && r.Header.Get("If-None-Match") == "*" {
			s3TestError(w, 412, "PreconditionFailed")
			return
		}
		objects[r.URL.Path] = bytes.Clone(data)
		s3TestPutResponse(w, data)
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()
	var backends []*S3
	for range 2 {
		config := s3TestConfig()
		config.Endpoint, config.AllowLoopbackHTTP = server.URL, true
		b, err := NewS3(config)
		if err != nil {
			t.Fatal(err)
		}
		backends = append(backends, b)
		defer b.Close()
	}
	var workers sync.WaitGroup
	for index := range 16 {
		workers.Go(func() {
			data := fmt.Sprintf("payload-%d", index)
			info, err := backends[index%2].Save(t.Context(), "shared.bin", strings.NewReader(data), SaveOptions{})
			if err != nil || !info.Valid() {
				t.Error("concurrent S3 publication failed", err)
			}
		})
	}
	workers.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(objects) != 16 {
		t.Fatal("independent S3 instances overwrote a shared name")
	}
	seen := map[string]bool{}
	for _, data := range objects {
		seen[string(data)] = true
	}
	if len(seen) != 16 {
		t.Fatal("concurrent S3 content was replaced")
	}
}

func TestS3CollisionEndsPreviousRequestBodyBeforeStartingNextCandidate(t *testing.T) {
	config := s3TestConfig()
	var first io.ReadCloser
	calls := 0
	content := "complete bounded content"
	config.Transport = s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		response := httptest.NewRecorder()
		if calls == 1 {
			first = request.Body
			s3TestError(response, 412, "PreconditionFailed")
		} else {
			// A transport can retain the rejected request while the SDK returns
			// its response. It must not borrow the next attempt's live cursor.
			if n, err := first.Read(make([]byte, 64)); n != 0 || err == nil {
				t.Error("rejected request retained a live upload cursor")
			}
			payload, err := io.ReadAll(request.Body)
			if err != nil || string(payload) != content {
				t.Error("next candidate lost its independent upload content", err)
			}
			s3TestPutResponse(response, payload)
		}
		result := response.Result()
		result.Request = request
		return result, nil
	})
	backend, err := NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	info, err := backend.Save(t.Context(), "same.bin", strings.NewReader(content), SaveOptions{})
	if err != nil || !info.Valid() || calls != 2 {
		t.Fatal("independent collision attempt failed", err, calls)
	}
	if n, err := first.Read(make([]byte, 64)); n != 0 || err == nil {
		t.Fatal("completed Save retained its borrowed request stream")
	}
}
