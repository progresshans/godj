package model_test

import (
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/examples/helpdesk/models"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestInitialValuesSelectsTypedFieldsAndDetachesNullableValue(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"subject", "details", "closed", "priority"})
	if err != nil {
		t.Fatal(err)
	}
	details := "original"
	priority := int64(math.MaxInt64)
	ticket := models.Ticket{ID: 1, Subject: "Printer", Details: &details, CategoryID: 2, Priority: &priority}
	initial, err := formmodel.InitialValues(metadata, spec, ticket, (models.TicketDescriptor{}).WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	details = "changed"
	priority = 0
	if text, ok := initial["details"].AsString(); !ok || text != "original" || len(initial) != 4 {
		t.Fatal("initial values retained model state or exposed unselected fields")
	}
	if integer, ok := initial["priority"].AsInteger(); !ok || integer != math.MaxInt64 {
		t.Fatal("integer initial value retained model state or lost precision")
	}
	ticket.Priority = nil
	cleared, err := formmodel.InitialValues(metadata, spec, ticket, (models.TicketDescriptor{}).WriteFieldValue)
	if err != nil || !cleared["priority"].IsNull() {
		t.Fatal("nullable integer initial became zero")
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
