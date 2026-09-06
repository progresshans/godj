package admin_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestModelObjectUsesExplicitAllowlistAndValidatesTypedValues(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	details := "original"
	ticket := models.Ticket{ID: 1, Subject: "Printer", Details: &details, CategoryID: 2}
	object, err := admin.ModelObject(metadata, ticket, (models.TicketDescriptor{}).WriteFieldValue, ticket.ID, ticket.Subject, "subject", "details")
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
		if _, err := admin.ModelObject(metadata, ticket, (models.TicketDescriptor{}).WriteFieldValue, 1, "Printer", names...); err == nil {
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
		if _, err := admin.ModelObject(metadata, ticket, reader, 1, "Printer", "subject"); err == nil {
			t.Fatal("invalid typed projection accepted")
		}
	}
}
