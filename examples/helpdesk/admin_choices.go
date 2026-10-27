package helpdesk

import (
	"context"
	"errors"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

// Choice sources keep the canonical source relation and the application's
// category scope through both paged search and submitted-key resolution.
// The final writers still recheck these relations in their write transaction.
func (a *Application) labelChoiceSource(source, field string) (admin.RelatedChoices, error) {
	choices, err := labelModelChoices(source, field)
	if err != nil {
		return nil, err
	}
	return admin.NewRelatedChoices(admin.RelatedChoiceConfig[models.Label]{
		Choices: choices, Permission: ViewLabel,
		Scope: func(ctx context.Context, actor auth.Principal, origin admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[models.Label]) error) error {
			if a.selected == nil {
				return visit(a.backend, []orm.Predicate[models.Label]{models.LabelFields.ID.GreaterThan(0), a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)})
			}
			policy, err := choiceAccess(origin, source)
			if err != nil {
				return err
			}
			_, err = readApplication(ctx, a, actor, allAccess(policy, requireAccess(ViewLabel)), func(_ context.Context, scope db.Queryer, _ auth.Principal) (struct{}, error) {
				return struct{}{}, visit(scope, []orm.Predicate[models.Label]{models.LabelFields.ID.GreaterThan(0), a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)})
			})
			return err
		},
	})
}

func labelModelChoices(source, field string) (formmodel.ModelChoices[models.Label], error) {
	binding, err := project.Bind()
	if err != nil {
		return formmodel.ModelChoices[models.Label]{}, err
	}
	return formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: source}, field, models.LabelObjects, formmodel.ModelChoiceConfig[models.Label]{
		Label: func(row models.Label) (string, error) { return row.Name, nil },
		Search: func(term string) ([]orm.Predicate[models.Label], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Label]{models.LabelFields.Name.IContains(term)}, nil
		},
		Order: []orm.Ordering[models.Label]{models.LabelFields.ID.Asc()},
	})
}

func (a *Application) ticketChoiceSource(source, field string) (admin.RelatedChoices, error) {
	binding, err := project.Bind()
	if err != nil {
		return nil, err
	}
	choices, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: source}, field, models.TicketObjects, formmodel.ModelChoiceConfig[models.Ticket]{
		Label: func(row models.Ticket) (string, error) { return row.Subject, nil },
		Search: func(term string) ([]orm.Predicate[models.Ticket], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Ticket]{models.TicketFields.Subject.IContains(term)}, nil
		},
		Order: []orm.Ordering[models.Ticket]{models.TicketFields.ID.Asc()},
	})
	if err != nil {
		return nil, err
	}
	return admin.NewRelatedChoices(admin.RelatedChoiceConfig[models.Ticket]{
		Choices: choices, Permission: ViewTicket,
		Scope: func(ctx context.Context, actor auth.Principal, origin admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[models.Ticket]) error) error {
			if a.selected == nil {
				return visit(a.backend, []orm.Predicate[models.Ticket]{models.TicketFields.ID.GreaterThan(0), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)})
			}
			policy, err := choiceAccess(origin, source)
			if err != nil {
				return err
			}
			_, err = readApplication(ctx, a, actor, allAccess(policy, requireAccess(ViewTicket)), func(_ context.Context, scope db.Queryer, _ auth.Principal) (struct{}, error) {
				return struct{}{}, visit(scope, []orm.Predicate[models.Ticket]{models.TicketFields.ID.GreaterThan(0), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)})
			})
			return err
		},
	})
}

// Source form authority and target visibility are both current in the choice
// query's native snapshot. A sealed origin selects a known registered policy.
func choiceAccess(origin admin.ChoiceOrigin, source string) (accessPolicy, error) {
	if origin.Source().AppLabel != "helpdesk" || origin.Source().ModelName != source {
		return accessPolicy{}, errors.New("helpdesk: choice source mismatch")
	}
	var view, add, change auth.Permission
	switch source {
	case "ticket":
		view, add, change = ViewTicket, AddTicket, ChangeTicket
	case "label":
		view, add, change = ViewLabel, AddLabel, ChangeLabel
	case "service_report":
		view, add, change = ViewServiceReport, AddServiceReport, ChangeServiceReport
	case "ticket_label":
		view, add, change = ViewTicketLabel, AddTicketLabel, ChangeTicketLabel
	default:
		return accessPolicy{}, errors.New("helpdesk: unknown choice source")
	}
	var policy accessPolicy
	switch origin.Operation() {
	case admin.ChoiceAdd:
		policy = requireAccess(add)
	case admin.ChoiceChange:
		policy = readAccess(view, change)
	case admin.ChoiceCollection:
		if source == "service_report" && origin.Name() == "save" {
			policy = requireAccess(ViewServiceReport, AddServiceReport, ChangeServiceReport, ViewTicket)
		} else {
			return accessPolicy{}, errors.New("helpdesk: unknown choice command")
		}
	case admin.ChoiceCollectionSet:
		if source != "ticket" {
			return accessPolicy{}, errors.New("helpdesk: unknown choice set")
		}
		switch origin.Name() {
		case "create":
			policy = readAccess(ViewTicket, ChangeTicket, AddTicket)
		case "edit":
			policy = readAccess(ViewTicket, ChangeTicket)
		default:
			return accessPolicy{}, errors.New("helpdesk: unknown choice set")
		}
	default:
		return accessPolicy{}, errors.New("helpdesk: unsupported choice origin")
	}
	return policy, nil
}
