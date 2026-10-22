package helpdesk

import (
	"context"
	"errors"
	"github.com/progresshans/godj/api/output"
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

func (a *Application) initLabels() error {
	descriptor := models.LabelDescriptor{}
	metadata := descriptor.Metadata()
	inputSpec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "name"})
	if err != nil {
		return err
	}
	a.labelInput, err = prepareLabelAPIInput(a.resources.label, inputSpec)
	if err != nil {
		return err
	}
	a.labelOutput, err = serializers.FromModel(metadata, serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "name"}, serializers.ModelField{Name: "category", ReadOnly: true})
	if err != nil {
		return err
	}
	a.labelEncoder, err = serializers.NewModelEncoder(a.labelOutput, metadata, descriptor.WriteFieldValue)
	return err
}

func (a *Application) registerLabels(builder *admin.Builder) error {
	descriptor := models.LabelDescriptor{}
	metadata := descriptor.Metadata()
	fields := a.resources.label.Fields()
	form, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "name")
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.Label]{
		AppLabel: "helpdesk", Slug: "labels", Model: metadata, FormFields: fields,
		ListFields: []string{"id", "name"}, SearchFields: []string{"name"},
		Permissions:        admin.Permissions{View: ViewLabel, Add: AddLabel, Change: ChangeLabel, Delete: DeleteLabel},
		CollectionCommands: a.labelEnsureCommands(form),
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[models.Label], error) {
			return a.listLabels(ctx, request)
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (models.Label, bool, error) {
			return a.label(ctx, a.backend, id)
		},
		Snapshot: func(value models.Label) (admin.Object, error) { return projector.Project(value, value.ID, value.Name) },
		Initial: func(value models.Label) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, form, value, descriptor.WriteFieldValue)
		},
		ValidateCreate: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
			return a.checkLabelForm(ctx, actor, 0, bound)
		},
		ValidateChange: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm) error {
			return a.checkLabelForm(ctx, actor, mutation.ID, bound)
		},
		Create: func(ctx context.Context, _ auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (models.Label, error) {
			input, err := formmodel.PrepareInput(models.LabelObjects, bound)
			if err != nil {
				return models.Label{}, err
			}
			return a.createLabel(ctx, input)
		},
		Update: func(ctx context.Context, _ auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (models.Label, []string, error) {
			input, err := formmodel.PrepareInput(models.LabelObjects, bound)
			if err != nil {
				return models.Label{}, nil, err
			}
			result, err := a.updateLabel(ctx, mutation.ID, input)
			return result.value, result.changed, err
		},
		Delete: func(ctx context.Context, _ auth.Principal, mutation admin.Mutation) (models.Label, error) {
			id := mutation.ID
			return a.deleteLabel(ctx, id)
		},
	})
}

func (a *Application) listLabels(ctx context.Context, request admin.ListRequest) (admin.Page[models.Label], error) {
	var filters []orm.Predicate[models.Label]
	if request.Search != "" {
		filters = append(filters, models.LabelFields.Name.IContains(request.Search))
	}
	page, err := a.resources.label.List(ctx, a.backend, crud.ListOptions[models.Label]{Filter: filters, Offset: request.Offset, Limit: request.Limit})
	if err != nil {
		return admin.Page[models.Label]{}, err
	}
	return admin.Page[models.Label]{Items: page.Items, Total: page.Total, Offset: page.Offset, Limit: page.Limit}, nil
}

func (a *Application) label(ctx context.Context, backend db.Queryer, id int64) (models.Label, bool, error) {
	return a.resources.label.Get(ctx, backend, id)
}

func (a *Application) createLabel(ctx context.Context, input orm.ModelInput[models.Label]) (models.Label, error) {
	return runApplicationAtomic(ctx, a.backend, func(work context.Context, session db.Session) (models.Label, error) {
		mutation, err := a.resources.label.CreateInSession(work, session, input, crud.SaveOptions[models.Label]{
			ServerValues: map[string]query.Value{"category": query.Integer(a.categoryID)},
			Before: func(ctx context.Context, session db.Session, _, _ *models.Label) error {
				_, found, err := a.resources.category.Get(ctx, session, a.categoryID)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				return nil
			},
		})
		if err != nil {
			return models.Label{}, resourceError(err)
		}
		return a.publishableLabel(work, session, mutation.Value.ID)
	})
}

type updatedLabel struct {
	value    models.Label
	changed  []string
	response output.Prepared[models.Label]
}

func (a *Application) updateLabel(ctx context.Context, id int64, input orm.ModelInput[models.Label]) (updatedLabel, error) {
	return runApplicationAtomic(ctx, a.backend, func(work context.Context, session db.Session) (updatedLabel, error) {
		mutation, err := a.resources.label.UpdateInSession(work, session, id, input, crud.SaveOptions[models.Label]{})
		if err != nil {
			return updatedLabel{}, resourceError(err)
		}
		updated, changed := mutation.Value, mutation.ChangedFields
		if len(changed) != 0 {
			var found bool
			updated, found, err = a.label(work, session, updated.ID)
			if err != nil {
				return updatedLabel{}, err
			}
			if !found {
				return updatedLabel{}, admin.ErrObjectNotFound
			}
		}
		response, err := a.responses.label.Prepare(work, http.StatusOK, updated)
		if err != nil {
			return updatedLabel{}, err
		}
		return updatedLabel{value: updated, changed: changed, response: response}, nil
	})
}

func (a *Application) publishableLabel(ctx context.Context, backend db.Queryer, id int64) (models.Label, error) {
	value, found, err := a.label(ctx, backend, id)
	if err != nil {
		return models.Label{}, err
	}
	if !found {
		return models.Label{}, admin.ErrObjectNotFound
	}
	encoded, err := a.labelEncoder.Encode(value)
	if err != nil {
		return models.Label{}, err
	}
	list, err := serializers.NewList(encoded)
	if err != nil {
		return models.Label{}, err
	}
	if _, err := serializers.Encode(list, serializers.Limits{}); err != nil {
		return models.Label{}, err
	}
	return value, nil
}

func (a *Application) deleteLabel(ctx context.Context, id int64) (models.Label, error) {
	return runApplicationRelationAtomic(ctx, a.backend, func(work context.Context, session db.RelationSession) (models.Label, error) {
		removed, err := a.resources.label.DeleteInSession(work, session, id, crud.DeleteOptions[models.Label]{Delete: func(ctx context.Context, backend db.Session, value models.Label) (int64, error) {
			relations, ok := backend.(db.RelationSession)
			if !ok {
				return 0, errors.New("helpdesk: deletion lost relation session")
			}
			return a.deleters.ModelsLabel.DeleteInSession(ctx, relations, value)
		}})
		if err != nil {
			return models.Label{}, resourceError(err)
		}
		return removed, nil
	})
}
