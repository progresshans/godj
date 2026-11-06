package sessionauth

import (
	"context"
	"net/http"
	"testing"

	"github.com/progresshans/godj/web"
)

func TestResponseChangeAppliesToNilHeaders(t *testing.T) {
	original, err := web.NewResponse(http.StatusNoContent, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	change := ResponseChange{cookies: []http.Cookie{{Name: "first", Value: "one"}, {Name: "second", Value: "two"}}}
	response, err := change.Apply(original)
	if err != nil {
		t.Fatal(err)
	}
	body, bodyErr := response.Body()
	if bodyErr != nil || response.Status() != http.StatusNoContent || len(body) != 0 || len(response.Header().Values("Set-Cookie")) != 2 {
		t.Fatalf("response = %d, %#v", response.Status(), response.Header())
	}
	if len(original.Header()) != 0 {
		t.Fatal("cookie application changed original response")
	}
	if _, err := change.Apply(web.Response{}); err == nil {
		t.Fatal("zero response accepted")
	}
}

func TestResponseChangePreservesLazyStream(t *testing.T) {
	opened := false
	original, err := web.NewStreamResponse(http.StatusOK, nil, func(context.Context) (web.Stream, error) {
		opened = true
		return web.Stream{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	change := ResponseChange{cookies: []http.Cookie{{Name: "session", Value: "rotated"}}}
	response, err := change.Apply(original)
	if err != nil || !response.Streaming() || opened || len(response.Header().Values("Set-Cookie")) != 1 || len(original.Header()) != 0 {
		t.Fatal("cookie mutation consumed or lost streaming description", err)
	}
	if _, err := response.Body(); err == nil {
		t.Fatal("stream was silently materialized")
	}
}
