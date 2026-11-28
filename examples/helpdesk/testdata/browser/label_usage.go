package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
)

type labelUsageSnapshot struct {
	labels []models.Label
	links  []models.TicketLabel
}

func seedLabelUsage(ctx context.Context, backend db.Session, category models.Category, tickets []models.Ticket) (labelUsageSnapshot, error) {
	var zero labelUsageSnapshot
	if len(tickets) != 30 {
		return zero, errors.New("label usage fixture requires retained summary tickets")
	}
	labels, err := models.LabelObjects.Using(backend).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil {
		return zero, err
	}
	if len(labels) != 2 || labels[0].ID != 1 || labels[1].ID != 2 {
		return zero, errors.New("label usage fixture initial labels differ")
	}
	for number := 3; number <= 25; number++ {
		value, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate(fmt.Sprintf("Usage label %02d", number), category.ID))
		if err != nil {
			return zero, err
		}
		labels = append(labels, value)
	}
	outside, err := models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("Private outside category"))
	if err != nil {
		return zero, err
	}
	foreign, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("Private outside label", outside.ID))
	if err != nil {
		return zero, err
	}
	labels = append(labels, foreign)
	var links []models.TicketLabel
	link := func(ticket, label int64) error {
		value, err := models.TicketLabelObjects.Create(ctx, backend, models.NewTicketLabelCreate(ticket, label))
		if err == nil {
			links = append(links, value)
		}
		return err
	}
	for _, ticket := range tickets[:3] {
		if err := link(ticket.ID, labels[0].ID); err != nil {
			return zero, err
		}
	}
	for _, label := range labels[2:] {
		if err := link(tickets[0].ID, label.ID); err != nil {
			return zero, err
		}
	}
	return labelUsageSnapshot{labels: labels, links: links}, nil
}
func (before labelUsageSnapshot) verify(ctx context.Context, backend browserReadBackend, actor auth.Principal, links []models.TicketLabel) ([]map[string]any, error) {
	labels, err := models.LabelObjects.Using(backend).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before.labels, labels) || !reflect.DeepEqual(before.links, links) {
		return nil, errors.New("label usage browser changed labels or links")
	}
	evidence := make([]map[string]any, len(labels))
	for index, label := range labels {
		history, err := browserAuditHistory(ctx, backend, actor, "helpdesk.label", label.ID, 10)
		if err != nil {
			return nil, err
		}
		if len(history) != 0 {
			return nil, errors.New("label usage browser appended label audit")
		}
		evidence[index] = map[string]any{"id": label.ID, "name": label.Name, "category": label.CategoryID, "audit": history}
	}
	for _, link := range links {
		history, err := browserAuditHistory(ctx, backend, actor, "helpdesk.ticket_label", link.ID, 10)
		if err != nil {
			return nil, err
		}
		if len(history) != 0 {
			return nil, errors.New("label usage browser appended link audit")
		}
	}
	return evidence, nil
}
