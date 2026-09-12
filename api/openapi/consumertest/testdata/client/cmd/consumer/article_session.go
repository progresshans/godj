package main

import (
	"context"
	"net/http"
	"strconv"

	as "example.com/godj-openapi-client/articlesession"
)

func checkArticleSession(ctx context.Context, target endpoint) error {
	httpClient, transport, state, err := newSessionClient(target, target.Session, "/api/articles/")
	if err != nil {
		return err
	}
	defer transport.base.CloseIdleConnections()
	client, err := as.NewClient(target.URL, articleSessionSource{state}, as.WithClient(httpClient))
	if err != nil {
		return fail("session article client setup")
	}
	initial, err := sessionArticleList(ctx, client, transport, state)
	if err != nil || initial.Count != 0 || len(initial.Results) != 0 || !initial.Next.Null || !initial.Previous.Null {
		return fail("session initial article list")
	}
	created, err := client.GodjConformanceArticleCreate(ctx, &as.ArticleCreate{Title: "Session Article"})
	createdArticle, ok := created.(*as.ArticleHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated {
		return fail("session article create")
	}
	article := createdArticle.Response
	if article.ID <= 0 || article.Title != "Session Article" || article.Published || !article.Summary.Null || createdArticle.Location != "/api/articles/"+strconv.FormatInt(article.ID, 10)+"/" {
		return fail("session article create defaults")
	}
	patched, err := client.GodjConformanceArticlePartialUpdate(ctx, &as.ArticlePatch{
		Published: as.NewOptBool(true), Summary: as.NewOptNilString("session summary"),
	}, as.GodjConformanceArticlePartialUpdateParams{ID: article.ID})
	updated, ok := patched.(*as.Article)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || updated.ID != article.ID || updated.Title != article.Title || !updated.Published || updated.Summary.Null || updated.Summary.Value != "session summary" {
		return fail("session article patch")
	}
	state.setInvalid(true)
	denied, err := client.GodjConformanceArticleCreate(ctx, &as.ArticleCreate{Title: "Rejected by CSRF"})
	state.setInvalid(false)
	forbidden, ok := denied.(*as.GodjConformanceArticleCreateForbidden)
	if err != nil || !ok || transport.lastStatus() != http.StatusForbidden || forbidden.Code != "csrf_rejected" {
		return fail("session invalid csrf rejection")
	}
	afterRejected, err := sessionArticleList(ctx, client, transport, state)
	if err != nil || afterRejected.Count != 1 || len(afterRejected.Results) != 1 || afterRejected.Results[0] != *updated {
		return fail("session csrf rejection preserved articles")
	}
	deleted, err := client.GodjConformanceArticleDelete(ctx, as.GodjConformanceArticleDeleteParams{ID: article.ID})
	if _, ok := deleted.(*as.GodjConformanceArticleDeleteNoContent); err != nil || !ok || transport.lastStatus() != http.StatusNoContent {
		return fail("session article delete")
	}
	final, err := sessionArticleList(ctx, client, transport, state)
	if err != nil || final.Count != 0 || len(final.Results) != 0 || !final.Next.Null || !final.Previous.Null {
		return fail("session final article list")
	}
	return nil
}

func sessionArticleList(ctx context.Context, client *as.Client, transport *observedTransport, state *sessionState) (*as.ArticlePage, error) {
	response, err := client.GodjConformanceArticleList(ctx, as.GodjConformanceArticleListParams{})
	page, ok := response.(*as.ArticlePageHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || !page.XGodjCsrftoken.Set || !transport.capturedCSRF() || !state.ready(page.XGodjCsrftoken.Value) {
		return nil, fail("session safe article response")
	}
	return &page.Response, nil
}
