package helpdesk

import (
	"context"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/jsonvalue"
)

// The Admin candidate retains field selection, clean changes and collection
// intent. Current admission/row/category, complete write uniqueness, relation
// endpoints and publishable output all remain in the original write scope.
func (a *Application) saveTicketForm(ctx context.Context, principal auth.Principal, id int64, bound formmodel.BoundForm, inlines admin.InlineSubmission) (ticketRecord, []string, error) {
	permission := ChangeTicket
	if id == 0 {
		permission = AddTicket
	}
	result, err := writeApplicationRelations(ctx, a, principal, requireAccess(permission, ViewLabel), func(work context.Context, session db.RelationSession, principal auth.Principal) (updatedTicket, error) {
		inline, present, err := a.bindAdminReports(work, session, principal, id, inlines)
		if err != nil {
			return updatedTicket{}, err
		}
		saved, changed, err := a.saveTicketFormInSession(work, session, principal, id, bound)
		if err != nil {
			return updatedTicket{}, err
		}
		if present {
			childChanged, err := a.saveAdminReports(work, session, principal, saved.Ticket, inline)
			if err != nil {
				return updatedTicket{}, err
			}
			if childChanged {
				changed = append(changed, reportInlinePrefix)
			}
		}
		if a.auditEnabled(a.adminAudit) && (id == 0 || len(changed) > 0) {
			action := admin.ActionChange
			if id == 0 {
				action = admin.ActionAdd
			}
			event, err := admin.PrepareEvent(principal.ID(), "helpdesk.ticket", saved.ID, action, changed, saved.Subject)
			if err != nil {
				return updatedTicket{}, err
			}
			if err := a.appendAuditIn(work, session, principal, event, a.adminAudit); err != nil {
				return updatedTicket{}, err
			}
		}
		return updatedTicket{value: saved, changed: changed}, nil
	})
	return result.value, result.changed, err
}

// saveTicketFormInSession borrows the caller's transaction. A returned error
// must leave the outer callback; no row or audit may be published before commit.
func (a *Application) saveTicketFormInSession(ctx context.Context, session db.RelationSession, principal auth.Principal, id int64, bound formmodel.BoundForm) (ticketRecord, []string, error) {
	permission := ChangeTicket
	if id == 0 {
		permission = AddTicket
	}
	if err := ticketWritePermission(ctx, principal, permission); err != nil {
		return ticketRecord{}, nil, err
	}
	input, err := formmodel.PrepareInput(models.TicketObjects, bound)
	if err != nil {
		return ticketRecord{}, nil, err
	}
	options := a.ticketSaveOptions(principal, input, id == 0, true)
	var mutation crud.Mutation[models.Ticket]
	if id == 0 {
		mutation, err = a.resources.ticket.CreateInSession(ctx, session, input, options)
	} else {
		mutation, err = a.resources.ticket.UpdateInSession(ctx, session, id, input, options)
	}
	if err != nil {
		return ticketRecord{}, nil, operationError(ctx, resourceError(err))
	}
	changed := mutation.ChangedFields
	var current models.Ticket
	if mutation.Before != nil {
		current = *mutation.Before
	} else {
		// Add audit uses changes from the unsaved default state. Insertion still
		// writes every scalar; excluded defaults require no manual field mapping.
		empty, err := models.TicketObjects.Input(nil)
		if err != nil {
			return ticketRecord{}, nil, err
		}
		initial, err := a.prepareNewTicket(empty)
		if err != nil {
			return ticketRecord{}, nil, err
		}
		current, err = initial.Model()
		if err != nil {
			return ticketRecord{}, nil, err
		}
		before, err := models.TicketObjects.ModelValues(current)
		if err != nil {
			return ticketRecord{}, nil, err
		}
		after, err := models.TicketObjects.ModelValues(mutation.Value)
		if err != nil {
			return ticketRecord{}, nil, err
		}
		changed = nil
		for _, field := range bound.Model().Fields {
			if !field.PrimaryKey && !ticketValuesEqual(before[field.Name], after[field.Name]) {
				changed = append(changed, field.Name)
			}
		}
		if slices.Contains(mutation.ChangedFields, "labels") {
			changed = append(changed, "labels")
		}
	}
	scalarWrite := id == 0
	for _, field := range changed {
		if field != "labels" {
			scalarWrite = true
		}
	}
	saved, err := a.publishableTicket(ctx, session, mutation.Value.ID, scalarWrite)
	if err != nil {
		return ticketRecord{}, nil, operationError(ctx, err)
	}
	changed = payloadDigestChanges(changed, current, saved.Ticket)
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
