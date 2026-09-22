package helpdesk

import (
	"context"
	"errors"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

func (a *Application) initLabels() error {
	descriptor := models.LabelDescriptor{}
	metadata := descriptor.Metadata()
	var err error
	a.labelInput, err = serializers.FromModel(metadata, serializers.ModelField{Name: "name"})
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
	fields := []string{"name"}
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
		Permissions: admin.Permissions{View: ViewLabel, Add: AddLabel, Change: ChangeLabel, Delete: DeleteLabel},
		List:        a.listLabels,
		Get:         func(ctx context.Context, id int64) (models.Label, bool, error) { return a.label(ctx, a.backend, id) },
		Snapshot:    func(value models.Label) (admin.Object, error) { return projector.Project(value, value.ID, value.Name) },
		Initial: func(value models.Label) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, form, value, descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, _ auth.Principal, values forms.Values) (models.Label, error) {
			name, ok := values.String("name")
			if !ok {
				return models.Label{}, errors.New("helpdesk: invalid label name")
			}
			return a.createLabel(ctx, name)
		},
		Update: func(ctx context.Context, _ auth.Principal, id int64, values forms.Values) (models.Label, []string, error) {
			name, ok := values.String("name")
			if !ok {
				return models.Label{}, nil, errors.New("helpdesk: invalid label name")
			}
			return a.updateLabel(ctx, id, models.LabelPatch{}.WithName(name))
		},
		Delete: func(ctx context.Context, _ auth.Principal, id int64) (models.Label, error) {
			return a.deleteLabel(ctx, id)
		},
	})
}

func (a *Application) listLabels(ctx context.Context, request admin.ListRequest) (admin.Page[models.Label], error) {
	rows := models.LabelObjects.Using(a.backend).Filter(a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)).OrderBy(models.LabelFields.ID.Asc())
	if request.Search != "" {
		rows = rows.Filter(models.LabelFields.Name.IContains(request.Search))
	}
	total, err := rows.Count(ctx)
	if err != nil {
		return admin.Page[models.Label]{}, err
	}
	rows, err = rows.Offset(request.Offset)
	if err != nil {
		return admin.Page[models.Label]{}, err
	}
	rows, err = rows.Limit(request.Limit)
	if err != nil {
		return admin.Page[models.Label]{}, err
	}
	values, err := rows.All(ctx)
	return admin.Page[models.Label]{Items: values, Total: total, Offset: request.Offset, Limit: request.Limit}, err
}

func (a *Application) label(ctx context.Context, backend db.Queryer, id int64) (models.Label, bool, error) {
	return models.LabelObjects.Using(backend).Filter(models.LabelFields.ID.Exact(id), a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
}

func (a *Application) createLabel(ctx context.Context, name string) (models.Label, error) {
	input := models.NewLabelCreate(name, a.categoryID)
	var created models.Label
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		_, found, err := models.CategoryObjects.Using(session).Filter(models.CategoryFields.ID.Exact(a.categoryID)).OrderBy(models.CategoryFields.ID.Asc()).First(ctx)
		if err != nil {
			return err
		}
		if !found {
			return admin.ErrObjectNotFound
		}
		violations, err := models.LabelObjects.ValidateUniqueCreate(ctx, session, input)
		if err != nil {
			return err
		}
		if !violations.Empty() {
			return validation.Reject(violations, nil)
		}
		created, err = models.LabelObjects.Create(ctx, session, input)
		if err != nil {
			return writeRejection(err)
		}
		created, err = a.publishableLabel(ctx, session, created.ID)
		return err
	})
	if err != nil {
		return models.Label{}, operationError(ctx, err)
	}
	return created, nil
}

func (a *Application) updateLabel(ctx context.Context, id int64, patch models.LabelPatch) (models.Label, []string, error) {
	var updated models.Label
	var changed []string
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := a.label(ctx, session, id)
		if err != nil {
			return err
		}
		if !found {
			return admin.ErrObjectNotFound
		}
		mutation := patch.BuildPatch(current)
		if err := mutation.Err(); err != nil {
			if errors.Is(err, &query.Error{Code: query.CodeEmptyPatch}) {
				updated = current
				return nil
			}
			return err
		}
		for _, assignment := range mutation.Assignments() {
			if assignment.Field().Name() != "name" {
				return errors.New("helpdesk: label category is assigned by the server")
			}
			name, ok := assignment.Value().String()
			if !ok {
				return errors.New("helpdesk: invalid label name")
			}
			if name != current.Name {
				changed = append(changed, "name")
			}
		}
		if len(changed) == 0 {
			updated = current
			return nil
		}
		violations, err := models.LabelObjects.ValidateUniqueUpdate(ctx, session, current, patch)
		if err != nil {
			return err
		}
		if !violations.Empty() {
			return validation.Reject(violations, nil)
		}
		updated, err = models.LabelObjects.Update(ctx, session, current, patch)
		if err != nil {
			return writeRejection(err)
		}
		updated, err = a.publishableLabel(ctx, session, updated.ID)
		return err
	})
	if err != nil {
		return models.Label{}, nil, operationError(ctx, err)
	}
	return updated, changed, nil
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
	var removed models.Label
	target := models.NewLabelWithID(id)
	scoped := scopedRelationDelete{backend: a.backend, check: func(session db.RelationSession) error {
		current, found, err := a.label(ctx, session, id)
		if err != nil {
			return err
		}
		if !found {
			return admin.ErrObjectNotFound
		}
		removed = current
		return nil
	}}
	if _, err := a.deleters.ModelsLabel.Delete(ctx, scoped, &target); err != nil {
		return models.Label{}, err
	}
	return removed, nil
}
