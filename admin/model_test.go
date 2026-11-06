package admin_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestModelProjectorUsesExplicitAllowlistAndValidatesTypedValues(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	details := "original"
	ticket := models.Ticket{ID: 1, Subject: "Printer", Details: &details, CategoryID: 2}
	projector, err := admin.NewModelProjector(metadata, (models.TicketDescriptor{}).WriteFieldValue, "subject", "details")
	if err != nil {
		t.Fatal(err)
	}
	object, err := projector.Project(ticket, ticket.ID, ticket.Subject)
	if err != nil {
		t.Fatal(err)
	}
	details = "changed"
	values, _ := object.Values().Members()
	if len(values) != 2 {
		t.Fatal("unselected model fields were exposed")
	}
	detail, _ := object.Values().Member("details")
	if text, ok := detail.AsString(); !ok || text != "original" {
		t.Fatal("snapshot retained nullable model state")
	}
	for _, names := range [][]string{nil, {"unknown"}, {"subject", "subject"}} {
		if _, err := admin.NewModelProjector(metadata, (models.TicketDescriptor{}).WriteFieldValue, names...); err == nil {
			t.Fatal("invalid allowlist accepted")
		}
	}
	for _, reader := range []func(models.Ticket, ir.Field) (query.Value, bool){
		nil,
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Value{}, false },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Integer(3), true },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Null(), true },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.String(strings.Repeat("x", 121)), true },
	} {
		projector, err := admin.NewModelProjector(metadata, reader, "subject")
		if err == nil {
			_, err = projector.Project(ticket, 1, "Printer")
		}
		if err == nil {
			t.Fatal("invalid typed projection accepted")
		}
	}
	for index := range metadata.Fields {
		metadata.Fields[index].Name = "changed"
	}
	if _, err := projector.Project(ticket, ticket.ID, ticket.Subject); err != nil {
		t.Fatalf("caller metadata changed prepared projector: %v", err)
	}
	if _, err := (admin.ModelProjector[models.Ticket]{}).Project(ticket, 1, "Printer"); err == nil {
		t.Fatal("zero projector accepted")
	}
}
