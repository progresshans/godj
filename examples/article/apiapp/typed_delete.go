package apiapp

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) deleteEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	empty := output.NoContent()
	id := endpoint.Resolve(endpoint.PathInt64("id"), func(_ *web.Request, _ auth.Principal, id int64) (int64, error) {
		if id <= 0 {
			return 0, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
		}
		return id, nil
	})
	return endpoint.New(authentication, endpoint.Config[int64, struct{}]{
		Route: web.Route{Name: Namespace + ":article-delete", Method: http.MethodDelete, Path: DetailPath}, Summary: "Delete an Article",
		Admission: endpoint.All(articleapp.ArticleDeletePermission), Input: id, Output: empty,
		Success: []endpoint.Status{{Code: 204, Description: "The Article was deleted. The response has no body or Content-Type."}},
		Errors: []endpoint.Status{
			{Code: 400, Description: "The deletion was rejected by application validation."},
			{Code: 404, Description: "The Article or requested page does not exist, or its identifier is invalid."},
		},
		Handle: func(request *web.Request, _ auth.Principal, id int64) (output.Prepared[struct{}], error) {
			// The fixed representation can be prepared before opening a transaction.
			// Delete owns callback, hook and commit confirmation; an error discards it.
			prepared, err := empty.Prepare(request.Context(), http.StatusNoContent, struct{}{})
			if err != nil {
				return output.Prepared[struct{}]{}, err
			}
			if _, err := a.repository.Delete(request.Context(), id); err != nil {
				return output.Prepared[struct{}]{}, typedArticleFailure(err)
			}
			return prepared, nil
		},
	})
}
