package helpdesk

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) labelDeleteEndpoint(authentication api.Authentication) (endpoint.Endpoint, error) {
	empty := output.NoContent()
	id := endpoint.Resolve(endpoint.PathInt64("id"), func(_ *web.Request, _ auth.Principal, id int64) (int64, error) {
		if id <= 0 {
			return 0, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
		}
		return id, nil
	})
	return endpoint.New(authentication, endpoint.Config[int64, struct{}]{
		Route: web.Route{Name: "helpdesk:label-delete", Method: http.MethodDelete, Path: "/api/labels/<int64:id>/"}, Summary: "Delete a category label",
		Description: "Deletes the scoped label and its ticket links in one coordinated transaction. Its category, tickets and links to other labels remain. Authentication, CSRF and permission precede lookup.",
		Admission:   endpoint.All(DeleteLabel), Input: id, Output: empty,
		Success: []endpoint.Status{{Code: 204, Description: "The label and its ticket links were deleted."}},
		Errors: []endpoint.Status{
			{Code: 400, Description: "The deletion was rejected by application validation."},
			{Code: 404, Description: "The label or assigned category does not exist in the selected scope."},
		},
		Handle: func(request *web.Request, _ auth.Principal, id int64) (output.Prepared[struct{}], error) {
			prepared, err := empty.Prepare(request.Context(), http.StatusNoContent, struct{}{})
			if err != nil {
				return output.Prepared[struct{}]{}, err
			}
			if _, err := a.deleteLabel(request.Context(), id); err != nil {
				return output.Prepared[struct{}]{}, typedEndpointFailure(err)
			}
			return prepared, nil
		},
	})
}
