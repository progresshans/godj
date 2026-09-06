package model_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/examples/helpdesk/models"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestInitialValuesSelectsTypedFieldsAndDetachesNullableValue(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"subject", "details", "closed"})
	if err != nil {
		t.Fatal(err)
	}
	details := "original"
	ticket := models.Ticket{ID: 1, Subject: "Printer", Details: &details, CategoryID: 2}
	initial, err := formmodel.InitialValues(metadata, spec, ticket, (models.TicketDescriptor{}).WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	details = "changed"
	if text, ok := initial["details"].AsString(); !ok || text != "original" || len(initial) != 3 {
		t.Fatal("initial values retained model state or exposed unselected fields")
	}
	for _, bad := range []models.Ticket{{Subject: strings.Repeat("x", 121)}, {Subject: "bad\x00text"}} {
		if _, err := formmodel.InitialValues(metadata, spec, bad, (models.TicketDescriptor{}).WriteFieldValue); err == nil {
			t.Fatal("invalid model text accepted")
		}
	}
	for _, reader := range []func(models.Ticket, ir.Field) (query.Value, bool){
		nil,
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Value{}, false },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Boolean(true), true },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Null(), true },
	} {
		if _, err := formmodel.InitialValues(metadata, spec, ticket, reader); err == nil {
			t.Fatal("missing or mistyped typed projection accepted")
		}
	}
}
