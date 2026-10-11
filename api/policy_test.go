package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func TestJSONPolicyReportsAndAppliesTheSameSubtree(t *testing.T) {
	configured, err := api.NewJSONPolicy("/api/")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		policy api.JSONPolicy
	}{
		{name: "configured", policy: configured},
		{name: "unconfigured"},
	} {
		t.Run(test.name, func(t *testing.T) {
			detached := test.policy.Middleware()
			if len(detached) != 0 {
				detached[0] = nil
			}
			calls := 0
			application := testApplication(t, test.policy.Middleware(), []web.Route{{
				Name: "test:item", Method: http.MethodGet, Path: "/api/items/",
				Handler: func(*web.Request) (web.Response, error) {
					calls++
					return api.JSON(http.StatusOK, serializers.String("item"))
				},
			}})
			for _, path := range []string{"/api/items/", "/api/missing/", "/apix/missing/"} {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("Accept", "text/html")
				recorder := httptest.NewRecorder()
				before := calls
				application.ServeHTTP(recorder, request)
				if test.policy.NegotiatesJSON(path) {
					if recorder.Code != http.StatusNotAcceptable || calls != before ||
						!strings.Contains(recorder.Body.String(), `"code":"not_acceptable"`) {
						t.Fatalf("negotiated response for %s = %d %s", path, recorder.Code, recorder.Body)
					}
				} else if path == "/api/items/" {
					if recorder.Code != http.StatusOK || calls != before+1 {
						t.Fatalf("unnegotiated handler = %d, calls=%d", recorder.Code, calls)
					}
				} else if recorder.Code != http.StatusNotFound {
					t.Fatalf("unnegotiated missing route = %d", recorder.Code)
				}
			}
			request := httptest.NewRequest(http.MethodGet, "/api/missing/", nil)
			request.Header.Set("Accept", api.JSONContentType)
			recorder := httptest.NewRecorder()
			application.ServeHTTP(recorder, request)
			jsonError := recorder.Header().Get("Content-Type") == api.JSONContentType
			if recorder.Code != http.StatusNotFound || jsonError != test.policy.NegotiatesJSON(request.URL.Path) {
				t.Fatalf("routing representation = %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
			}
		})
	}
	if policy, err := api.NewJSONPolicy("/api"); err == nil || len(policy.Middleware()) != 0 || policy.NegotiatesJSON("/api/items/") {
		t.Fatal("invalid prefix published a partial policy")
	}
}
