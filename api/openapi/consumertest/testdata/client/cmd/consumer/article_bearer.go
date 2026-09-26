package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	ab "example.com/godj-openapi-client/articlebearer"
)

func checkArticleBearer(ctx context.Context, target endpoint) error {
	httpClient, transport := newHTTPClient()
	defer transport.base.CloseIdleConnections()
	client, err := ab.NewClient(target.URL, bearerSource{token: target.Token}, ab.WithClient(httpClient))
	if err != nil {
		return fail("bearer client setup")
	}
	if err := requireEmptyBearerList(ctx, client, transport); err != nil {
		return err
	}
	invalid, err := ab.NewClient(target.URL, bearerSource{token: "invalid-consumer-token"}, ab.WithClient(httpClient))
	if err != nil {
		return fail("invalid bearer client setup")
	}
	denied, err := invalid.GodjConformanceArticleList(ctx, ab.GodjConformanceArticleListParams{})
	unauthorized, ok := denied.(*ab.GodjConformanceArticleListUnauthorized)
	if err != nil || !ok || transport.lastStatus() != http.StatusUnauthorized || unauthorized.Response.Code != "not_authenticated" || !strings.Contains(unauthorized.WWWAuthenticate.Value, "invalid_token") {
		return fail("invalid bearer authentication")
	}
	readOnly, err := ab.NewClient(target.URL, bearerSource{token: target.ReadOnlyToken}, ab.WithClient(httpClient))
	if err != nil {
		return fail("read-only bearer client setup")
	}
	// An invalid title must not move body validation before permission checks.
	createDenied, err := readOnly.GodjConformanceArticleCreate(ctx, &ab.ArticleCreate{Title: ""})
	forbidden, ok := createDenied.(*ab.GodjConformanceArticleCreateForbidden)
	if err != nil || !ok || transport.lastStatus() != http.StatusForbidden || forbidden.Response.Code != "permission_denied" || !strings.Contains(forbidden.WWWAuthenticate.Value, "insufficient_scope") {
		return fail("bearer create permission")
	}
	created, err := client.GodjConformanceArticleCreate(ctx, &ab.ArticleCreate{
		Title: "  Consumer Article  ", Published: ab.NewOptBool(true), Summary: ab.NewOptNilString("  initial  "),
	})
	articleResponse, ok := created.(*ab.ArticleHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated {
		return fail("bearer article create")
	}
	article := articleResponse.Response
	if article.ID <= 0 || article.Title != "Consumer Article" || !article.Published || article.Summary.Null || article.Summary.Value != "initial" || articleResponse.Location != "/api/articles/"+strconv.FormatInt(article.ID, 10)+"/" {
		return fail("bearer created projection")
	}
	id := article.ID
	detail, err := client.GodjConformanceArticleDetail(ctx, ab.GodjConformanceArticleDetailParams{ID: id})
	read, ok := detail.(*ab.Article)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || *read != article {
		return fail("bearer created retrieval")
	}
	patch := func(request ab.ArticlePatch) (*ab.Article, error) {
		response, err := client.GodjConformanceArticlePartialUpdate(ctx, &request, ab.GodjConformanceArticlePartialUpdateParams{ID: id})
		value, ok := response.(*ab.Article)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || value.ID != id {
			return nil, fail("bearer article patch")
		}
		return value, nil
	}
	omitted, err := patch(ab.ArticlePatch{})
	if err != nil || *omitted != article {
		return fail("bearer omitted patch")
	}
	nullSummary := ab.OptNilString{}
	nullSummary.SetToNull()
	cleared, err := patch(ab.ArticlePatch{Summary: nullSummary})
	if err != nil || !cleared.Summary.Null || cleared.Title != article.Title || !cleared.Published {
		return fail("bearer null patch")
	}
	empty, err := patch(ab.ArticlePatch{Summary: ab.NewOptNilString("")})
	if err != nil || empty.Summary.Null || empty.Summary.Value != "" || empty.Title != article.Title || !empty.Published {
		return fail("bearer empty string patch")
	}
	valued, err := patch(ab.ArticlePatch{Summary: ab.NewOptNilString("  changed  ")})
	if err != nil || valued.Summary.Null || valued.Summary.Value != "changed" || valued.Title != article.Title || !valued.Published {
		return fail("bearer value patch")
	}
	unpublished, err := patch(ab.ArticlePatch{Published: ab.NewOptBool(false)})
	if err != nil || unpublished.Published || unpublished.Summary.Null || unpublished.Summary.Value != "changed" || unpublished.Title != article.Title {
		return fail("bearer explicit false patch")
	}
	republished, err := patch(ab.ArticlePatch{Published: ab.NewOptBool(true)})
	if err != nil || !republished.Published || republished.Summary.Null || republished.Summary.Value != "changed" {
		return fail("bearer prepare full replacement")
	}
	replaced, err := client.GodjConformanceArticleUpdate(ctx, &ab.ArticleReplace{Title: "  Replacement Article  "}, ab.GodjConformanceArticleUpdateParams{ID: id})
	replacement, ok := replaced.(*ab.Article)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || replacement.ID != id || replacement.Title != "Replacement Article" || replacement.Published || replacement.Summary.Null || replacement.Summary.Value != "changed" {
		return fail("bearer replacement defaults and summary preservation")
	}
	deleted, err := client.GodjConformanceArticleDelete(ctx, ab.GodjConformanceArticleDeleteParams{ID: id})
	if _, ok := deleted.(*ab.GodjConformanceArticleDeleteNoContent); err != nil || !ok || transport.lastStatus() != http.StatusNoContent {
		return fail("bearer article delete")
	}
	missing, err := client.GodjConformanceArticleDetail(ctx, ab.GodjConformanceArticleDetailParams{ID: id})
	notFound, ok := missing.(*ab.GodjConformanceArticleDetailNotFound)
	if err != nil || !ok || transport.lastStatus() != http.StatusNotFound || notFound.Code != "not_found" {
		return fail("bearer deleted retrieval")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = client.GodjConformanceArticleCreate(canceled, &ab.ArticleCreate{Title: "Canceled Article"})
	if !errors.Is(err, context.Canceled) {
		return fail("pre-canceled generated request")
	}
	// The parent also checks the database independently after process success.
	return requireEmptyBearerList(ctx, client, transport)
}

func requireEmptyBearerList(ctx context.Context, client *ab.Client, transport *observedTransport) error {
	response, err := client.GodjConformanceArticleList(ctx, ab.GodjConformanceArticleListParams{})
	page, ok := response.(*ab.ArticlePage)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || page.Count != 0 || len(page.Results) != 0 || !page.Next.Null || !page.Previous.Null {
		return fail("bearer empty article list")
	}
	return nil
}
