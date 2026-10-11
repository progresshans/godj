package helpdesk

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) labelUpdateEndpoint(authentication api.Authentication, mode serializers.Mode) (endpoint.Endpoint, error) {
	name, method, summary, component := "helpdesk:label-update", http.MethodPut, "Update a category label", "LabelUpdate"
	if mode == serializers.ModePartial {
		name, method, summary, component = "helpdesk:label-patch", http.MethodPatch, "Partially update a category label", "LabelPatch"
	}
	id := endpoint.Resolve(endpoint.PathInt64("id"), func(_ *web.Request, _ auth.Principal, id int64) (int64, error) {
		if id <= 0 {
			return 0, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
		}
		return id, nil
	})
	// Only the positive-key check precedes parsing here. Missing/scoped target
	// lookup remains inside updateLabel, after full body validation.
	return endpoint.New(authentication, endpoint.Config[endpoint.Pair[int64, labelAPIInput], models.Label]{
		Route: web.Route{Name: name, Method: method, Path: "/api/labels/<int64:id>/"}, Summary: summary, Description: labelWritePolicy,
		Admission: endpoint.All(ChangeLabel), Input: endpoint.Sequence(id, endpoint.JSONBody(component, a.labelInput, mode, "")), Output: a.responses.label,
		Success: []endpoint.Status{{Code: 200, Description: "The saved label."}},
		Errors: []endpoint.Status{
			{Code: 400, Description: "Invalid input or duplicate name in this category. Preflight duplicates use __all__/unique_together; a concurrent native conflict uses __all__/unique."},
			{Code: 404, Description: "The label or assigned category does not exist in the selected scope."},
			{Code: 413, Description: "The JSON body exceeds 4096 bytes."},
			{Code: 415, Description: "The body is not application/json."},
		},
		Handle: func(request *web.Request, _ auth.Principal, value endpoint.Pair[int64, labelAPIInput]) (output.Prepared[models.Label], error) {
			patch := models.LabelPatch{}
			if name, present := value.Second.name.Get(); present {
				patch = patch.WithName(name)
			}
			result, err := a.updateLabel(request.Context(), value.First, patch)
			if err != nil {
				return output.Prepared[models.Label]{}, typedEndpointFailure(err)
			}
			return result.response, nil
		},
	})
}
