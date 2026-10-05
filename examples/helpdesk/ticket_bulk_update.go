package helpdesk

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uuid"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// The selected surface owns field cleaning; this candidate retains only its
// explicit patch or bound form. Current objects are always read in the writer.
type ticketBulkChange struct {
	index  int
	id     int64
	patch  models.TicketPatch
	labels []int64
	form   *formmodel.BoundForm
}

type updatedTickets struct {
	records    []ticketRecord
	changedIDs []int64
	response   web.Response
}

func (change ticketBulkChange) prepare(current models.Ticket) (models.Ticket, []int64, error) {
	if change.form != nil {
		prepared, err := formmodel.PrepareInstance(models.TicketObjects, *change.form, &current)
		if err != nil {
			return models.Ticket{}, nil, err
		}
		candidate, err := prepared.Model()
		if err != nil {
			return models.Ticket{}, nil, err
		}
		preserveTicketFormJSON(current, &candidate)
		labels, selected := prepared.Collections().Integers("labels")
		if selected && labels == nil {
			labels = []int64{}
		}
		return candidate, labels, nil
	}
	mutation := change.patch.BuildPatch(current)
	if err := mutation.Err(); err != nil {
		return models.Ticket{}, nil, err
	}
	fields := make(map[string]query.Value)
	for _, assignment := range mutation.Assignments() {
		fields[assignment.Field().Name()] = assignment.Value()
	}
	value, err := models.TicketObjects.ApplyValues(current, fields)
	return value, slices.Clone(change.labels), err
}

func (a *Application) updateTickets(ctx context.Context, actor auth.Principal, changes []ticketBulkChange, appendAudit appendTicketAudit) (updatedTickets, error) {
	if ctx == nil || appendAudit == nil {
		return updatedTickets{}, errors.New("helpdesk: bulk update requires context and transactional audit")
	}
	if err := ticketWritePermission(ctx, actor, ChangeTicket); err != nil {
		return updatedTickets{}, err
	}
	if len(changes) < 1 || len(changes) > ticketBulkMaximum {
		return updatedTickets{}, bulkCountFailure()
	}
	return runApplicationRelationAtomic(ctx, a.backend, "bulk ticket update", func(ctx context.Context, session db.RelationSession) (updatedTickets, error) {
		return a.updateTicketsInSession(ctx, session, actor, changes, appendAudit)
	})
}

// All selected rows must exist in the current category. This application-level
// all-or-nothing rule is stronger than the ORM's valid missing-key/count policy.
// A borrowed success remains provisional until its parent commits.
func (a *Application) updateTicketsInSession(ctx context.Context, session db.RelationSession, actor auth.Principal, changes []ticketBulkChange, appendAudit appendTicketAudit) (updatedTickets, error) {
	if ctx == nil || appendAudit == nil || nilFormReader(session) {
		return updatedTickets{}, errors.New("helpdesk: bulk update requires a live owner and audit")
	}
	if err := ticketWritePermission(ctx, actor, ChangeTicket); err != nil {
		return updatedTickets{}, err
	}
	if len(changes) < 1 || len(changes) > ticketBulkMaximum {
		return updatedTickets{}, bulkCountFailure()
	}
	keys := make([]int64, len(changes))
	seenKeys, seenIndices := make(map[int64]bool), make(map[int]bool)
	for index, change := range changes {
		if change.id <= 0 || seenKeys[change.id] {
			return updatedTickets{}, validation.Reject(bulkRowFailures(change.index, validation.NewErrors(validation.New("id", "invalid_choice"))), nil)
		}
		if change.index < 0 || seenIndices[change.index] {
			return updatedTickets{}, errors.New("helpdesk: invalid bulk change index")
		}
		seenKeys[change.id], seenIndices[change.index], keys[index] = true, true, change.id
	}
	currentRows, err := models.TicketObjects.Using(session).Filter(models.TicketFields.ID.In(keys...), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).All(ctx)
	if err != nil {
		return updatedTickets{}, err
	}
	if len(currentRows) != len(keys) {
		return updatedTickets{}, admin.ErrObjectNotFound
	}
	currentByID := make(map[int64]models.Ticket, len(currentRows))
	for _, value := range currentRows {
		if !seenKeys[value.ID] || value.CategoryID != a.categoryID {
			return updatedTickets{}, errors.New("helpdesk: bulk lookup returned an unselected row")
		}
		if _, repeated := currentByID[value.ID]; repeated {
			return updatedTickets{}, errors.New("helpdesk: bulk lookup repeated a selected row")
		}
		currentByID[value.ID] = value
	}
	type preparedChange struct {
		before, value models.Ticket
		labels        []int64
		fields        []string
	}
	type updateGroup struct {
		fields []string
		values []models.Ticket
	}
	prepared := make([]preparedChange, len(changes))
	groups := make([]updateGroup, 0)
	groupIndex := make(map[string]int)
	references := make(map[uuid.UUID]bool)
	metadata := (models.TicketDescriptor{}).Metadata()
	var failures validation.Errors
	for index, change := range changes {
		current := currentByID[change.id]
		candidate, labels, err := change.prepare(current)
		if err != nil {
			if rejected, ok := validation.Rejected(err); ok {
				failures = validation.Join(failures, bulkRowFailures(change.index, rejected))
				continue
			}
			return updatedTickets{}, err
		}
		before, err := models.TicketObjects.ModelValues(current)
		if err != nil {
			return updatedTickets{}, err
		}
		after, err := models.TicketObjects.ModelValues(candidate)
		if err != nil {
			return updatedTickets{}, err
		}
		for _, name := range []string{"id", "category", "external_payload_digest"} {
			if !before[name].Equal(after[name]) {
				return updatedTickets{}, errors.New("helpdesk: bulk change altered a server-owned field")
			}
		}
		var fields []string
		for _, field := range metadata.Fields {
			if !field.PrimaryKey && !ticketValuesEqual(before[field.Name], after[field.Name]) {
				fields = append(fields, field.Name)
			}
		}
		if labels != nil {
			labels, err = a.validateTicketLabelKeys(ctx, session, labels)
			if err != nil {
				if rejected, ok := validation.Rejected(err); ok {
					failures = validation.Join(failures, bulkRowFailures(change.index, rejected))
					continue
				}
				return updatedTickets{}, err
			}
		}
		if len(fields) != 0 {
			if candidate.ExternalPayload != nil {
				failures = validation.Join(failures, bulkRowFailures(change.index, externalPayloadErrors(*candidate.ExternalPayload)))
			}
			delete(after, "id")
			unique, err := models.TicketObjects.ValidateUniqueFields(ctx, session, after, &current)
			if err != nil {
				return updatedTickets{}, err
			}
			constraints, err := models.TicketObjects.ValidateUniqueConstraints(ctx, session, after, &current)
			if err != nil {
				return updatedTickets{}, err
			}
			if candidate.ExternalReference != nil {
				if references[*candidate.ExternalReference] {
					unique = validation.Join(unique, validation.NewErrors(validation.New("external_reference", validation.CodeUnique)))
				}
				references[*candidate.ExternalReference] = true
			}
			failures = validation.Join(failures, bulkRowFailures(change.index, validation.Join(unique, constraints)))
			mask := strings.Join(fields, "\x00")
			position, found := groupIndex[mask]
			if !found {
				position, groupIndex[mask] = len(groups), len(groups)
				groups = append(groups, updateGroup{fields: slices.Clone(fields)})
			}
			groups[position].values = append(groups[position].values, candidate)
		}
		prepared[index] = preparedChange{before: current, value: candidate, labels: labels, fields: fields}
	}
	if !failures.Empty() {
		return updatedTickets{}, validation.Reject(failures, nil)
	}
	objects, err := project.UsingSession(session)
	if err != nil {
		return updatedTickets{}, err
	}
	for _, group := range groups {
		count, err := objects.ModelsTicket.Filter(a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).BulkUpdate(ctx, group.values, orm.BulkUpdateFieldNames[models.Ticket](group.fields...), orm.BulkUpdateBatchSize[models.Ticket](ticketBulkBatchSize))
		if err != nil {
			return updatedTickets{}, writeRejection(err)
		}
		if count != int64(len(group.values)) {
			return updatedTickets{}, admin.ErrObjectNotFound
		}
	}
	result := updatedTickets{records: make([]ticketRecord, len(prepared))}
	encoded := make([]serializers.Value, len(prepared))
	events := make([]admin.PreparedEvent, 0, len(prepared))
	for index, change := range prepared {
		changed := slices.Clone(change.fields)
		if change.labels != nil {
			updated, err := a.setTicketLabelKeys(ctx, session, change.value, change.labels)
			if err != nil {
				return updatedTickets{}, err
			}
			if updated {
				changed = append(changed, "labels")
			}
		}
		stored, err := a.publishableTicket(ctx, session, change.value.ID, len(change.fields) != 0)
		if err != nil {
			return updatedTickets{}, err
		}
		if change.labels != nil && !slices.Equal(stored.labels, change.labels) {
			return updatedTickets{}, errors.New("helpdesk: stored bulk labels differ from validated selection")
		}
		changed = payloadDigestChanges(changed, change.before, stored.Ticket)
		result.records[index] = stored
		encoded[index], err = a.encoder.Encode(stored)
		if err != nil {
			return updatedTickets{}, err
		}
		if len(changed) != 0 {
			event, err := admin.PrepareEvent(actor.ID(), "helpdesk.ticket", stored.ID, admin.ActionChange, changed, stored.Subject)
			if err != nil {
				return updatedTickets{}, err
			}
			events = append(events, event)
			result.changedIDs = append(result.changedIDs, stored.ID)
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
