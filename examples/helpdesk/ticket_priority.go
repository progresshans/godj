package helpdesk

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

func prioritySelectionError(ids []int64) error {
	if len(ids) < 1 || len(ids) > ticketBulkMaximum {
		return bulkCountFailure()
	}
	seen := make(map[int64]bool, len(ids))
	for index, id := range ids {
		if id <= 0 || seen[id] {
			return validation.Reject(bulkRowFailures(index, validation.NewErrors(validation.New("ids", "invalid_choice"))), nil)
		}
		seen[id] = true
	}
	return nil
}

func (a *Application) raiseTicketPriority(ctx context.Context, actor auth.Principal, ids []int64, appendAudit appendTicketAudit) (updatedTickets, error) {
	if ctx == nil || appendAudit == nil {
		return updatedTickets{}, errors.New("helpdesk: priority raise requires context and transactional audit")
	}
	if err := ticketWritePermission(ctx, actor, ChangeTicket); err != nil {
		return updatedTickets{}, err
	}
	if err := prioritySelectionError(ids); err != nil {
		return updatedTickets{}, err
	}
	keys := slices.Clone(ids)
	return runApplicationRelationAtomic(ctx, a.backend, "ticket priority raise", func(ctx context.Context, session db.RelationSession) (updatedTickets, error) {
		return a.raiseTicketPriorityInSession(ctx, session, actor, keys, appendAudit)
	})
}

func (a *Application) raiseTicketPriorityInSession(ctx context.Context, session db.RelationSession, actor auth.Principal, ids []int64, appendAudit appendTicketAudit) (updatedTickets, error) {
	if ctx == nil || appendAudit == nil || nilFormReader(session) {
		return updatedTickets{}, errors.New("helpdesk: priority raise requires a live owner and audit")
	}
	if err := ticketWritePermission(ctx, actor, ChangeTicket); err != nil {
		return updatedTickets{}, err
	}
	if err := prioritySelectionError(ids); err != nil {
		return updatedTickets{}, err
	}
	objects, err := project.UsingSession(session)
	if err != nil {
		return updatedTickets{}, err
	}
	scope := models.TicketFields.CategoryID.Exact(a.categoryID)
	current, err := models.TicketObjects.Using(session).Filter(scope, models.TicketFields.ID.In(ids...)).All(ctx)
	if err != nil {
		return updatedTickets{}, err
	}
	if len(current) != len(ids) {
		return updatedTickets{}, admin.ErrObjectNotFound
	}
	before := make(map[int64]models.Ticket, len(current))
	var low, normal, unset []int64
	for _, row := range current {
		if _, duplicate := before[row.ID]; duplicate || row.CategoryID != a.categoryID || !slices.Contains(ids, row.ID) {
			return updatedTickets{}, errors.New("helpdesk: priority lookup returned an unselected or repeated row")
		}
		before[row.ID] = row
		switch {
		case row.Priority == nil:
			unset = append(unset, row.ID)
		case *row.Priority == -1:
			low = append(low, row.ID)
		case *row.Priority == 0:
			normal = append(normal, row.ID)
		}
	}
	// The assignment reads the database's current value. The original value and
	// category remain predicates so a changed selection cannot publish a false
	// audit. NULL follows the application's explicit policy in a second statement.
	if len(low)+len(normal) != 0 {
		selection := orm.Or(
			orm.And(models.TicketFields.ID.In(low...), models.TicketFields.Priority.Exact(-1)),
			orm.And(models.TicketFields.ID.In(normal...), models.TicketFields.Priority.Exact(0)),
		)
		count, err := objects.ModelsTicket.Filter(scope, selection).Update(ctx,
			orm.AssignExpression(models.TicketFields.Priority, orm.Add(orm.F(models.TicketFields.Priority), int64(1))))
		if err != nil {
			return updatedTickets{}, writeRejection(err)
		}
		if count != int64(len(low)+len(normal)) {
			return updatedTickets{}, admin.ErrObjectNotFound
		}
	}
	if len(unset) != 0 {
		count, err := objects.ModelsTicket.Filter(scope, models.TicketFields.ID.In(unset...), models.TicketFields.Priority.IsNull(true)).Update(ctx,
			orm.Assign(models.TicketFields.Priority, int64(0)))
		if err != nil {
			return updatedTickets{}, writeRejection(err)
		}
		if count != int64(len(unset)) {
			return updatedTickets{}, admin.ErrObjectNotFound
		}
	}
	result := updatedTickets{records: make([]ticketRecord, len(ids))}
	encoded := make([]serializers.Value, len(ids))
	var events []admin.PreparedEvent
	for index, id := range ids {
		original := before[id]
		expected, changed := raisedTicketPriority(original.Priority)
		stored, err := a.publishableTicket(ctx, session, id, changed)
		if err != nil {
			return updatedTickets{}, err
		}
		if stored.CategoryID != a.categoryID || stored.Priority == nil || *stored.Priority != expected {
			return updatedTickets{}, admin.ErrObjectNotFound
		}
		result.records[index] = stored
		encoded[index], err = a.encoder.Encode(stored)
		if err != nil {
			return updatedTickets{}, err
		}
		if changed {
			fields := payloadDigestChanges([]string{"priority"}, original, stored.Ticket)
			event, err := admin.PrepareEvent(actor.ID(), "helpdesk.ticket", id, admin.ActionChange, fields, stored.Subject)
			if err != nil {
				return updatedTickets{}, err
			}
			events = append(events, event)
			result.changedIDs = append(result.changedIDs, id)
		}
	}
	list, err := serializers.NewList(encoded...)
	if err != nil {
		return updatedTickets{}, err
	}
	result.response, err = api.JSONWithLimits(http.StatusOK, list, serializers.Limits{MaxValues: maximumJSONListValues})
	if err != nil {
		return updatedTickets{}, err
	}
	for _, event := range events {
		if err := appendAudit(ctx, session, event); err != nil {
			return updatedTickets{}, err
		}
	}
	return result, nil
}

func raisedTicketPriority(current *int64) (int64, bool) {
	if current == nil {
		return 0, true
	}
	if *current == -1 || *current == 0 {
		return *current + 1, true
	}
	return *current, false
}
