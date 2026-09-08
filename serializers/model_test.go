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

func TestModelEncoderUsesExplicitAllowlistWithoutInputCleaning(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	details := " original "
	ticket := models.Ticket{ID: 1, Subject: " Printer ", Details: &details, CategoryID: 2}
	encoder, err := serializers.NewModelEncoder(spec, metadata, (models.TicketDescriptor{}).WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	value, err := encoder.Encode(ticket)
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
		encoder, err := serializers.NewModelEncoder(spec, metadata, reader)
		if err == nil {
			_, err = encoder.Encode(ticket)
		}
		if err == nil {
			t.Fatal("invalid typed encoder accepted")
		}
	}
}

func TestModelEncoderOwnsMetadataAndDetachesReaderFields(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	for index := range metadata.Fields {
		if metadata.Fields[index].Name == "subject" {
			metadata.Fields[index].Default = &ir.ScalarDefault{Kind: ir.ScalarString, String: "seed"}
		}
	}
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "subject"})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, func(ticket models.Ticket, field ir.Field) (query.Value, bool) {
		if field.Default == nil || field.Default.String != "seed" {
			t.Fatal("reader mutated the prepared field")
		}
		value, found := (models.TicketDescriptor{}).WriteFieldValue(ticket, field)
		field.Default.String = "changed"
		return value, found
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range metadata.Fields {
		if metadata.Fields[index].Default != nil {
			metadata.Fields[index].Default.String = "caller change"
		}
		metadata.Fields[index].Name = "changed"
	}
	for range 2 {
		value, err := encoder.Encode(models.Ticket{Subject: "original"})
		if err != nil {
			t.Fatal(err)
		}
		object, _ := value.AsObject()
		subject, _ := object.Get("subject")
		if text, ok := subject.AsString(); !ok || text != "original" {
			t.Fatalf("encoded subject = %q, %v", text, ok)
		}
	}
	if _, err := (serializers.ModelEncoder[models.Ticket]{}).Encode(models.Ticket{}); err == nil {
		t.Fatal("zero encoder accepted")
	}
}
