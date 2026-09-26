package helpdesk

import (
	"context"
	"errors"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
)

// ticketRecord owns the scalar row and its authorized, fully loaded relation
// keys. Pure Form/Admin/API projection never performs a lazy database read.
type ticketRecord struct {
	models.Ticket
	labels []int64
}

func ticketScalar(value ticketRecord, field ir.Field) (query.Value, bool) {
	return (models.TicketDescriptor{}).WriteFieldValue(value.Ticket, field)
}

func ticketCollection(value ticketRecord, field ir.ManyToManyField) ([]int64, bool) {
	return slices.Clone(value.labels), field.Name == "labels" && value.labels != nil
}

func ticketSnapshot(value ticketRecord, projector admin.ModelProjector[models.Ticket]) (admin.Object, error) {
	if value.labels == nil {
		return admin.Object{}, errors.New("helpdesk: ticket labels were not loaded")
	}
	base, err := projector.Project(value.Ticket, value.ID, value.Subject)
	if err != nil {
		return admin.Object{}, err
	}
	members, _ := base.Values().Members()
	values := make(map[string]templates.Value, len(members)+1)
	for _, member := range members {
		values[member.Name()] = member.Value()
	}
	labels := make([]templates.Value, len(value.labels))
	for index, key := range value.labels {
		labels[index] = templates.Integer(key)
	}
	values["labels"] = templates.List(labels...)
	return admin.NewObject(value.ID, value.Subject, values)
}

func (a *Application) withTicketLabels(source project.ModelsTicketQuery) project.ModelsTicketPrefetchQuery {
	return source.PrefetchRelated(source.Prefetch.Labels.
		Filter(a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)).
		OrderBy(models.LabelFields.ID.Asc()))
}

func ticketRecordFromModel(ctx context.Context, selected *project.ModelsTicket) (ticketRecord, error) {
	value, err := selected.Unwrap()
	if err != nil {
		return ticketRecord{}, err
	}
	collection, err := selected.Labels()
	if err != nil {
		return ticketRecord{}, err
	}
	labels, err := collection.All(ctx)
	if err != nil {
		return ticketRecord{}, err
	}
	keys := make([]int64, len(labels))
	for index, label := range labels {
		keys[index] = label.ID
	}
	return ticketRecord{Ticket: value, labels: keys}, nil
}

func (a *Application) readTicketRecord(ctx context.Context, objects project.Models, id int64) (ticketRecord, bool, error) {
	selected, found, err := a.withTicketLabels(objects.ModelsTicket.
		Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).
		OrderBy(models.TicketFields.ID.Asc())).First(ctx)
	if err != nil || !found {
		return ticketRecord{}, false, err
	}
	value, err := ticketRecordFromModel(ctx, selected)
	return value, err == nil, err
}

// Admission uses the immutable principal accepted by authentication. A policy
// update revokes future sessions; it does not retroactively cancel admitted
// work. The same requirements are rechecked before any transaction reads.
func ticketWritePermission(ctx context.Context, principal auth.Principal, permission auth.Permission) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !principal.Authenticated() || !principal.Has(permission) || !principal.Has(ViewLabel) {
		return errors.New("helpdesk: ticket write lacks admitted owner or label permission")
	}
	return nil
}

func (a *Application) validateTicketLabelKeys(ctx context.Context, session db.Session, submitted []int64) ([]int64, error) {
	keys := append([]int64{}, submitted...)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	rejected := func() error {
		return validation.Reject(validation.NewErrors(validation.New("labels", "invalid_choice")), nil)
	}
	for _, key := range keys {
		if key <= 0 {
			return nil, rejected()
		}
	}
	if len(keys) == 0 {
		return keys, nil
	}
	labels, err := models.LabelObjects.Using(session).
		Filter(models.LabelFields.ID.In(keys...), a.relations.ModelsLabel.Category.ID.Exact(a.categoryID)).
		OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(labels) != len(keys) {
		return nil, rejected()
	}
	for index, label := range labels {
		if label.ID != keys[index] {
			return nil, rejected()
		}
	}
	return keys, nil
}

func (a *Application) setTicketLabelKeys(ctx context.Context, session db.RelationSession, owner models.Ticket, keys []int64) (bool, error) {
	links, err := models.TicketLabelObjects.Using(session).
		Filter(a.relations.ModelsTicketLabel.Ticket.ID.Exact(owner.ID)).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx)
	if err != nil {
		return false, err
	}
	before := make([]int64, len(links))
	for index, link := range links {
		before[index] = link.LabelID
	}
	slices.Sort(before)
	collection, err := a.collections.ModelsTicketLabels.InSession(session, owner)
	if err != nil {
		return false, err
	}
	if err := collection.SetKeys(ctx, keys); err != nil {
		return false, writeRejection(err)
	}
	return !slices.Equal(slices.Compact(before), keys), nil
}
