package helpdesk

import (
	"context"
	"errors"
	"fmt"

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

func (a *Application) initTicketLabels() error {
	descriptor := models.TicketLabelDescriptor{}
	metadata := descriptor.Metadata()
	var err error
	a.linkInput, err = serializers.FromModel(metadata, serializers.ModelField{Name: "ticket"}, serializers.ModelField{Name: "label"})
	if err != nil {
		return err
	}
	a.linkOutput, err = serializers.FromModel(metadata, serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "ticket", ReadOnly: true}, serializers.ModelField{Name: "label", ReadOnly: true})
	if err != nil {
		return err
	}
	a.linkEncoder, err = serializers.NewModelEncoder(a.linkOutput, metadata, descriptor.WriteFieldValue)
	return err
}

func (a *Application) registerTicketLabels(builder *admin.Builder) error {
	descriptor := models.TicketLabelDescriptor{}
	metadata := descriptor.Metadata()
	fields := []string{"ticket", "label"}
	form, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "ticket", "label")
	if err != nil {
		return err
	}
	keys := func(values forms.Values) (int64, int64, error) {
		ticket, validTicket := values.Integer("ticket")
		label, validLabel := values.Integer("label")
		if !validTicket || !validLabel {
			return 0, 0, errors.New("helpdesk: invalid ticket label keys")
		}
		return ticket, label, nil
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.TicketLabel]{
		AppLabel: "helpdesk", Slug: "ticket-labels", Model: metadata, FormFields: fields,
		RelatedChoices: []admin.RelatedChoices{
			{Field: "ticket", Permission: ViewTicket, Load: a.reportTicketChoices},
			{Field: "label", Permission: ViewLabel, Load: a.ticketLabelChoices},
		},
		ListFields:  []string{"id", "ticket", "label"},
		Permissions: admin.Permissions{View: ViewTicketLabel, Add: AddTicketLabel, Change: ChangeTicketLabel, Delete: DeleteTicketLabel},
		List:        a.listTicketLabels,
		Get: func(ctx context.Context, id int64) (models.TicketLabel, bool, error) {
			return a.ticketLabel(ctx, a.backend, id)
		},
		Snapshot: func(value models.TicketLabel) (admin.Object, error) {
			return projector.Project(value, value.ID, fmt.Sprintf("Ticket label #%d", value.ID))
		},
		Initial: func(value models.TicketLabel) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, form, value, descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, _ auth.Principal, values forms.Values) (models.TicketLabel, error) {
			ticket, label, err := keys(values)
			if err != nil {
				return models.TicketLabel{}, err
			}
			return a.createTicketLabel(ctx, ticket, label)
		},
		Update: func(ctx context.Context, _ auth.Principal, id int64, values forms.Values) (models.TicketLabel, []string, error) {
			ticket, label, err := keys(values)
			if err != nil {
				return models.TicketLabel{}, nil, err
			}
			return a.updateTicketLabel(ctx, id, models.TicketLabelPatch{}.WithTicketID(ticket).WithLabelID(label))
		},
		Delete: func(ctx context.Context, _ auth.Principal, id int64) (models.TicketLabel, error) {
			return a.deleteTicketLabel(ctx, id)
		},
	})
}

func (a *Application) ticketLabelChoices(ctx context.Context, _ auth.Principal) ([]forms.Choice, error) {
	rows, err := models.LabelObjects.Using(a.backend).Filter(a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	choices := make([]forms.Choice, 0, len(rows))
	for _, row := range rows {
		if row.ID > 0 {
			choices = append(choices, forms.Choice{Value: forms.Integer(row.ID), Label: row.Name})
		}
	}
	return choices, nil
}

// The category is the trusted scope of both endpoints, not a third mutable
// copy on the association. Even malformed cross-category rows written outside
// this application remain invisible through every link read and mutation.
func (a *Application) listTicketLabels(ctx context.Context, request admin.ListRequest) (admin.Page[models.TicketLabel], error) {
	rows := models.TicketLabelObjects.Using(a.backend).Filter(a.relations.ModelsTicketLabel.Ticket.Category().ID.Exact(a.categoryID), a.relations.ModelsTicketLabel.Label.Category().ID.Exact(a.categoryID)).OrderBy(models.TicketLabelFields.ID.Asc())
	total, err := rows.Count(ctx)
	if err != nil {
		return admin.Page[models.TicketLabel]{}, err
	}
	rows, err = rows.Offset(request.Offset)
	if err != nil {
		return admin.Page[models.TicketLabel]{}, err
	}
	rows, err = rows.Limit(request.Limit)
	if err != nil {
		return admin.Page[models.TicketLabel]{}, err
	}
	values, err := rows.All(ctx)
	return admin.Page[models.TicketLabel]{Items: values, Total: total, Offset: request.Offset, Limit: request.Limit}, err
}

func (a *Application) ticketLabel(ctx context.Context, backend db.Queryer, id int64) (models.TicketLabel, bool, error) {
	return models.TicketLabelObjects.Using(backend).Filter(models.TicketLabelFields.ID.Exact(id), a.relations.ModelsTicketLabel.Ticket.Category().ID.Exact(a.categoryID), a.relations.ModelsTicketLabel.Label.Category().ID.Exact(a.categoryID)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
}

func (a *Application) requireTicketLabelEndpoints(ctx context.Context, backend db.Queryer, ticketID, labelID int64) error {
	failures := []validation.Violation{}
	// Each lookup happens inside the same transaction as uniqueness and saving.
	// A failed lookup discards any earlier validation result.
	found := false
	if ticketID > 0 {
		_, present, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(ticketID), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil {
			return err
		}
		found = present
	}
	if !found {
		failures = append(failures, validation.New("ticket", "invalid_choice"))
	}
	found = false
	if labelID > 0 {
		_, present, err := a.label(ctx, backend, labelID)
		if err != nil {
			return err
		}
		found = present
	}
	if !found {
		failures = append(failures, validation.New("label", "invalid_choice"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(failures) > 0 {
		return validation.Reject(validation.NewErrors(failures...), nil)
	}
	return nil
}

func (a *Application) createTicketLabel(ctx context.Context, ticketID, labelID int64) (models.TicketLabel, error) {
	input := models.NewTicketLabelCreate(ticketID, labelID)
	var created models.TicketLabel
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		if err := a.requireTicketLabelEndpoints(ctx, session, ticketID, labelID); err != nil {
			return err
		}
		violations, err := models.TicketLabelObjects.ValidateUniqueCreate(ctx, session, input)
		if err != nil {
			return err
		}
		if !violations.Empty() {
			return validation.Reject(violations, nil)
		}
		created, err = models.TicketLabelObjects.Create(ctx, session, input)
		if err != nil {
			return writeRejection(err)
		}
		created, err = a.publishableTicketLabel(ctx, session, created.ID)
		return err
	})
	if err != nil {
		return models.TicketLabel{}, operationError(ctx, err)
	}
	return created, nil
}

func (a *Application) updateTicketLabel(ctx context.Context, id int64, patch models.TicketLabelPatch) (models.TicketLabel, []string, error) {
	var updated models.TicketLabel
	var changed []string
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := a.ticketLabel(ctx, session, id)
		if err != nil {
			return err
		}
		if !found {
			return admin.ErrObjectNotFound
		}
		mutation := patch.BuildPatch(current)
		if err := mutation.Err(); err != nil && !errors.Is(err, &query.Error{Code: query.CodeEmptyPatch}) {
			return err
		}
		ticketID, labelID := current.TicketID, current.LabelID
		for _, assignment := range mutation.Assignments() {
			value, ok := assignment.Value().Integer()
			if !ok {
				return errors.New("helpdesk: invalid ticket label key")
			}
			switch assignment.Field().Name() {
			case "ticket":
				if value != ticketID {
					changed = append(changed, "ticket")
				}
				ticketID = value
			case "label":
				if value != labelID {
					changed = append(changed, "label")
				}
				labelID = value
			default:
				return errors.New("helpdesk: unexpected ticket label assignment")
			}
		}
		// Omitted and self-assigned keys receive the same scoped check as new keys.
		if err := a.requireTicketLabelEndpoints(ctx, session, ticketID, labelID); err != nil {
			return err
		}
		if len(changed) == 0 {
			updated = current
			return nil
		}
		violations, err := models.TicketLabelObjects.ValidateUniqueUpdate(ctx, session, current, patch)
		if err != nil {
			return err
		}
		if !violations.Empty() {
			return validation.Reject(violations, nil)
		}
		updated, err = models.TicketLabelObjects.Update(ctx, session, current, patch)
		if err != nil {
			return writeRejection(err)
		}
		updated, err = a.publishableTicketLabel(ctx, session, updated.ID)
		return err
	})
	if err != nil {
		return models.TicketLabel{}, nil, operationError(ctx, err)
	}
	return updated, changed, nil
}

func (a *Application) publishableTicketLabel(ctx context.Context, backend db.Queryer, id int64) (models.TicketLabel, error) {
	value, found, err := a.ticketLabel(ctx, backend, id)
	if err != nil {
		return models.TicketLabel{}, err
	}
	if !found {
		return models.TicketLabel{}, admin.ErrObjectNotFound
	}
	encoded, err := a.linkEncoder.Encode(value)
	if err != nil {
		return models.TicketLabel{}, err
	}
	if _, err = serializers.Encode(encoded, serializers.Limits{}); err != nil {
		return models.TicketLabel{}, err
	}
	return value, nil
}

func (a *Application) deleteTicketLabel(ctx context.Context, id int64) (models.TicketLabel, error) {
	var removed models.TicketLabel
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := a.ticketLabel(ctx, session, id)
		if err != nil {
			return err
		}
		if !found {
			return admin.ErrObjectNotFound
		}
		removed = current
		_, err = models.TicketLabelObjects.Delete(ctx, session, &current)
		return err
	})
	if err != nil {
		return models.TicketLabel{}, operationError(ctx, err)
	}
	return removed, nil
}
