package helpdesk

import (
	"context"
	"errors"
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

type appendLabelAudit func(context.Context, db.Session, admin.PreparedEvent) error

type ensuredLabel struct {
	label    models.Label
	created  bool
	response output.Prepared[labelEnsureResponse]
}

// ensureLabel shares one parent transaction across lookup/create, output
// preparation and audit. GetOrCreate borrows that parent's savepoint; its
// provisional created result is never exposed before the parent commits.
func (a *Application) ensureLabel(ctx context.Context, actor auth.Principal, name string, appendAudit appendLabelAudit) (ensuredLabel, error) {
	if ctx == nil || appendAudit == nil {
		return ensuredLabel{}, errors.New("helpdesk: label ensure requires context and transactional audit")
	}
	if err := labelEnsurePermission(ctx, actor); err != nil {
		return ensuredLabel{}, err
	}
	return runApplicationAtomic(ctx, a.backend, "label", func(workContext context.Context, session db.Session) (ensuredLabel, error) {
		return a.ensureLabelInSession(workContext, session, actor, name, appendAudit)
	})
}

func labelEnsurePermission(ctx context.Context, actor auth.Principal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// As for ticket writes, this is the immutable principal admitted by the
	// request's authentication and deny overlay, not a new session admission.
	if !actor.Authenticated() || !actor.Has(AddLabel) || !actor.Has(ViewLabel) {
		return admin.NewOperationError(admin.OperationDenied, nil)
	}
	return nil
}

func (a *Application) ensureLabelInSession(ctx context.Context, session db.Session, actor auth.Principal, name string, appendAudit appendLabelAudit) (ensuredLabel, error) {
	if err := labelEnsurePermission(ctx, actor); err != nil {
		return ensuredLabel{}, err
	}
	present, err := models.CategoryObjects.Using(session).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Exists(ctx)
	if err != nil {
		return ensuredLabel{}, err
	}
	if !present {
		return ensuredLabel{}, admin.ErrObjectNotFound
	}
	value, created, err := models.LabelObjects.Using(session).
		Filter(a.relations.ModelsLabel.Category.ID.Exact(a.categoryID), models.LabelFields.Name.Exact(name)).
		GetOrCreate(ctx, models.NewLabelCreate(name, a.categoryID))
	if err != nil {
		return ensuredLabel{}, writeRejection(err)
	}
	value, err = a.publishableLabel(ctx, session, value.ID)
	if err != nil {
		return ensuredLabel{}, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response, err := a.responses.labelEnsure.Prepare(ctx, status, labelEnsureResponse{label: value, created: created})
	if err != nil {
		return ensuredLabel{}, err
	}
	if created {
		event, err := admin.PrepareEvent(actor.ID(), "helpdesk.label", value.ID, admin.ActionAdd, nil, value.Name)
		if err != nil {
			return ensuredLabel{}, err
		}
		if err := appendAudit(ctx, session, event); err != nil {
			return ensuredLabel{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return ensuredLabel{}, err
	}
	return ensuredLabel{label: value, created: created, response: response}, nil
}

func (a *Application) labelEnsureCommands(form forms.Spec) []admin.CollectionCommandConfig {
	if a.adminAudit == nil {
		return nil
	}
	return []admin.CollectionCommandConfig{{
		Name: "ensure", Label: "Find or create label", Permission: AddLabel,
		AdditionalPermissions: []auth.Permission{ViewLabel}, Form: form,
		Run: func(ctx context.Context, actor auth.Principal, values forms.Values) (admin.CollectionCommandResult, error) {
			name, ok := values.String("name")
			if !ok {
				return admin.CollectionCommandResult{}, errors.New("helpdesk: invalid label name")
			}
			result, err := a.ensureLabel(ctx, actor, name, a.adminAudit)
			if err != nil {
				return admin.CollectionCommandResult{}, err
			}
			return admin.CollectionCommandResult{ID: result.label.ID, Created: result.created}, nil
		},
	}}
}

func (a *Application) labelEnsureEndpoint(authentication api.Authentication, appendAudit appendLabelAudit) (endpoint.Endpoint, error) {
	return endpoint.New(authentication, endpoint.Config[labelAPIInput, labelEnsureResponse]{
		Route:       web.Route{Name: "helpdesk:label-ensure", Method: http.MethodPost, Path: "/api/labels/ensure/"},
		Summary:     "Find or create a category label",
		Description: "Authentication, both add and view permissions, and CSRF precede parsing. Accepts the same normalized name as ordinary creation; category and id remain server-owned. Query parameters are rejected. The current assigned category is checked in the parent transaction. An exact (category, name) match returns 200 with created=false; a new label returns 201 with created=true only after the row and its single add audit event commit together. Reuse does not append an audit event. A concurrent native unique conflict is rolled back to a savepoint before one fresh lookup. Output, audit, cancellation and uncertain transaction failures do not publish a result or trigger automatic retries. Ordinary creation continues to reject duplicates.",
		Admission:   endpoint.All(AddLabel, ViewLabel),
		Input:       endpoint.NoQuery(endpoint.JSONBody("LabelCreate", a.labelInput, serializers.ModeFull, "")),
		Output:      a.responses.labelEnsure,
		Success:     []endpoint.Status{{Code: http.StatusOK, Description: "The existing scoped label; created is false."}, {Code: http.StatusCreated, Description: "The newly committed scoped label; created is true."}},
		Errors: []endpoint.Status{
			{Code: http.StatusBadRequest, Description: "Invalid name, body or query parameters. A native unique conflict with no matching visible label uses __all__/unique after confirmed rollback."},
			{Code: http.StatusNotFound, Description: "The label or assigned category does not exist in the selected scope."},
			{Code: http.StatusRequestEntityTooLarge, Description: "The JSON body exceeds 4096 bytes."},
			{Code: http.StatusUnsupportedMediaType, Description: "The body is not application/json."},
		},
		Handle: func(request *web.Request, actor auth.Principal, values labelAPIInput) (output.Prepared[labelEnsureResponse], error) {
			name, _ := values.name.Get()
			result, err := a.ensureLabel(request.Context(), actor, name, appendAudit)
			if err != nil {
				return output.Prepared[labelEnsureResponse]{}, typedEndpointFailure(err)
			}
			return result.response, nil
		},
	})
}
