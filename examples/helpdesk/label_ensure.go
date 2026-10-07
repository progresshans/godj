package helpdesk

import (
	"context"
	"errors"
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type appendLabelAudit func(context.Context, db.Session, admin.PreparedEvent) error

type ensuredLabel struct {
	label    models.Label
	created  bool
	response web.Response
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
	encoded, err := a.labelEncoder.Encode(value)
	if err != nil {
		return ensuredLabel{}, err
	}
	object, err := serializers.NewObject(serializers.MemberOf("label", encoded), serializers.MemberOf("created", serializers.Boolean(created)))
	if err != nil {
		return ensuredLabel{}, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response, err := api.JSON(status, object.Value())
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

func (a *Application) apiLabelEnsure(appendAudit appendLabelAudit) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if request.HTTP().URL.RawQuery != "" {
			return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "invalid")))
		}
		values, response, handled, err := bindTypedInput(request, a.labelInput, serializers.ModeFull)
		if handled || err != nil {
			return response, err
		}
		name, _ := values.name.Get()
		result, err := a.ensureLabel(request.Context(), actor, name, appendAudit)
		if err != nil {
			return objectFailure(err)
		}
		return result.response, nil
	}
}
