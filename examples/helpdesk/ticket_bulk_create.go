package helpdesk

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/uuid"
	"github.com/progresshans/godj/validation"
)

const ticketBulkMaximum = 40
const ticketBulkBatchSize = 20
const ticketBulkBodyBytes = ticketBulkMaximum * maximumJSONBodyBytes

type ticketBulkCandidate struct {
	index  int // original, zero-based API/form row; never a database identity
	value  models.Ticket
	labels []int64
}

type appendTicketAudit func(context.Context, db.Session, admin.PreparedEvent) error

type createdTickets struct {
	records  []ticketRecord
	response output.Prepared[[]ticketRecord]
}

func bulkRowFailures(index int, failures validation.Errors) validation.Errors {
	items := failures.All()
	for position, failure := range items {
		parameters := failure.Params()
		for slot, parameter := range parameters {
			if parameter.Key() == "index" {
				parameters[slot] = validation.NewParam("item_index", parameter.Value())
			}
		}
		items[position] = validation.New(failure.Field(), failure.Code(), append(parameters, validation.NewParam("index", strconv.Itoa(index)))...)
	}
	return validation.NewErrors(items...)
}

func bulkFailureIndex(failure validation.Violation) (int, bool) {
	for _, parameter := range failure.Params() {
		if parameter.Key() == "index" {
			index, err := strconv.Atoi(parameter.Value())
			return index, err == nil && index >= 0
		}
	}
	return 0, false
}

func bulkCountFailure() error {
	return validation.Reject(validation.NewErrors(validation.New(validation.NonField, "invalid_count", validation.NewParam("min", "1"), validation.NewParam("max", strconv.Itoa(ticketBulkMaximum)))), nil)
}

// Candidates have already passed the selected Form/API input policy. The
// application still owns scope, current uniqueness/relations, stored output
// and audit. BulkCreate itself never runs those per-object policies.
func (a *Application) createTickets(ctx context.Context, actor auth.Principal, candidates []ticketBulkCandidate, appendAudit appendTicketAudit) (createdTickets, error) {
	if ctx == nil || appendAudit == nil {
		return createdTickets{}, errors.New("helpdesk: bulk create requires context and transactional audit")
	}
	if err := ticketWritePermission(ctx, actor, AddTicket); err != nil {
		return createdTickets{}, err
	}
	if len(candidates) < 1 || len(candidates) > ticketBulkMaximum {
		return createdTickets{}, bulkCountFailure()
	}
	return runApplicationRelationAtomic(ctx, a.backend, "bulk ticket create", func(ctx context.Context, session db.RelationSession) (createdTickets, error) {
		return a.createTicketsInSession(ctx, session, actor, candidates, appendAudit)
	})
}

// A successful borrowed result remains provisional until the outer owner
// commits. Any error must leave that callback so all rows, links and audit
// entries roll back together. No key or response is published on failure.
func (a *Application) createTicketsInSession(ctx context.Context, session db.RelationSession, actor auth.Principal, candidates []ticketBulkCandidate, appendAudit appendTicketAudit) (createdTickets, error) {
	if ctx == nil || appendAudit == nil {
		return createdTickets{}, errors.New("helpdesk: bulk create requires context and transactional audit")
	}
	if err := ticketWritePermission(ctx, actor, AddTicket); err != nil {
		return createdTickets{}, err
	}
	if len(candidates) < 1 || len(candidates) > ticketBulkMaximum {
		return createdTickets{}, bulkCountFailure()
	}
	present, err := models.CategoryObjects.Using(session).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Exists(ctx)
	if err != nil {
		return createdTickets{}, err
	}
	if !present {
		return createdTickets{}, admin.ErrObjectNotFound
	}
	values := make([]models.Ticket, len(candidates))
	labels := make([][]int64, len(candidates))
	seen := make(map[uuid.UUID]bool)
	indices := make(map[int]bool)
	var failures validation.Errors
	for index, candidate := range candidates {
		if candidate.index < 0 || indices[candidate.index] {
			return createdTickets{}, errors.New("helpdesk: invalid bulk candidate index")
		}
		indices[candidate.index] = true
		value, err := models.TicketObjects.ApplyValues(candidate.value, nil)
		if err != nil {
			return createdTickets{}, err
		}
		_, hasKey := (models.TicketDescriptor{}).PrimaryKey(value)
		if value.ID != 0 || hasKey || value.CategoryID != a.categoryID || value.ExternalPayloadDigest != nil {
			return createdTickets{}, errors.New("helpdesk: bulk candidate changed server-owned fields")
		}
		keys, err := a.validateTicketLabelKeys(ctx, session, candidate.labels)
		if err != nil {
			if diagnostics, rejected := validation.Rejected(err); rejected {
				failures = validation.Join(failures, bulkRowFailures(candidate.index, diagnostics))
				continue
			}
			return createdTickets{}, err
		}
		if value.ExternalPayload != nil {
			failures = validation.Join(failures, bulkRowFailures(candidate.index, externalPayloadErrors(*value.ExternalPayload)))
		}
		fields, err := models.TicketObjects.ModelValues(value)
		if err != nil {
			return createdTickets{}, err
		}
		delete(fields, "id")
		unique, err := models.TicketObjects.ValidateUniqueFields(ctx, session, fields, nil)
		if err != nil {
			return createdTickets{}, err
		}
		constraints, err := models.TicketObjects.ValidateUniqueConstraints(ctx, session, fields, nil)
		if err != nil {
			return createdTickets{}, err
		}
		// This model's nullable UUID is its only non-primary unique field.
		// Check the complete incoming batch before any SQL can assign a key.
		if value.ExternalReference != nil {
			if seen[*value.ExternalReference] {
				unique = validation.Join(unique, validation.NewErrors(validation.New("external_reference", validation.CodeUnique)))
			}
			seen[*value.ExternalReference] = true
		}
		failures = validation.Join(failures, bulkRowFailures(candidate.index, validation.Join(unique, constraints)))
		values[index], labels[index] = value, keys
	}
	if !failures.Empty() {
		return createdTickets{}, validation.Reject(failures, nil)
	}
	objects, err := project.UsingSession(session)
	if err != nil {
		return createdTickets{}, err
	}
	bulk, err := objects.ModelsTicket.BulkCreate(ctx, values, orm.BulkBatchSize[models.Ticket](ticketBulkBatchSize))
	if err != nil {
		return createdTickets{}, writeRejection(err)
	}
	if !bulk.ReturnedKeys || bulk.RowsAffected != int64(len(values)) || len(bulk.Objects) != len(values) {
		return createdTickets{}, errors.New("helpdesk: incomplete bulk ticket result")
	}
	result := createdTickets{records: make([]ticketRecord, len(values))}
	events := make([]admin.PreparedEvent, len(values))
	for index, object := range bulk.Objects {
		stored, err := object.Unwrap()
		if err != nil {
			return createdTickets{}, err
		}
		if _, err := a.setTicketLabelKeys(ctx, session, stored, labels[index]); err != nil {
			return createdTickets{}, err
		}
		storedRecord, err := a.publishableTicket(ctx, session, stored.ID, true)
		if err != nil {
			return createdTickets{}, err
		}
		if !slices.Equal(storedRecord.labels, labels[index]) {
			return createdTickets{}, errors.New("helpdesk: stored bulk label membership differs from the validated selection")
		}
		result.records[index] = storedRecord
		events[index], err = admin.PrepareEvent(actor.ID(), "helpdesk.ticket", stored.ID, admin.ActionAdd, nil, storedRecord.Subject)
		if err != nil {
			return createdTickets{}, err
		}
	}
	// Validate the complete response, after backend JSON normalization and
	// digest refresh, before any caller can report success or commit audit.
	result.response, err = a.responses.ticketBulk.Prepare(ctx, http.StatusCreated, result.records)
	if err != nil {
		return createdTickets{}, err
	}
	for _, event := range events {
		if err := appendAudit(ctx, session, event); err != nil {
			return createdTickets{}, err
		}
	}
	return result, nil
}

func (input ticketInput) model(categoryID int64) models.Ticket {
	return models.Ticket{Subject: input.subject, Details: input.details, Closed: input.closed, CategoryID: categoryID,
		Priority: input.priority, Resolution: input.resolution, DueAt: input.dueAt, Reviewed: input.reviewed,
		ServiceOn: input.serviceOn, ServiceAt: input.serviceAt, Elapsed: input.elapsed, Effort: input.effort,
		ExpectedCost: input.expectedCost, ExternalReference: input.externalReference, ExternalPayload: input.externalPayload, ExternalURL: input.externalURL}
}
