package helpdesk

import (
	"context"

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
	binding, err := project.Bind()
	if err != nil {
		return nil, err
	}
	choices, err := formmodel.NewRelationChoices(binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: source}, field, models.LabelObjects, formmodel.ModelChoiceConfig[models.Label]{
		Label: func(row models.Label) (string, error) { return row.Name, nil },
		Search: func(term string) ([]orm.Predicate[models.Label], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Label]{models.LabelFields.Name.IContains(term)}, nil
		},
		Order: []orm.Ordering[models.Label]{models.LabelFields.ID.Asc()},
	})
	if err != nil {
		return nil, err
	}
	return admin.NewRelatedChoices(admin.RelatedChoiceConfig[models.Label]{
		Choices: choices, Permission: ViewLabel,
		Scope: func(_ context.Context, _ auth.Principal, _ admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[models.Label]) error) error {
			return visit(a.backend, []orm.Predicate[models.Label]{models.LabelFields.ID.GreaterThan(0), a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)})
		},
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
		Scope: func(_ context.Context, _ auth.Principal, _ admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[models.Ticket]) error) error {
			return visit(a.backend, []orm.Predicate[models.Ticket]{models.TicketFields.ID.GreaterThan(0), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)})
		},
	})
}
