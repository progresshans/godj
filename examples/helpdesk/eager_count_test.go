package helpdesk_test

import (
	"context"
	"testing"

	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
)

// Called by the actual SQLite and PostgreSQL Helpdesk consumers after reopen.
func verifyHelpdeskEagerCount(t *testing.T, ctx context.Context, backend *helpdeskReadCounter, category models.Category, seed models.Ticket) {
	t.Helper()
	objects, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	base := objects.ModelsTicket.OrderBy(models.TicketFields.ID.Asc()).SelectRelated(objects.ModelsTicket.Related.Category)
	before := backend.queries
	if got, err := base.Count(ctx); err != nil || got != 2 || backend.queries != before+1 {
		t.Fatalf("Helpdesk cold eager Count = %d, %v", got, err)
	}
	if _, selected := backend.last.RelationProjection(); selected || !backend.last.ResultShape().IsCountAll() {
		t.Fatal("Helpdesk Count loads category objects")
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	filtered := base.Filter(relations.ModelsTicket.Category.ID.Exact(category.ID))
	if got, err := filtered.Count(ctx); err != nil || got != 1 {
		t.Fatalf("filtered Count = %d, %v", got, err)
	}
	page, err := filtered.Limit(1)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := page.Count(ctx); err != nil || got != 1 {
		t.Fatalf("page Count = %d, %v", got, err)
	}
	rows, err := page.All(ctx)
	if err != nil || len(rows) != 1 || rows[0].ID != seed.ID {
		t.Fatalf("page All = %v, %v", rows, err)
	}
	before = backend.queries
	if got, err := page.Count(ctx); err != nil || got != 1 || backend.queries != before {
		t.Fatalf("warm page Count = %d, %v", got, err)
	}
	selected, err := rows[0].Category(ctx)
	if err != nil || selected.Name != category.Name || backend.queries != before {
		t.Fatalf("warm category = %v, %v", selected, err)
	}
	past, err := filtered.Offset(1)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := past.Count(ctx); err != nil || got != 0 {
		t.Fatalf("past-page Count = %d, %v", got, err)
	}
	objectsAdapter, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	dynamic, err := objectsAdapter.ModelsTicket.SelectRelated(models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(seed.ID))).ParseDynamic("category")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := dynamic.Count(ctx); err != nil || got != 1 {
		t.Fatalf("dynamic Count = %d, %v", got, err)
	}
}
