package webapp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/web"
)

func TestComposedApplicationKeepsLegacyConstructorReverseAndUsesPrivateWebIdentity(t *testing.T) {
	backend, err := sqlite.OpenMemory(context.Background(), "article-web-composition-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`); err != nil {
		t.Fatal(err)
	}

	legacy, err := webapp.NewApplication(backend)
	if err != nil {
		t.Fatal(err)
	}
	if path, err := legacy.Reverse(webapp.ArticleListRoute); err != nil || path != webapp.ArticleListPath {
		t.Fatalf("legacy reverse = %q, %v", path, err)
	}
	withoutCollision, err := webapp.NewComposedApplication(backend, []web.Route{{
		Name:   "godj_conformance:probe",
		Method: http.MethodGet,
		Path:   "/probe/",
		Handler: func(*web.Request) (web.Response, error) {
			return web.HTML(http.StatusOK, []byte("probe"))
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path, err := withoutCollision.Reverse(webapp.ArticleListRoute); err != nil || path != webapp.ArticleListPath {
		t.Fatalf("non-conflicting composed reverse = %q, %v", path, err)
	}

	// The committed API owns the same historical route-name bytes. Composed
	// mode therefore gives the public Web handler a private reverse identity,
	// while preserving its path and rendered self-link.
	composed, err := webapp.NewComposedApplication(backend, []web.Route{{
		Name:   webapp.ArticleListRoute,
		Method: http.MethodGet,
		Path:   "/api-probe/",
		Handler: func(*web.Request) (web.Response, error) {
			return web.HTML(http.StatusOK, []byte("api probe"))
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path, err := composed.Reverse(webapp.ArticleListRoute); err != nil || path != "/api-probe/" {
		t.Fatalf("composed additional-route reverse = %q, %v", path, err)
	}
	recorder := httptest.NewRecorder()
	composed.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://example.test"+webapp.ArticleListPath, nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `href="/articles/"`) {
		t.Fatalf("composed public response = status %d body %q", recorder.Code, recorder.Body.String())
	}
}
