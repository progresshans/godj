package helpdesk

import (
	"context"
	"errors"
	"reflect"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// Parent, relation and unique checks share one read snapshot with the current
// selected-account authorization. Publish diagnostics only after cleanup.
func (a *Application) readFormValidation(ctx context.Context, actor auth.Principal, permissions []auth.Permission, check func(context.Context, db.Queryer) (validation.Errors, error)) error {
	if interfaceNil(ctx) {
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
	type result struct {
		failures validation.Errors
		found    bool
	}
	checked, err := readApplication(ctx, a, actor, requireAccess(permissions...), func(work context.Context, reader db.Queryer, _ auth.Principal) (result, error) {
		present, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Exists(work)
		if err != nil || !present {
			return result{}, err
		}
		failures, err := check(work, reader)
		if err == admin.ErrObjectNotFound {
			return result{}, nil
		}
		return result{failures: failures, found: true}, err
	})
	if err != nil {
		return err
	}
	if !checked.found {
		return admin.ErrObjectNotFound
	}
	return validation.Reject(checked.failures, nil)
}

func interfaceNil(reader any) bool {
	if reader == nil {
		return true
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
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
	return a.readFormValidation(ctx, actor, []auth.Permission{permission, ViewLabel}, func(ctx context.Context, reader db.Queryer) (validation.Errors, error) {
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
	return a.readFormValidation(ctx, actor, []auth.Permission{permission, ViewTicket, ViewLabel}, func(ctx context.Context, reader db.Queryer) (validation.Errors, error) {
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

func (a *Application) checkLabelForm(ctx context.Context, actor auth.Principal, id int64, bound formmodel.BoundForm) error {
	permission := AddLabel
	if id != 0 {
		permission = ChangeLabel
	}
	return a.readFormValidation(ctx, actor, []auth.Permission{permission}, func(ctx context.Context, reader db.Queryer) (validation.Errors, error) {
		var current *models.Label
		if id != 0 {
			row, present, err := a.label(ctx, reader, id)
			if err != nil {
				return validation.Errors{}, err
			}
			if !present {
				return validation.Errors{}, admin.ErrObjectNotFound
			}
			current = &row
		}
		return bound.CheckDatabase(ctx, formmodel.DatabaseChecks{
			UniqueFields: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				return models.LabelObjects.ValidateUniqueFields(ctx, reader, values, current)
			},
			Constraints: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				// Category is a fixed application scope, not an editable form
				// field. Supply only this server-owned value for the product's
				// scoped name check; absent/invalid names remain excluded.
				values["category"] = query.Integer(a.categoryID)
				return models.LabelObjects.ValidateUniqueConstraints(ctx, reader, values, current)
			},
		})
	})
}

func (a *Application) checkReportForm(ctx context.Context, actor auth.Principal, id int64, bound formmodel.BoundForm) error {
	permission := AddServiceReport
	if id != 0 {
		permission = ChangeServiceReport
	}
	return a.readFormValidation(ctx, actor, []auth.Permission{permission, ViewTicket}, func(ctx context.Context, reader db.Queryer) (validation.Errors, error) {
		var current *models.ServiceReport
		if id != 0 {
			row, present, err := a.report(ctx, reader, id)
			if err != nil {
				return validation.Errors{}, err
			}
			if !present {
				return validation.Errors{}, admin.ErrObjectNotFound
			}
			current = &row
		}
		var related validation.Errors
		if key, valid := bound.Form().Cleaned().Integer("ticket"); valid {
			present, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(key), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).Exists(ctx)
			if err != nil {
				return validation.Errors{}, err
			}
			if !present {
				related = validation.NewErrors(validation.New("ticket", "invalid_choice"))
			}
		}
		checked, err := bound.WithErrors(related)
		if err != nil {
			return validation.Errors{}, err
		}
		unique, err := checkFormUnique(ctx, reader, models.ServiceReportObjects, current, checked)
		if err != nil {
			return validation.Errors{}, err
		}
		return validation.Join(related, unique), nil
	})
}
