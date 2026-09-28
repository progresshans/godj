package helpdesk

import (
	"context"
	"errors"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// The application retains the immutable principal admitted by authentication.
// All parent, row, relation and uniqueness reads below use one borrowed read
// snapshot. Diagnostics are published only after its cleanup succeeds.
func (a *Application) readFormValidation(ctx context.Context, actor auth.Principal, permissions []auth.Permission, check func(db.Queryer) (validation.Errors, error)) error {
	if ctx == nil {
		return errors.New("helpdesk: missing validation context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !actor.Authenticated() {
		return errors.New("helpdesk: model validation lacks admitted owner")
	}
	for _, permission := range permissions {
		if !actor.Has(permission) {
			return errors.New("helpdesk: model validation lacks admitted permission")
		}
	}
	var failures validation.Errors
	var callbackErr error
	notFound := false
	calls := 0
	err := a.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || reader == nil {
			callbackErr = errors.New("helpdesk: invalid read snapshot callback")
			return callbackErr
		}
		present, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Exists(ctx)
		if err != nil {
			callbackErr = err
			return err
		}
		if !present {
			notFound = true
			return nil
		}
		failures, callbackErr = check(reader)
		if callbackErr == admin.ErrObjectNotFound {
			notFound = true
			callbackErr = nil
		}
		return callbackErr
	})
	if err = errors.Join(err, callbackErr, ctx.Err()); err != nil {
		return err
	}
	if calls != 1 {
		return errors.New("helpdesk: missing read snapshot callback")
	}
	if notFound {
		return admin.ErrObjectNotFound
	}
	return validation.Reject(failures, nil)
}

func checkFormUnique[M any](ctx context.Context, reader db.Queryer, manager orm.Manager[M], current *M, bound formmodel.BoundForm) (validation.Errors, error) {
	return bound.CheckDatabase(ctx, formmodel.DatabaseChecks{
		UniqueFields: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
			return manager.ValidateUniqueFields(ctx, reader, values, current)
		},
		Constraints: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
			return manager.ValidateUniqueConstraints(ctx, reader, values, current)
		},
	})
}

func (a *Application) checkTicketForm(ctx context.Context, actor auth.Principal, id int64, bound formmodel.BoundForm) error {
	permission := AddTicket
	if id != 0 {
		permission = ChangeTicket
	}
	return a.readFormValidation(ctx, actor, []auth.Permission{permission, ViewLabel}, func(reader db.Queryer) (validation.Errors, error) {
		var current *models.Ticket
		if id != 0 {
			row, present, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
			if err != nil {
				return validation.Errors{}, err
			}
			if !present {
				return validation.Errors{}, admin.ErrObjectNotFound
			}
			current = &row
		}
		return checkFormUnique(ctx, reader, models.TicketObjects, current, bound)
	})
}

func (a *Application) checkTicketLabelForm(ctx context.Context, actor auth.Principal, id int64, bound formmodel.BoundForm) error {
	permission := AddTicketLabel
	if id != 0 {
		permission = ChangeTicketLabel
	}
	return a.readFormValidation(ctx, actor, []auth.Permission{permission, ViewTicket, ViewLabel}, func(reader db.Queryer) (validation.Errors, error) {
		var current *models.TicketLabel
		if id != 0 {
			row, present, err := a.ticketLabel(ctx, reader, id)
			if err != nil {
				return validation.Errors{}, err
			}
			if !present {
				return validation.Errors{}, admin.ErrObjectNotFound
			}
			current = &row
		}
		// Choices may have changed since form projection. Recheck each valid
		// relation independently before a tuple query can observe another scope.
		var failures []validation.Violation
		if key, valid := bound.Form().Cleaned().Integer("ticket"); valid {
			present, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(key), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).Exists(ctx)
			if err != nil {
				return validation.Errors{}, err
			}
			if !present {
				failures = append(failures, validation.New("ticket", "invalid_choice"))
			}
		}
		if key, valid := bound.Form().Cleaned().Integer("label"); valid {
			_, present, err := a.label(ctx, reader, key)
			if err != nil {
				return validation.Errors{}, err
			}
			if !present {
				failures = append(failures, validation.New("label", "invalid_choice"))
			}
		}
		related := validation.NewErrors(failures...)
		checked, err := bound.WithErrors(related)
		if err != nil {
			return validation.Errors{}, err
		}
		unique, err := checkFormUnique(ctx, reader, models.TicketLabelObjects, current, checked)
		if err != nil {
			return validation.Errors{}, err
		}
		return validation.Join(related, unique), nil
	})
}
