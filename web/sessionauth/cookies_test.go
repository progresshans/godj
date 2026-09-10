package sessionauth

import (
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
	if response.Status() != http.StatusNoContent || len(response.Body()) != 0 || len(response.Header().Values("Set-Cookie")) != 2 {
		t.Fatalf("response = %d, %#v", response.Status(), response.Header())
	}
	if len(original.Header()) != 0 {
		t.Fatal("cookie application changed original response")
	}
	if _, err := change.Apply(web.Response{}); err == nil {
		t.Fatal("zero response accepted")
	}
}
