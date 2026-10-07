package apiapp_test

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/examples/article/articleapp"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type articleObservedBody struct {
	io.Reader
	reads   int
	failure error
}

func (body *articleObservedBody) Read(data []byte) (int, error) {
	body.reads++
	if body.failure != nil {
		return 0, body.failure
	}
	return body.Reader.Read(data)
}
func (*articleObservedBody) Close() error { return nil }

func TestTypedArticleLookupAndAdmissionPrecedeBodyIO(t *testing.T) {
	harness := newHarness(t)
	created, err := harness.repository.Create(t.Context(), articleapp.Input{Title: "Existing"})
	if err != nil {
		t.Fatal(err)
	}
	token, csrf := harness.csrf(t, harness.allSession, "/api/articles/")
	for _, method := range []string{"PUT", "PATCH"} {
		for _, test := range []struct {
			name, path string
			status     int
			read       bool
		}{
			{"existing malformed", "/api/articles/1/", 400, true},
			{"missing malformed", "/api/articles/9223372036854775807/", 404, false},
			{"zero id", "/api/articles/0/", 404, false},
			{"invalid router id", "/api/articles/01/", 404, false},
			{"missing csrf", "/api/articles/1/", 403, false},
			{"anonymous", "/api/articles/1/", 403, false},
			{"reader failure", "/api/articles/1/", 500, true},
		} {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &articleObservedBody{Reader: strings.NewReader(`{`)}
				if test.name == "reader failure" {
					body.failure = errors.New("private Article reader failure")
				}
				request := httptest.NewRequest(method, "http://attacker.example"+test.path, nil)
				request.Body, request.ContentLength = body, -1
				request.Header.Set("Accept", api.JSONContentType)
				request.Header.Set("Content-Type", api.JSONContentType)
				if test.name != "anonymous" {
					request.AddCookie(harness.allSession)
				}
				if test.name != "missing csrf" {
					request.AddCookie(csrf)
					request.Header.Set(websessionauth.DefaultCSRFHeader, token)
				}
				response := httptest.NewRecorder()
				harness.application.ServeHTTP(response, request)
				if response.Code != test.status || (body.reads > 0) != test.read || strings.Contains(response.Body.String(), "private") {
					t.Fatal("Article body/lookup order changed", response.Code, response.Body, body.reads)
				}
				assertArticle(t, harness.repository, created.ID, created.Title, false, nil)
			})
		}
	}
	if count := articleCount(t, harness.repository); count != 1 {
		t.Fatal("failed input changed database", count)
	}
}

var _ io.ReadCloser = (*articleObservedBody)(nil)
