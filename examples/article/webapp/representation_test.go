package webapp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	articleproject "github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/examples/article/webapp"
)

func TestArticleViewExplicitlyUnwrapsAndSerializesRawFields(t *testing.T) {
	backend, err := sqlite.OpenMemory(context.Background(), "article-view-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	bound, err := articleproject.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	summary := ""
	wrapper, err := bound.ModelsArticle.New(articlemodels.Article{
		ID:        7,
		Title:     "<script>alert(1)</script>",
		Published: true,
		Summary:   &summary,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := webapp.NewArticleView(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	summary = "mutated"
	if view.ID != 7 || view.Title != "<script>alert(1)</script>" || !view.Published || view.Summary == nil || *view.Summary != "" {
		t.Fatalf("view = %#v", view)
	}
	payload, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "<script>") || !strings.Contains(string(payload), `\u003cscript\u003e`) || !strings.Contains(string(payload), `"summary":""`) {
		t.Fatalf("JSON payload = %s", payload)
	}
}

func TestArticleViewRejectsNilWrapper(t *testing.T) {
	if _, err := webapp.NewArticleView(nil); err == nil {
		t.Fatal("NewArticleView(nil) error = nil")
	}
}
