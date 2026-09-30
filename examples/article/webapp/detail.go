package webapp

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"

	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	articleproject "github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/web"
)

const (
	ArticleSlugRoute = "godj_conformance:article-public-slug"
	ArticleSlugPath  = "/articles/by-slug/<str:slug>/"
	ArticleIDRoute   = "godj_conformance:article-public-id"
	ArticleIDPath    = "/articles/<int64:id>/"
)

type articleDetailHandler struct {
	backend       articleproject.Backend
	template      *template.Template
	listRouteName string
}

// The public detail only exposes published rows. The explicit ID address also
// serves older rows that have no usable slug; invalid legacy strings never
// become URL syntax. Management remains behind the existing Admin/API guards.
func (h articleDetailHandler) serveSlug(request *web.Request) (web.Response, error) {
	slug, ok := request.StringParameter("slug")
	if !ok || !articleapp.ValidAddressSlug(slug) {
		return articlePublicNotFound(request)
	}
	return h.serve(request, articlemodels.ArticleFields.Slug.Exact(slug))
}

func (h articleDetailHandler) serveID(request *web.Request) (web.Response, error) {
	id, ok := request.Int64Parameter("id")
	if !ok || id <= 0 {
		return articlePublicNotFound(request)
	}
	return h.serve(request, articlemodels.ArticleFields.ID.Exact(id))
}

func (h articleDetailHandler) serve(request *web.Request, match orm.Predicate[articlemodels.Article]) (web.Response, error) {
	article, found, err := articlemodels.ArticleObjects.Using(h.backend).
		Filter(articlemodels.ArticleFields.Published.Exact(true), match).
		OrderBy(articlemodels.ArticleFields.ID.Asc()).First(request.Context())
	if err != nil {
		return web.Response{}, fmt.Errorf("article public detail: %w", err)
	}
	if !found {
		return articlePublicNotFound(request)
	}
	view := ArticleView{ID: article.ID, Title: article.Title, Published: article.Published, Summary: article.Summary, Slug: article.Slug}
	view.URL, err = articleAddress(request, view)
	if err != nil {
		return web.Response{}, err
	}
	listURL, err := request.Reverse(h.listRouteName)
	if err != nil {
		return web.Response{}, err
	}
	var body bytes.Buffer
	if err := h.template.ExecuteTemplate(&body, "article_detail.html", articleDetailPage{ProjectName: request.Settings().ProjectName(), ListURL: listURL, Article: view}); err != nil {
		return web.Response{}, fmt.Errorf("article public detail render: %w", err)
	}
	response, err := web.HTML(http.StatusOK, body.Bytes())
	return articlePublicBody(request, response, err)
}

func articleAddress(request *web.Request, article ArticleView) (string, error) {
	if !article.Published {
		return "", nil
	}
	if article.Slug != nil && articleapp.ValidAddressSlug(*article.Slug) {
		return request.ReverseWith(ArticleSlugRoute, web.StringArgument("slug", *article.Slug))
	}
	return request.ReverseWith(ArticleIDRoute, web.Int64Argument("id", article.ID))
}

func articlePublicNotFound(request *web.Request) (web.Response, error) {
	response, err := web.HTML(http.StatusNotFound, []byte("Not Found\n"))
	return articlePublicBody(request, response, err)
}

func articlePublicBody(request *web.Request, response web.Response, err error) (web.Response, error) {
	if err != nil {
		return web.Response{}, err
	}
	if request.Method() == http.MethodHead {
		return web.NewResponse(response.Status(), response.Header(), nil)
	}
	return response, nil
}
