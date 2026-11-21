package main

import (
	"context"
	"net/http"
	"slices"

	ab "example.com/godj-openapi-client/articlebearer"
	as "example.com/godj-openapi-client/articlesession"
)

func checkArticleBearerBulk(ctx context.Context, client, readOnly *ab.Client, transport *observedTransport) error {
	count := func() (int64, error) {
		result, err := client.GodjConformanceArticleList(ctx, ab.GodjConformanceArticleListParams{})
		page, ok := result.(*ab.ArticlePage)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK {
			return 0, fail("bearer bulk count")
		}
		return page.Count, nil
	}
	before, err := count()
	if err != nil || before != 1 {
		return fail("bearer bulk baseline")
	}
	for _, test := range []struct {
		rows               []ab.ArticleCreate
		field, code, index string
	}{
		{[]ab.ArticleCreate{{Title: "valid"}, {Title: ""}}, "title", "blank", "1"},
		{[]ab.ArticleCreate{{Title: "first", Slug: ab.NewOptNilString("bulk-duplicate")}, {Title: "second", Slug: ab.NewOptNilString("bulk-duplicate")}}, "slug", "unique", "1"},
		{[]ab.ArticleCreate{{Title: "first"}, {Title: "existing", Slug: ab.NewOptNilString("Client_읽기-주소")}}, "slug", "unique", "1"},
		{[]ab.ArticleCreate{}, "__all__", "invalid_count", ""},
		{make([]ab.ArticleCreate, 41), "__all__", "invalid_count", ""},
	} {
		result, err := client.GodjConformanceArticleBulkCreate(ctx, test.rows)
		failure, ok := result.(*ab.GodjConformanceArticleBulkCreateBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || failure.Response.Code != "validation_error" || len(failure.Response.Errors) != 1 || failure.Response.Errors[0].Field != test.field || failure.Response.Errors[0].Code != test.code {
			return fail("bearer bulk indexed validation")
		}
		if test.index != "" && !slices.ContainsFunc(failure.Response.Errors[0].Params, func(p ab.GoDjAPIErrorErrorsItemParamsItem) bool { return p.Key == "index" && p.Value == test.index }) {
			return fail("bearer bulk original row index")
		}
		if got, err := count(); err != nil || got != before {
			return fail("bearer invalid bulk wrote an earlier row")
		}
	}
	denied, err := readOnly.GodjConformanceArticleBulkCreate(ctx, []ab.ArticleCreate{{Title: ""}})
	if failure, ok := denied.(*ab.GodjConformanceArticleBulkCreateForbidden); err != nil || !ok || failure.Response.Code != "permission_denied" || transport.lastStatus() != http.StatusForbidden {
		return fail("bearer bulk permission before validation")
	}
	inputs := []ab.ArticleCreate{{Title: "  Client bulk article first  ", Published: ab.NewOptBool(true), Summary: ab.NewOptNilString("  bulk summary  "), Slug: ab.NewOptNilString("Client_bulk")}, {Title: "Client bulk article second"}}
	inputs[1].Slug.SetToNull()
	result, err := client.GodjConformanceArticleBulkCreate(ctx, inputs)
	created, ok := result.(*ab.GodjConformanceArticleBulkCreateCreatedApplicationJSON)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || len(*created) != 2 || (*created)[0].ID <= 0 || (*created)[1].ID <= (*created)[0].ID {
		return fail("bearer bulk ordered created identities")
	}
	first, second := (*created)[0], (*created)[1]
	if first.Title != "Client bulk article first" || !first.Published || first.Summary.Null || first.Summary.Value != "bulk summary" || first.Slug.Null || first.Slug.Value != "Client_bulk" || second.Title != "Client bulk article second" || second.Published || !second.Summary.Null || !second.Slug.Null {
		return fail("bearer bulk typed scalar fidelity")
	}
	for _, row := range *created {
		result, err := client.GodjConformanceArticleDetail(ctx, ab.GodjConformanceArticleDetailParams{ID: row.ID})
		stored, ok := result.(*ab.Article)
		if err != nil || !ok || *stored != row {
			return fail("bearer bulk stored response differs")
		}
	}
	if inputs[0].Title != "  Client bulk article first  " || inputs[0].Summary.Value != "  bulk summary  " {
		return fail("bearer bulk input was mutated")
	}
	if got, err := count(); err != nil || got != before+2 {
		return fail("bearer bulk final count")
	}
	return nil
}

func checkArticleSessionBulk(ctx context.Context, client *as.Client, transport *observedTransport, state *sessionState) error {
	before, err := sessionArticleList(ctx, client, transport, state)
	if err != nil || before.Count != 1 {
		return fail("session bulk baseline")
	}
	for _, test := range []struct {
		rows               []as.ArticleCreate
		field, code, index string
	}{
		{[]as.ArticleCreate{{Title: "valid"}, {Title: ""}}, "title", "blank", "1"},
		{[]as.ArticleCreate{{Title: "first", Slug: as.NewOptNilString("bulk-duplicate")}, {Title: "second", Slug: as.NewOptNilString("bulk-duplicate")}}, "slug", "unique", "1"},
		{[]as.ArticleCreate{{Title: "first"}, {Title: "existing", Slug: as.NewOptNilString("Client_읽기-주소")}}, "slug", "unique", "1"},
		{[]as.ArticleCreate{}, "__all__", "invalid_count", ""},
	} {
		result, err := client.GodjConformanceArticleBulkCreate(ctx, test.rows)
		failure, ok := result.(*as.GodjConformanceArticleBulkCreateBadRequest)
		if err != nil || !ok || transport.lastStatus() != http.StatusBadRequest || failure.Code != "validation_error" || len(failure.Errors) != 1 || failure.Errors[0].Field != test.field || failure.Errors[0].Code != test.code {
			return fail("session bulk indexed validation")
		}
		if test.index != "" && !slices.ContainsFunc(failure.Errors[0].Params, func(p as.GoDjAPIErrorErrorsItemParamsItem) bool { return p.Key == "index" && p.Value == test.index }) {
			return fail("session bulk original row index")
		}
		if got, err := sessionArticleList(ctx, client, transport, state); err != nil || got.Count != before.Count {
			return fail("session invalid bulk wrote an earlier row")
		}
	}
	state.setInvalid(true)
	denied, err := client.GodjConformanceArticleBulkCreate(ctx, []as.ArticleCreate{{Title: ""}})
	state.setInvalid(false)
	if failure, ok := denied.(*as.GodjConformanceArticleBulkCreateForbidden); err != nil || !ok || failure.Code != "csrf_rejected" || transport.lastStatus() != http.StatusForbidden {
		return fail("session bulk CSRF before validation")
	}
	inputs := []as.ArticleCreate{{Title: "  Client bulk article first  ", Published: as.NewOptBool(true), Summary: as.NewOptNilString("  bulk summary  "), Slug: as.NewOptNilString("Client_bulk")}, {Title: "Client bulk article second"}}
	inputs[1].Slug.SetToNull()
	result, err := client.GodjConformanceArticleBulkCreate(ctx, inputs)
	created, ok := result.(*as.GodjConformanceArticleBulkCreateCreatedApplicationJSON)
	if err != nil || !ok || transport.lastStatus() != http.StatusCreated || len(*created) != 2 || (*created)[0].ID <= 0 || (*created)[1].ID <= (*created)[0].ID {
		return fail("session bulk ordered created identities")
	}
	first, second := (*created)[0], (*created)[1]
	if first.Title != "Client bulk article first" || !first.Published || first.Summary.Null || first.Summary.Value != "bulk summary" || first.Slug.Null || first.Slug.Value != "Client_bulk" || second.Title != "Client bulk article second" || second.Published || !second.Summary.Null || !second.Slug.Null {
		return fail("session bulk typed scalar fidelity")
	}
	for _, row := range *created {
		result, err := client.GodjConformanceArticleDetail(ctx, as.GodjConformanceArticleDetailParams{ID: row.ID})
		stored, ok := result.(*as.GodjConformanceArticleDetailOKHeaders)
		if err != nil || !ok || stored.Response != row {
			return fail("session bulk stored response differs")
		}
	}
	if inputs[0].Title != "  Client bulk article first  " || inputs[0].Summary.Value != "  bulk summary  " {
		return fail("session bulk input was mutated")
	}
	if got, err := sessionArticleList(ctx, client, transport, state); err != nil || got.Count != before.Count+2 {
		return fail("session bulk final count")
	}
	return nil
}
