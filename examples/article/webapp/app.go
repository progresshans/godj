// Package webapp is the Article example's explicit HTTP adapter. It keeps the
// generated declaration runner independent from runtime generated imports.
package webapp

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/progresshans/godj/apps"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	articleproject "github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

const (
	ArticleListRoute = "godj_conformance:article-list"
	ArticleListPath  = "/articles/"

	defaultArticleListLimit = 20
	maximumArticleListLimit = 100
	articleListBadQueryBody = "Bad Request\n"
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
	options, err := parseArticleListOptions(request)
	if err != nil {
		return web.HTML(http.StatusBadRequest, []byte(articleListBadQueryBody))
	}
	bound, err := articleproject.Using(h.backend)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: bind request facade: %w", err)
	}
	matching := bound.ModelsArticle
	if options.Published != nil {
		matching = matching.Filter(articlemodels.ArticleFields.Published.Exact(*options.Published))
	}
	matching = matching.Distinct()
	pageQuery, err := matching.
		OrderBy(articlemodels.ArticleFields.ID.Asc()).
		Offset(options.Offset)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: apply offset: %w", err)
	}
	pageQuery, err = pageQuery.Limit(options.Limit)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: apply limit: %w", err)
	}
	projection := orm.Project4(
		articlemodels.ArticleFields.ID,
		articlemodels.ArticleFields.Title,
		articlemodels.ArticleFields.Published,
		articlemodels.ArticleFields.Summary,
		func(id int64, title string, published bool, summary *string) ArticleView {
			return ArticleView{ID: id, Title: title, Published: published, Summary: summary}
		},
	)
	views, err := articleproject.SelectModelsArticleInto(request.Context(), pageQuery, projection)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: project page: %w", err)
	}
	aggregate := orm.Aggregate2(
		orm.CountRows[articlemodels.Article](),
		orm.Max(articlemodels.ArticleFields.ID),
		func(count int64, latest orm.Optional[int64]) articleListReport {
			report := articleListReport{MatchingCount: count}
			if id, present := latest.Get(); present {
				report.LatestID = &id
			}
			return report
		},
	)
	report, err := articleproject.AggregateModelsArticleInto(request.Context(), matching, aggregate)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: aggregate report: %w", err)
	}
	selfURL, err := request.Reverse(ArticleListRoute)
	if err != nil {
		return web.Response{}, fmt.Errorf("article list: reverse self: %w", err)
	}
	page := articleListPage{
		ProjectName: request.Settings().ProjectName(),
		SelfURL:     selfURL,
		Articles:    views,
		Report:      report,
		Pagination: articleListPagination{
			Offset:   options.Offset,
			Limit:    options.Limit,
			Returned: len(views),
		},
	}
	var body bytes.Buffer
	if err := h.template.ExecuteTemplate(&body, "article_list.html", page); err != nil {
		return web.Response{}, fmt.Errorf("article list: render template: %w", err)
	}
	return web.HTML(http.StatusOK, body.Bytes())
}

type articleListOptions struct {
	Published *bool
	Offset    int
	Limit     int
}

func parseArticleListOptions(request *web.Request) (articleListOptions, error) {
	options := articleListOptions{Limit: defaultArticleListLimit}
	httpRequest := request.HTTP()
	if httpRequest == nil || httpRequest.URL == nil {
		return articleListOptions{}, invalidArticleListQuery("request URL is unavailable")
	}
	values, parseErr := url.ParseQuery(httpRequest.URL.RawQuery)
	if parseErr != nil {
		return articleListOptions{}, invalidArticleListQuery("query string is malformed")
	}
	if raw, present, err := articleListQueryValue(values, "published"); err != nil {
		return articleListOptions{}, err
	} else if present {
		var published bool
		switch raw {
		case "true":
			published = true
		case "false":
			published = false
		default:
			return articleListOptions{}, invalidArticleListQuery("published must be true or false")
		}
		options.Published = &published
	}
	if raw, present, err := articleListQueryValue(values, "offset"); err != nil {
		return articleListOptions{}, err
	} else if present {
		offset, parseErr := parseArticleListUnsigned(raw)
		if parseErr != nil || offset > math.MaxInt32 {
			return articleListOptions{}, invalidArticleListQuery("offset is outside the supported range")
		}
		options.Offset = int(offset)
	}
	if raw, present, err := articleListQueryValue(values, "limit"); err != nil {
		return articleListOptions{}, err
	} else if present {
		limit, parseErr := parseArticleListUnsigned(raw)
		if parseErr != nil || limit == 0 {
			return articleListOptions{}, invalidArticleListQuery("limit must be a positive decimal integer")
		}
		if limit > maximumArticleListLimit {
			limit = maximumArticleListLimit
		}
		options.Limit = int(limit)
	}
	return options, nil
}

func articleListQueryValue(values url.Values, name string) (string, bool, error) {
	entries, present := values[name]
	if !present {
		return "", false, nil
	}
	if len(entries) != 1 {
		return "", false, invalidArticleListQuery(name + " must appear at most once")
	}
	return entries[0], true, nil
}

func parseArticleListUnsigned(raw string) (uint64, error) {
	if raw == "" {
		return 0, invalidArticleListQuery("numeric parameter is empty")
	}
	for index := 0; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, invalidArticleListQuery("numeric parameter is not an unsigned decimal integer")
		}
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, invalidArticleListQuery("numeric parameter overflows uint64")
	}
	return value, nil
}

func invalidArticleListQuery(detail string) error {
	return fmt.Errorf("invalid article list query: %s", detail)
}
