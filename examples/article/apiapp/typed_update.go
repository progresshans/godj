package apiapp

import (
	"context"
	"errors"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) typedUpdate(authentication api.Authentication, mode serializers.Mode, bodyDescription, orderDescription string) (endpoint.Endpoint, error) {
	name, method, summary, component := Namespace+":article-update", http.MethodPut, "Update an Article", "ArticleReplace"
	if mode == serializers.ModePartial {
		name, method, summary, component = Namespace+":article-partial-update", http.MethodPatch, "Partially update an Article", "ArticlePatch"
	}
	// DRF's target lookup precedes body I/O. Permission/CSRF remain outside
	// both stages, and the write rechecks current state in its own transaction.
	current := endpoint.Resolve(endpoint.PathInt64("id"), func(request *web.Request, _ auth.Principal, id int64) (articleapp.Article, error) {
		if id <= 0 {
			return articleapp.Article{}, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
		}
		article, found, err := a.repository.Get(request.Context(), id)
		if err != nil {
			return articleapp.Article{}, typedArticleFailure(err)
		}
		if !found {
			return articleapp.Article{}, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
		}
		return article, nil
	})
	body := endpoint.Resolve(endpoint.JSONBody(component, a.body, mode, bodyDescription), func(_ *web.Request, _ auth.Principal, value articleInput) (articleInput, error) {
		if diagnostics := value.textDiagnostics(); !diagnostics.Empty() {
			return articleInput{}, endpoint.Reject(400, api.CodeValidationError, diagnostics)
		}
		return value, nil
	})
	return endpoint.New(authentication, endpoint.Config[endpoint.Pair[articleapp.Article, articleInput], articlemodels.Article]{
		Route: web.Route{Name: name, Method: method, Path: DetailPath}, Summary: summary, Description: orderDescription,
		Admission: endpoint.All(articleapp.ArticleChangePermission), Input: endpoint.Sequence(current, body), Output: a.response,
		Success: []endpoint.Status{{Code: http.StatusOK, Description: "The updated Article."}},
		Errors: []endpoint.Status{
			{Code: 400, Description: "The body cannot be parsed or a field or query parameter is invalid."},
			{Code: 404, Description: "The Article or requested page does not exist, or its identifier is invalid."},
			{Code: 413, Description: "The request body exceeds 4096 bytes."},
			{Code: 415, Description: "The request Content-Type is not supported application/json."},
		},
		Handle: func(request *web.Request, _ auth.Principal, input endpoint.Pair[articleapp.Article, articleInput]) (output.Prepared[articlemodels.Article], error) {
			prepare := func(ctx context.Context, article articleapp.Article, _ []string) (output.Prepared[articlemodels.Article], error) {
				return a.response.Prepare(ctx, http.StatusOK, articleapp.ModelSnapshot(article))
			}
			var prepared output.Prepared[articlemodels.Article]
			var err error
			if mode == serializers.ModePartial {
				prepared, err = articleapp.PatchAndPrepare(request.Context(), a.repository, input.First.ID, input.Second.patch(), prepare)
			} else {
				prepared, err = articleapp.UpdateAndPrepare(request.Context(), a.repository, input.First.ID, input.Second.full(&input.First), prepare)
			}
			if err != nil {
				return output.Prepared[articlemodels.Article]{}, typedArticleFailure(err)
			}
			return prepared, nil
		},
	})
}

func typedArticleFailure(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
		return err
	}
	if diagnostics, rejected := validation.Rejected(err); rejected {
		return endpoint.Reject(400, api.CodeValidationError, diagnostics)
	}
	if missing, expected := err.(*articleapp.Error); expected && missing != nil && missing.Code == articleapp.CodeNotFound && missing.Cause == nil {
		return endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
	}
	if err == articleapp.ErrNotFound {
		return endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
	}
	return err
}
