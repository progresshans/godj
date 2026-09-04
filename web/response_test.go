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
