package web_test

import (
	"net/http"
	"testing"

	"github.com/progresshans/godj/web"
)

func TestResponseSnapshotsHeadersAndBody(t *testing.T) {
	header := http.Header{"X-Test": {"before"}}
	body := []byte("body")
	response, err := web.NewResponse(http.StatusCreated, header, body)
	if err != nil {
		t.Fatal(err)
	}
	header.Set("X-Test", "after")
	body[0] = 'B'
	if got := response.Header().Get("X-Test"); got != "before" {
		t.Fatalf("Header() = %q", got)
	}
	if got := string(response.Body()); got != "body" {
		t.Fatalf("Body() = %q", got)
	}
	returned := response.Body()
	returned[0] = 'B'
	if got := string(response.Body()); got != "body" {
		t.Fatalf("Body() after returned mutation = %q", got)
	}
}

func TestResponseRejectsInvalidStatusAndBody(t *testing.T) {
	if _, err := web.NewResponse(199, nil, nil); err == nil {
		t.Fatal("NewResponse(199) error = nil")
	}
	if _, err := web.NewResponse(http.StatusNoContent, nil, []byte("body")); err == nil {
		t.Fatal("NewResponse(204, body) error = nil")
	}
	if _, err := web.NewResponse(http.StatusOK, http.Header{"Bad Header": {"value"}}, nil); err == nil {
		t.Fatal("NewResponse(invalid header name) error = nil")
	}
	if _, err := web.NewResponse(http.StatusOK, http.Header{"X-Test": {"safe\r\ninjected"}}, nil); err == nil {
		t.Fatal("NewResponse(invalid header value) error = nil")
	}
	if _, err := web.NewResponse(http.StatusOK, http.Header{"X-Test": {"unsafe\x00value"}}, nil); err == nil {
		t.Fatal("NewResponse(control header value) error = nil")
	}
}

func TestResponseWithHeadersPreservesBodyAndRoutingOrigin(t *testing.T) {
	original, err := web.NewResponse(http.StatusCreated, http.Header{"X-Old": {"old"}}, []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{"X-New": {"new"}}
	derived, err := original.WithHeaders(header)
	if err != nil {
		t.Fatal(err)
	}
	header["X-New"][0] = "mutated"
	derived.Header().Set("X-New", "also mutated")
	derived.Body()[0] = 'B'
	if derived.Status() != http.StatusCreated || derived.Header().Get("X-New") != "new" || derived.Header().Get("X-Old") != "" || string(derived.Body()) != "body" {
		t.Fatal("derived response did not retain its immutable snapshot")
	}
	if original.Header().Get("X-Old") != "old" || original.Header().Get("X-New") != "" || string(original.Body()) != "body" {
		t.Fatal("original response changed")
	}
	if _, err := original.WithHeaders(http.Header{"X-Bad": {"line\r\nbreak"}}); err == nil {
		t.Fatal("invalid derived header accepted")
	}
	if _, err := (web.Response{}).WithHeaders(nil); err == nil {
		t.Fatal("zero response accepted")
	}

	application := newTestApplication(t, web.Config{
		Routes: []web.Route{{Name: "articles:list", Method: http.MethodGet, Path: "/articles/", Handler: textHandler("list")}},
		Middleware: []web.Middleware{func(next web.Handler) web.Handler {
			return func(request *web.Request) (web.Response, error) {
				response, err := next(request)
				if err != nil {
					return response, err
				}
				derived, err := response.WithHeaders(http.Header{"X-Trace": {"checked"}})
				if code, ok := web.RoutingError(derived); !ok || code != web.CodeRouteNotFound {
					t.Errorf("routing origin lost: %q, %v", code, ok)
				}
				return derived, err
			}
		}},
	})
	if got := serve(application, http.MethodGet, "/missing/"); got.Code != http.StatusNotFound || got.Header().Get("X-Trace") != "checked" {
		t.Fatalf("derived router response = %d", got.Code)
	}
}
