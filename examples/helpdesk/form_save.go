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
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
)

// The Admin candidate retains field selection, clean changes and collection
// intent. Current admission/row/category, complete write uniqueness, relation
// endpoints and publishable output all remain in the original write scope.
func (a *Application) saveTicketForm(ctx context.Context, principal auth.Principal, id int64, bound formmodel.BoundForm) (ticketRecord, []string, error) {
	var saved ticketRecord
	var changed []string
	err := a.backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		permission := AddTicket
		if id != 0 {
			permission = ChangeTicket
		}
		if err := ticketWritePermission(ctx, principal, permission); err != nil {
			return err
		}
		current := models.Ticket{CategoryID: a.categoryID}
		if id == 0 {
			present, err := models.CategoryObjects.Using(session).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Exists(ctx)
			if err != nil {
				return err
			}
			if !present {
				return admin.ErrObjectNotFound
			}
		} else {
			row, present, err := ticket(ctx, session, id)
			if err != nil {
				return err
			}
			if !present || row.CategoryID != a.categoryID {
				return admin.ErrObjectNotFound
			}
			current = row
		}
		prepared, err := formmodel.PrepareInstance(models.TicketObjects, bound, &current)
		if err != nil {
			return err
		}
		candidate, err := prepared.Model()
		if err != nil {
			return err
		}
		if candidate.CategoryID != a.categoryID {
			return errors.New("helpdesk: form candidate changed its category scope")
		}
		if id != 0 {
			preserveTicketFormJSON(current, &candidate)
		}
		keys, selected := prepared.Collections().Integers("labels")
		if selected {
			keys, err = a.validateTicketLabelKeys(ctx, session, keys)
			if err != nil {
				return err
			}
		}
		before, err := models.TicketObjects.ModelValues(current)
		if err != nil {
			return err
		}
		after, err := models.TicketObjects.ModelValues(candidate)
		if err != nil {
			return err
		}
		metadata := bound.Model()
		for _, field := range metadata.Fields {
			if field.PrimaryKey {
				delete(after, field.Name)
				continue
			}
			if !ticketValuesEqual(before[field.Name], after[field.Name]) {
				changed = append(changed, field.Name)
			}
		}
		scalarWrite := id == 0 || len(changed) != 0
		if scalarWrite {
			var existing *models.Ticket
			if id != 0 {
				existing = &current
			}
			fields, err := models.TicketObjects.ValidateUniqueFields(ctx, session, after, existing)
			if err != nil {
				return err
			}
			constraints, err := models.TicketObjects.ValidateUniqueConstraints(ctx, session, after, existing)
			if err != nil {
				return err
			}
			if failures := validation.Join(fields, constraints); !failures.Empty() {
				return validation.Reject(failures, nil)
			}
		}
		var savers []formmodel.CollectionSaver[models.Ticket]
		if selected {
			savers = append(savers, formmodel.CollectionSaver[models.Ticket]{Field: "labels", Save: func(callbackCtx context.Context, backend db.Session, owner models.Ticket, submitted []int64) error {
				relationSession, ok := backend.(db.RelationSession)
				if !ok {
					return errors.New("helpdesk: form collection lost relation session")
				}
				// Preparation owns the selection. The application has validated and
				// canonicalized it inside this same transaction before any scalar write.
				if !sameTicketLabelKeys(submitted, keys) {
					return errors.New("helpdesk: form collection selection changed")
				}
				updated, err := a.setTicketLabelKeys(callbackCtx, relationSession, owner, keys)
				if err != nil {
					return err
				}
				if updated {
					changed = append(changed, "labels")
				}
				return nil
			}})
		}
		if id == 0 {
			err = prepared.Save(ctx, session, &candidate, savers...)
		} else {
			// Preserve excluded columns and update-only missing-row behavior.
			// A full Save could overwrite a concurrently changed server scope.
			if scalarWrite {
				err = models.TicketObjects.Save(ctx, session, &candidate, orm.UpdateFieldNames[models.Ticket](changed...))
			}
			if err == nil {
				err = prepared.SaveCollections(ctx, session, candidate, savers...)
			}
		}
		if err != nil {
			return writeRejection(err)
		}
		saved, err = a.publishableTicket(ctx, session, candidate.ID)
		return err
	})
	if err != nil {
		return ticketRecord{}, nil, operationError(ctx, err)
	}
	return saved, changed, nil
}

// Preserve the existing Form equivalence rule, including SQL NULL versus a
// stored JSON null. The JSON API still uses its exact-token update path.
func preserveTicketFormJSON(current models.Ticket, candidate *models.Ticket) {
	before, after := jsonvalue.Null(), jsonvalue.Null()
	if current.ExternalPayload != nil {
		before = *current.ExternalPayload
	}
	if candidate.ExternalPayload != nil {
		after = *candidate.ExternalPayload
	}
	if !forms.JSON(before).Equal(forms.JSON(after)) {
		return
	}
	candidate.ExternalPayload = nil
	if current.ExternalPayload != nil {
		document := *current.ExternalPayload
		candidate.ExternalPayload = &document
	}
}

func sameTicketLabelKeys(left, right []int64) bool {
	// Form multiple choices preserve submitted order; storage uses a sorted set.
	if len(left) != len(right) {
		return false
	}
	seen := make(map[int64]bool, len(left))
	for _, key := range left {
		seen[key] = true
	}
	for _, key := range right {
		if !seen[key] {
			return false
		}
	}
	return len(seen) == len(right)
}
