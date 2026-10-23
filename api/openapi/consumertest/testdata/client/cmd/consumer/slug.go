package main

import (
	"context"
	"encoding/json"
	ab "example.com/godj-openapi-client/articlebearer"
	as "example.com/godj-openapi-client/articlesession"
	"math"
	"net/http"
	"strings"
)

func checkArticleBearerSlug(ctx context.Context, client *ab.Client, transport *observedTransport) error {
	created, err := client.GodjConformanceArticleCreate(ctx, &ab.ArticleCreate{Title: "Retained Slug Article", Published: ab.NewOptBool(true), Slug: ab.NewOptNilString(strings.Repeat(" ", 70) + "Client_읽기-주소" + "  ")})
	header, ok := created.(*ab.ArticleHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || header.Response.Slug.Null || header.Response.Slug.Value != "Client_읽기-주소" {
		return fail("Bearer slug create after normalization")
	}
	expected := header.Response
	id := expected.ID
	read := func() error {
		response, err := client.GodjConformanceArticleDetail(ctx, ab.GodjConformanceArticleDetailParams{ID: id})
		value, ok := response.(*ab.Article)
		if err != nil || !ok || *value != expected {
			return fail("Bearer slug rejected write changed current state")
		}
		return nil
	}
	duplicate, err := client.GodjConformanceArticleCreate(ctx, &ab.ArticleCreate{Title: "Duplicate slug", Slug: ab.NewOptNilString(expected.Slug.Value)})
	conflict, ok := duplicate.(*ab.GodjConformanceArticleCreateBadRequest)
	if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || len(conflict.Response.Errors) != 1 || conflict.Response.Errors[0].Field != "slug" || conflict.Response.Errors[0].Code != "unique" {
		return fail("Bearer slug unique rejection")
	}
	for _, text := range []string{"bad/slug", "bad slug", "e\u0301", strings.Repeat("한", 51)} {
		response, err := client.GodjConformanceArticlePartialUpdate(ctx, &ab.ArticlePatch{Slug: ab.NewOptNilString(text)}, ab.GodjConformanceArticlePartialUpdateParams{ID: id})
		bad, ok := response.(*ab.GodjConformanceArticlePartialUpdateBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || len(bad.Response.Errors) == 0 || bad.Response.Errors[0].Field != "slug" {
			return fail("Bearer slug grammar/length rejection")
		}
		if err := read(); err != nil {
			return err
		}
	}
	patch := func(value ab.OptNilString) error {
		response, err := client.GodjConformanceArticlePartialUpdate(ctx, &ab.ArticlePatch{Slug: value}, ab.GodjConformanceArticlePartialUpdateParams{ID: id})
		got, ok := response.(*ab.Article)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *got != expected {
			return fail("Bearer slug value/null/omission")
		}
		return nil
	}
	if err := patch(ab.OptNilString{}); err != nil {
		return err
	}
	replaced, err := client.GodjConformanceArticleUpdate(ctx, &ab.ArticleReplace{Title: expected.Title}, ab.GodjConformanceArticleUpdateParams{ID: id})
	got, ok := replaced.(*ab.Article)
	expected.Published = false
	if err != nil || !ok || *got != expected {
		return fail("Bearer slug PUT omission")
	}
	clear := ab.OptNilString{}
	clear.SetToNull()
	expected.Slug.SetToNull()
	if err := patch(clear); err != nil {
		return err
	}
	expected.Slug = ab.NewNilString("")
	if err := patch(ab.NewOptNilString("")); err != nil {
		return err
	}
	expected.Slug = ab.NewNilString("Client_읽기-주소")
	if err := patch(ab.NewOptNilString("  Client_읽기-주소  ")); err != nil {
		return err
	}
	return read()
}

func checkArticleSessionSlug(ctx context.Context, client *as.Client, transport *observedTransport, state *sessionState) error {
	created, err := client.GodjConformanceArticleCreate(ctx, &as.ArticleCreate{Title: "Retained Slug Article", Published: as.NewOptBool(true), Slug: as.NewOptNilString(strings.Repeat(" ", 70) + "Client_읽기-주소" + "  ")})
	header, ok := created.(*as.ArticleHeaders)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || header.Response.Slug.Null || header.Response.Slug.Value != "Client_읽기-주소" {
		return fail("Session slug create after normalization")
	}
	expected := header.Response
	id := expected.ID
	read := func() error {
		response, err := client.GodjConformanceArticleDetail(ctx, as.GodjConformanceArticleDetailParams{ID: id})
		value, ok := response.(*as.GodjConformanceArticleDetailOKHeaders)
		if err != nil || !ok || value.Response != expected || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
			return fail("Session slug rejected write changed current state")
		}
		return nil
	}
	duplicate, err := client.GodjConformanceArticleCreate(ctx, &as.ArticleCreate{Title: "Duplicate slug", Slug: as.NewOptNilString(expected.Slug.Value)})
	conflict, ok := duplicate.(*as.GodjConformanceArticleCreateBadRequest)
	if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || len(conflict.Errors) != 1 || conflict.Errors[0].Field != "slug" || conflict.Errors[0].Code != "unique" {
		return fail("Session slug unique rejection")
	}
	for _, text := range []string{"bad/slug", "bad slug", "e\u0301", strings.Repeat("한", 51)} {
		response, err := client.GodjConformanceArticlePartialUpdate(ctx, &as.ArticlePatch{Slug: as.NewOptNilString(text)}, as.GodjConformanceArticlePartialUpdateParams{ID: id})
		bad, ok := response.(*as.GodjConformanceArticlePartialUpdateBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || len(bad.Errors) == 0 || bad.Errors[0].Field != "slug" {
			return fail("Session slug grammar/length rejection")
		}
		if err := read(); err != nil {
			return err
		}
	}
	patch := func(value as.OptNilString) error {
		response, err := client.GodjConformanceArticlePartialUpdate(ctx, &as.ArticlePatch{Slug: value}, as.GodjConformanceArticlePartialUpdateParams{ID: id})
		got, ok := response.(*as.Article)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *got != expected {
			return fail("Session slug value/null/omission")
		}
		return nil
	}
	if err := patch(as.OptNilString{}); err != nil {
		return err
	}
	replaced, err := client.GodjConformanceArticleUpdate(ctx, &as.ArticleReplace{Title: expected.Title}, as.GodjConformanceArticleUpdateParams{ID: id})
	got, ok := replaced.(*as.Article)
	expected.Published = false
	if err != nil || !ok || *got != expected {
		return fail("Session slug PUT omission")
	}
	clear := as.OptNilString{}
	clear.SetToNull()
	expected.Slug.SetToNull()
	if err := patch(clear); err != nil {
		return err
	}
	expected.Slug = as.NewNilString("")
	if err := patch(as.NewOptNilString("")); err != nil {
		return err
	}
	expected.Slug = as.NewNilString("Client_읽기-주소")
	if err := patch(as.NewOptNilString("  Client_읽기-주소  ")); err != nil {
		return err
	}
	return read()
}

func checkGeneratedSlugWire(ctx context.Context) error {
	const base = `{"id":1,"title":"wire","published":false,"summary":null`
	clear := ab.OptNilString{}
	clear.SetToNull()
	for _, input := range []ab.OptNilString{{}, clear, ab.NewOptNilString(""), ab.NewOptNilString("  Mixed_주소  "), ab.NewOptNilString("old invalid/slug")} {
		wire, output := `{}`, "null"
		if input.Set {
			if !input.Null {
				value, _ := json.Marshal(input.Value)
				output = string(value)
			}
			wire = `{"slug":` + output + `}`
		}
		calls := 0
		client, err := mockArticleClient(base+`,"slug":`+output+`}`, http.MethodPatch, wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjConformanceArticlePartialUpdate(ctx, &ab.ArticlePatch{Slug: input}, ab.GodjConformanceArticlePartialUpdateParams{ID: math.MaxInt64})
		row, ok := response.(*ab.Article)
		if err != nil || !ok || calls != 1 || row.Slug.Null != (!input.Set || input.Null) || input.Set && !input.Null && row.Slug.Value != input.Value {
			return fail("slug generated wire rewrote input or validated stored grammar")
		}
	}
	for _, suffix := range []string{`}`, `,"slug":false}`, `,"slug":0}`, `,"slug":[]}`, `,"slug":{}}`, `,"slug":"` + strings.Repeat("x", 51) + `"}`} {
		calls := 0
		client, err := mockArticleClient(base+suffix, http.MethodPatch, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.GodjConformanceArticlePartialUpdate(ctx, &ab.ArticlePatch{}, ab.GodjConformanceArticlePartialUpdateParams{ID: math.MaxInt64}); err == nil || calls != 1 {
			return fail("slug decoder admitted missing, wrong-type or oversized output")
		}
	}
	return nil
}
