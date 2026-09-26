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

func (a *Application) initReports() error {
	metadata := (models.ServiceReportDescriptor{}).Metadata()
	var err error
	a.reportInput, err = serializers.FromModel(metadata, serializers.ModelField{Name: "ticket"}, serializers.ModelField{Name: "summary"}, serializers.ModelField{Name: "completed"})
	if err != nil {
		return err
	}
	a.reportOutput, err = serializers.FromModel(metadata, serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "ticket", ReadOnly: true}, serializers.ModelField{Name: "summary"}, serializers.ModelField{Name: "completed"})
	if err != nil {
		return err
	}
	a.reportEncoder, err = serializers.NewModelEncoder(a.reportOutput, metadata, (models.ServiceReportDescriptor{}).WriteFieldValue)
	return err
}

func (a *Application) registerReports(builder *admin.Builder) error {
	descriptor := models.ServiceReportDescriptor{}
	metadata := descriptor.Metadata()
	fields := []string{"ticket", "summary", "completed"}
	overrides := []formmodel.Override{formmodel.OverrideField("ticket", formmodel.WithLabel("Ticket"))}
	form, err := formmodel.NewSpecForFields(metadata, fields, overrides...)
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "ticket", "summary", "completed")
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.ServiceReport]{
		AppLabel: "helpdesk", Slug: "service-reports", Model: metadata, FormFields: fields, FormOverrides: overrides,
		RelatedChoices: []admin.RelatedChoices{{Field: "ticket", Permission: ViewTicket, Load: a.reportTicketChoices}},
		ListFields:     []string{"id", "ticket", "summary", "completed"}, SearchFields: []string{"summary"},
		Permissions: admin.Permissions{View: ViewServiceReport, Add: AddServiceReport, Change: ChangeServiceReport, Delete: DeleteServiceReport},
		List:        a.listReports,
		Get: func(ctx context.Context, id int64) (models.ServiceReport, bool, error) {
			return a.report(ctx, a.backend, id)
		},
		Snapshot: func(value models.ServiceReport) (admin.Object, error) {
			return projector.Project(value, value.ID, fmt.Sprintf("Report #%d", value.ID))
		},
		Initial: func(value models.ServiceReport) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, form, value, descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, _ auth.Principal, values forms.Values) (models.ServiceReport, error) {
			key, ok := values.Integer("ticket")
			if !ok {
				return models.ServiceReport{}, errors.New("helpdesk: invalid report ticket")
			}
			summary, ok := values.String("summary")
			if !ok {
				return models.ServiceReport{}, errors.New("helpdesk: invalid report summary")
			}
			completed, ok := values.Boolean("completed")
			if !ok {
				return models.ServiceReport{}, errors.New("helpdesk: invalid report completed")
			}
			return a.createReport(ctx, serviceReportInput{ticketID: key, summary: summary, completed: completed})
		},
		Update: func(ctx context.Context, _ auth.Principal, id int64, values forms.Values) (models.ServiceReport, []string, error) {
			key, ok := values.Integer("ticket")
			if !ok {
				return models.ServiceReport{}, nil, errors.New("helpdesk: invalid report ticket")
			}
			summary, ok := values.String("summary")
			if !ok {
				return models.ServiceReport{}, nil, errors.New("helpdesk: invalid report summary")
			}
			completed, ok := values.Boolean("completed")
			if !ok {
				return models.ServiceReport{}, nil, errors.New("helpdesk: invalid report completed")
			}
			return a.updateReport(ctx, id, models.ServiceReportPatch{}.WithTicketID(key).WithSummary(summary).WithCompleted(completed))
		},
		Delete: func(ctx context.Context, _ auth.Principal, id int64) (models.ServiceReport, error) {
			return a.deleteReport(ctx, id)
		},
	})
}

func (a *Application) reportTicketChoices(ctx context.Context, _ auth.Principal) ([]forms.Choice, error) {
	rows, err := models.TicketObjects.Using(a.backend).Filter(a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	choices := make([]forms.Choice, 0, len(rows))
	for _, row := range rows {
		if row.ID > 0 {
			choices = append(choices, forms.Choice{Value: forms.Integer(row.ID), Label: row.Subject})
		}
	}
	return choices, nil
}

func (a *Application) listReports(ctx context.Context, request admin.ListRequest) (admin.Page[models.ServiceReport], error) {
	rows := models.ServiceReportObjects.Using(a.backend).Filter(a.relations.ModelsServiceReport.Ticket.Category().ID.Exact(a.categoryID)).OrderBy(models.ServiceReportFields.ID.Asc())
	if request.Search != "" {
		rows = rows.Filter(models.ServiceReportFields.Summary.IContains(request.Search))
	}
	total, err := rows.Count(ctx)
	if err != nil {
		return admin.Page[models.ServiceReport]{}, err
	}
	rows, err = rows.Offset(request.Offset)
	if err != nil {
		return admin.Page[models.ServiceReport]{}, err
	}
	rows, err = rows.Limit(request.Limit)
	if err != nil {
		return admin.Page[models.ServiceReport]{}, err
	}
	values, err := rows.All(ctx)
	return admin.Page[models.ServiceReport]{Items: values, Total: total, Offset: request.Offset, Limit: request.Limit}, err
}

func (a *Application) report(ctx context.Context, backend db.Queryer, id int64) (models.ServiceReport, bool, error) {
	return models.ServiceReportObjects.Using(backend).Filter(models.ServiceReportFields.ID.Exact(id), a.relations.ModelsServiceReport.Ticket.Category().ID.Exact(a.categoryID)).OrderBy(models.ServiceReportFields.ID.Asc()).First(ctx)
}

func (a *Application) requireReportTicket(ctx context.Context, backend db.Queryer, id int64) error {
	if id > 0 {
		_, found, err := models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
	}
	return validation.Reject(validation.NewErrors(validation.New("ticket", "invalid_choice")), nil)
}

type serviceReportInput struct {
	ticketID  int64
	summary   string
	completed bool
}

func (a *Application) createReport(ctx context.Context, values serviceReportInput) (models.ServiceReport, error) {
	input := models.NewServiceReportCreate(values.ticketID, values.summary).WithCompleted(values.completed)
	var created models.ServiceReport
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		if err := a.requireReportTicket(ctx, session, values.ticketID); err != nil {
			return err
		}
		violations, err := models.ServiceReportObjects.ValidateUniqueCreate(ctx, session, input)
		if err != nil {
			return err
		}
		if !violations.Empty() {
			return validation.Reject(violations, nil)
		}
		created, err = models.ServiceReportObjects.Create(ctx, session, input)
		if err != nil {
			return writeRejection(err)
		}
		created, err = a.publishableReport(ctx, session, created.ID)
		return err
	})
	if err != nil {
		return models.ServiceReport{}, operationError(ctx, err)
	}
	return created, nil
}

func (a *Application) updateReport(ctx context.Context, id int64, patch models.ServiceReportPatch) (models.ServiceReport, []string, error) {
	var updated models.ServiceReport
	var changed []string
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := a.report(ctx, session, id)
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
		descriptor := models.ServiceReportDescriptor{}
		ticketID := current.TicketID
		for _, field := range descriptor.Metadata().Fields {
			for _, assignment := range mutation.Assignments() {
				if assignment.Field().Name() != field.Name {
					continue
				}
				before, valid := descriptor.WriteFieldValue(current, field)
				if !valid {
					return errors.New("helpdesk: unreadable report field")
				}
				if !before.Equal(assignment.Value()) {
					changed = append(changed, field.Name)
				}
				if field.Name == "ticket" {
					var ok bool
					ticketID, ok = assignment.Value().Integer()
					if !ok {
						return errors.New("helpdesk: invalid report key")
					}
				}
			}
		}
		if err := a.requireReportTicket(ctx, session, ticketID); err != nil {
			return err
		}
		if len(changed) == 0 {
			updated = current
			return nil
		}
		violations, err := models.ServiceReportObjects.ValidateUniqueUpdate(ctx, session, current, patch)
		if err != nil {
			return err
		}
		if !violations.Empty() {
			return validation.Reject(violations, nil)
		}
		updated, err = models.ServiceReportObjects.Update(ctx, session, current, patch)
		if err != nil {
			return writeRejection(err)
		}
		updated, err = a.publishableReport(ctx, session, updated.ID)
		return err
	})
	if err != nil {
		return models.ServiceReport{}, nil, operationError(ctx, err)
	}
	return updated, changed, nil
}

func (a *Application) publishableReport(ctx context.Context, backend db.Queryer, id int64) (models.ServiceReport, error) {
	value, found, err := a.report(ctx, backend, id)
	if err != nil {
		return models.ServiceReport{}, err
	}
	if !found {
		return models.ServiceReport{}, admin.ErrObjectNotFound
	}
	encoded, err := a.reportEncoder.Encode(value)
	if err != nil {
		return models.ServiceReport{}, err
	}
	list, err := serializers.NewList(encoded)
	if err != nil {
		return models.ServiceReport{}, err
	}
	if _, err = serializers.Encode(list, serializers.Limits{}); err != nil {
		return models.ServiceReport{}, err
	}
	return value, nil
}

func (a *Application) deleteReport(ctx context.Context, id int64) (models.ServiceReport, error) {
	var removed models.ServiceReport
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := a.report(ctx, session, id)
		if err != nil {
			return err
		}
		if !found {
			return admin.ErrObjectNotFound
		}
		removed = current
		_, err = models.ServiceReportObjects.Delete(ctx, session, &current)
		return err
	})
	if err != nil {
		return models.ServiceReport{}, operationError(ctx, err)
	}
	return removed, nil
}
