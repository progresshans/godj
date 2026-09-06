package serializers_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/examples/helpdesk/modeldef"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestModelSerializerProjectsConstraintsWithExplicitExposure(t *testing.T) {
	schema, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	model := schema.Models[1]
	spec, err := serializers.FromModel(model,
		serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "subject"},
		serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "closed"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := spec.Bind(decodeObject(t, `{"subject":"Broken printer","details":null}`), serializers.ModeFull)
	if err != nil || !result.Valid() {
		t.Fatalf("binding: %v %v", result.Errors(), err)
	}
	closed, _ := result.Values().Get("closed")
	if value, valid := closed.AsBoolean(); !valid || value {
		t.Fatal("model default lost")
	}
	for _, document := range []string{`{"subject":"` + strings.Repeat("x", 121) + `"}`, `{"subject":"X","category":3}`, `{"subject":"X","id":3}`} {
		result, err := spec.Bind(decodeObject(t, document), serializers.ModeFull)
		if err != nil || result.Valid() {
			t.Fatalf("accepted %s: %v", document, err)
		}
	}
	for _, fields := range [][]serializers.ModelField{{{Name: "missing"}}, {{Name: "subject"}, {Name: "subject"}}} {
		if _, err := serializers.FromModel(model, fields...); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
			t.Fatalf("invalid field exposure was not a structured configuration error: %v", err)
		}
	}
}

func TestModelValueUsesExplicitAllowlistWithoutInputCleaning(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	details := " original "
	ticket := models.Ticket{ID: 1, Subject: " Printer ", Details: &details, CategoryID: 2}
	value, err := serializers.ModelValue(spec, metadata, ticket, (models.TicketDescriptor{}).WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	details = "changed"
	object, _ := value.AsObject()
	if _, found := object.Get("category"); found {
		t.Fatal("omitted model field exposed")
	}
	projected, _ := object.Get("details")
	if text, _ := projected.AsString(); text != " original " {
		t.Fatal("output was cleaned or retained nullable model state")
	}
	for _, reader := range []func(models.Ticket, ir.Field) (query.Value, bool){
		nil,
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Value{}, false },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Boolean(true), true },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.Null(), true },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.String(strings.Repeat("x", 121)), true },
		func(models.Ticket, ir.Field) (query.Value, bool) { return query.String("bad\xff"), true },
	} {
		if _, err := serializers.ModelValue(spec, metadata, ticket, reader); err == nil {
			t.Fatal("invalid typed projection accepted")
		}
	}
}
