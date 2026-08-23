// Package webapp is the Article example's explicit HTTP adapter. It keeps the
// generated declaration runner independent from runtime generated imports.
package webapp

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"

	"github.com/progresshans/godj/apps"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	articleproject "github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

const (
	ArticleListRoute = "godj_conformance:article-list"
	ArticleListPath  = "/articles/"
)

//go:embed templates/article_list.html
var templateFiles embed.FS

// NewApplication builds the Article HTTP application without performing
// database I/O. The backend pool may live for the application lifetime, but a
// fresh generated facade is created inside every request.
func NewApplication(backend articleproject.Backend) (*web.Application, error) {
	if _, err := articleproject.Using(backend); err != nil {
		return nil, fmt.Errorf("article web application: bind backend: %w", err)
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "article_example",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: "godj_conformance",
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("article web application: settings: %w", err)
	}
	articleTemplate, err := template.New("article_list.html").
		Option("missingkey=error").
		ParseFS(templateFiles, "templates/article_list.html")
	if err != nil {
		return nil, fmt.Errorf("article web application: parse templates: %w", err)
	}

	handler := articleListHandler{
		backend:  backend,
		template: articleTemplate,
	}
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{{
			Name:    ArticleListRoute,
			Method:  http.MethodGet,
			Path:    ArticleListPath,
			Handler: handler.serve,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("article web application: configure web runtime: %w", err)
	}
	return application, nil
}

type articleListHandler struct {
	backend  articleproject.Backend
	template *template.Template
}

func (h articleListHandler) serve(request *web.Request) (web.Response, error) {
	bound, err := articleproject.Using(h.backend)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: bind request facade: %w", err)
	}
	articles, err := bound.ModelsArticle.
		OrderBy(articlemodels.ArticleFields.ID.Asc()).
		All(request.Context())
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: query: %w", err)
	}
	views := make([]ArticleView, len(articles))
	for index, article := range articles {
		views[index], err = NewArticleView(article)
		if err != nil {
			return web.Response{}, err
		}
	}
	selfURL, err := request.Reverse(ArticleListRoute)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: reverse self: %w", err)
	}
	page := articleListPage{
		ProjectName: request.Settings().ProjectName(),
		SelfURL:     selfURL,
		Articles:    views,
	}
	var body bytes.Buffer
	if err := h.template.ExecuteTemplate(&body, "article_list.html", page); err != nil {
		return web.Response{}, fmt.Errorf("article list: render template: %w", err)
	}
	return web.HTML(http.StatusOK, body.Bytes())
}
