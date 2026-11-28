package helpdesk

import (
	"context"
	"errors"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type modelResources struct {
	category crud.Resource[models.Category]
	ticket   crud.Resource[models.Ticket]
	label    crud.Resource[models.Label]
}

func (a *Application) initResources() error {
	var err error
	a.resources.category, err = crud.New(models.CategoryObjects, crud.Config{ReadOnly: true})
	if err != nil {
		return err
	}
	a.resources.ticket, err = crud.New(models.TicketObjects, crud.Config{Fields: []string{
		"subject", "details", "closed", "priority", "resolution", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload", "external_url", "labels",
	}})
	if err != nil {
		return err
	}
	a.resources.ticket = a.resources.ticket.Filter(a.relations.ModelsTicket.Category.ID.Exact(a.categoryID))
	a.resources.label, err = crud.New(models.LabelObjects, crud.Config{Fields: []string{"name"}})
	if err != nil {
		return err
	}
	a.resources.label = a.resources.label.Filter(a.relations.ModelsLabel.Category.ID.Exact(a.categoryID))
	return nil
}

// Input has already passed its selected HTML/JSON cleaning policy. This hook
// checks the admitted actor, fresh category/object and complete collection
// choices in the write session before the common CRUD kernel issues DML.
func (a *Application) ticketSaveOptions(actor auth.Principal, input orm.ModelInput[models.Ticket], creating, form bool) crud.SaveOptions[models.Ticket] {
	options := crud.SaveOptions[models.Ticket]{}
	if creating {
		options.ServerValues = map[string]query.Value{"category": query.Integer(a.categoryID)}
	}
	if form {
		options.Unique = crud.UniqueAll
	}
	submitted, selected := input.Collection("labels")
	var validated []int64
	options.Before = func(ctx context.Context, session db.Session, current, candidate *models.Ticket) error {
		permission := ChangeTicket
		if creating {
			permission = AddTicket
		}
		if err := ticketWritePermission(ctx, actor, permission); err != nil {
			return err
		}
		if current == nil {
			_, found, err := a.resources.category.Get(ctx, session, a.categoryID)
			if err != nil {
				return err
			}
			if !found {
				return admin.ErrObjectNotFound
			}
		} else {
			if current.CategoryID != a.categoryID {
				return admin.ErrObjectNotFound
			}
			if form {
				preserveTicketFormJSON(*current, candidate)
			}
			preserveTicketNumericValues(*current, candidate)
		}
		if candidate.CategoryID != a.categoryID {
			return errors.New("helpdesk: candidate changed its category scope")
		}
		if selected {
			if _, ok := session.(db.RelationSession); !ok {
				return errors.New("helpdesk: collection requires a relation session")
			}
			var err error
			validated, err = a.validateTicketLabelKeys(ctx, session, submitted)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if selected {
		options.Collections = []crud.CollectionSaver[models.Ticket]{{Field: "labels", Save: func(ctx context.Context, session db.Session, owner models.Ticket, keys []int64) (bool, error) {
			if !slices.Equal(keys, submitted) {
				return false, errors.New("helpdesk: prepared collection changed after validation")
			}
			relations, ok := session.(db.RelationSession)
			if !ok {
				return false, errors.New("helpdesk: collection lost its relation session")
			}
			return a.setTicketLabelKeys(ctx, relations, owner, validated)
		}}}
	}
	return options
}

func resourceError(err error) error {
	if err == crud.ErrNotFound {
		return admin.ErrObjectNotFound
	}
	return writeRejection(err)
}
