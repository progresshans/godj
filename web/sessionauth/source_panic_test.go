package sessionauth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCallbackPanicDoesNotBlockFollowingHTTPRequest(t *testing.T) {
	for _, source := range []string{"entropy", "clock"} {
		t.Run(source, func(t *testing.T) {
			ring, err := NewCSRFKeyRing(bytes.Repeat([]byte{7}, csrfSecretBytes))
			if err != nil {
				t.Fatal(err)
			}
			config := csrfKeyRuntimeConfig(t, ring, nil)
			if source == "entropy" {
				config.Random = &panicOnceCSRFEntropy{}
			} else {
				panicked := false
				config.Clock = func() time.Time {
					if !panicked {
						panicked = true
						panic("private clock failure")
					}
					return time.Now()
				}
			}
			runtime, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			application := newCSRFKeyApplication(t, runtime)
			first := httptest.NewRecorder()
			application.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/issue/", nil))
			if first.Code != http.StatusInternalServerError || first.Body.String() != "Internal Server Error\n" || len(first.Header().Values("Set-Cookie")) != 0 {
				t.Fatalf("panic response = %d, %q, cookies=%d", first.Code, first.Body.String(), len(first.Header().Values("Set-Cookie")))
			}
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				next := httptest.NewRecorder()
				application.ServeHTTP(next, httptest.NewRequest(http.MethodGet, "/issue/", nil))
				finished <- next
			}()
			select {
			case next := <-finished:
				if next.Code != http.StatusOK || next.Body.Len() != csrfEncodedTokenSize || len(next.Header().Values("Set-Cookie")) != 1 {
					t.Fatalf("recovered request = %d, token bytes=%d, cookies=%d", next.Code, next.Body.Len(), len(next.Header().Values("Set-Cookie")))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("callback panic retained the runtime source lock")
			}
		})
	}
}

type panicOnceCSRFEntropy struct{ panicked bool }

func (source *panicOnceCSRFEntropy) Read(buffer []byte) (int, error) {
	if !source.panicked {
		source.panicked = true
		panic(source)
	}
	for index := range buffer {
		buffer[index] = byte(index + 1)
	}
	return len(buffer), nil
}
