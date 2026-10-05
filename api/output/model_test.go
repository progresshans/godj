package output_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestModelOutputUsesEncoderAllowlistExactValuesAndComputedFields(t *testing.T) {
	metadata := (models.TicketDescriptor{}).Metadata()
	spec, err := serializers.FromModel(metadata,
		serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "subject"},
		serializers.ModelField{Name: "details"}, serializers.ModelField{Name: "priority"},
		serializers.ModelField{Name: "expected_cost"}, serializers.ModelField{Name: "external_payload"},
	)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	encoder, err := serializers.NewModelEncoder(spec, metadata, func(value models.Ticket, field ir.Field) (query.Value, bool) {
		reads++
		return (models.TicketDescriptor{}).WriteFieldValue(value, field)
	})
	if err != nil {
		t.Fatal(err)
	}
	computed, err := serializers.BooleanField("flag", serializers.WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	extended, err := encoder.WithComputed(serializers.ComputedField[models.Ticket]{Field: computed, Read: func(value models.Ticket) (serializers.Value, bool) { return serializers.Boolean(value.Closed), true }})
	if err != nil {
		t.Fatal(err)
	}
	for index := range metadata.Fields {
		metadata.Fields[index].Name = "caller mutation"
	}
	fields := extended.Spec().Fields()
	fields[0] = computed
	prepared := prepare(t, output.Model(extended), serializers.Limits{})
	if reads != 0 {
		t.Fatal("schema construction read model")
	}
	priority := int64(math.MinInt64)
	cost, err := decimal.Parse("12.3")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := jsonvalue.Parse([]byte(`{"z":9007199254740993,"a":1.2500}`))
	if err != nil {
		t.Fatal(err)
	}
	value := models.Ticket{ID: math.MaxInt64, Subject: " untrimmed ", Priority: &priority, ExpectedCost: &cost, ExternalPayload: &payload, CategoryID: 99}
	body, err := prepared.Encode(t.Context(), value)
	const want = `{"id":9223372036854775807,"subject":" untrimmed ","details":null,"priority":-9223372036854775808,"expected_cost":"12.30","external_payload":{"a":1.2500,"z":9007199254740993},"flag":false}`
	if err != nil || string(body) != want || reads != 6 {
		t.Fatalf("model = %s, %v, reads %d", body, err, reads)
	}
	legacy, err := extended.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	legacyBytes, err := serializers.Encode(legacy, serializers.Limits{})
	if err != nil || string(legacyBytes) != want {
		t.Fatalf("existing model output changed: %s, %v", legacyBytes, err)
	}
	if len(encoder.Spec().Fields()) != 6 {
		t.Fatal("computed shape changed base")
	}
	schemaBytes, err := serializers.Encode(prepared.Schema().Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(schemaBytes), `"enum"`) || strings.Contains(string(schemaBytes), `"default"`) || strings.Contains(string(schemaBytes), `"category"`) || !strings.Contains(string(schemaBytes), `"required":["id","subject","details","priority","expected_cost","external_payload","flag"]`) {
		t.Fatalf("input policy or hidden field became output: %s", schemaBytes)
	}
	for name, malformed := range map[string]models.Ticket{
		"overlong":     {Subject: strings.Repeat("a", 121)},
		"invalid UTF8": {Subject: "invalid\xff"},
		"invalid JSON": {Subject: "valid", ExternalPayload: &jsonvalue.Value{Text: `{"duplicate":1,"duplicate":2}`}},
	} {
		t.Run(name, func(t *testing.T) {
			if body, err := prepared.Encode(t.Context(), malformed); !errors.Is(err, &api.Error{Code: api.FailureInvalidResponse}) || body != nil {
				t.Fatalf("malformed model = %s, %v", body, err)
			}
		})
	}
}

func TestModelOutputErrorsAndCollectionBudgets(t *testing.T) {
	type group struct {
		title   string
		members []int64
		present bool
	}
	model := ir.Model{Fields: []ir.Field{{Name: "title", Kind: ir.FieldChar, MaxLength: 10}}, ManyToMany: []ir.ManyToManyField{{Name: "members", Target: ir.ModelIdentity{AppLabel: "example", ModelName: "member"}, Symmetry: ir.ManyToManyDirected}}}
	spec, err := serializers.FromModel(model, serializers.ModelField{Name: "title"}, serializers.ModelField{Name: "members", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, model,
		func(value group, field ir.Field) (query.Value, bool) {
			return query.String(value.title), field.Name == "title"
		},
		func(value group, field ir.ManyToManyField) ([]int64, bool) {
			return value.members, value.present && field.Name == "members"
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared := prepare(t, output.Model(encoder), serializers.Limits{MaxArrayItems: 2, MaxValues: 5})
	value := group{title: " untrimmed", members: []int64{math.MinInt64, math.MaxInt64}, present: true}
	body, err := prepared.Encode(t.Context(), value)
	if err != nil || string(body) != `{"title":" untrimmed","members":[-9223372036854775808,9223372036854775807]}` {
		t.Fatalf("collection = %s %v", body, err)
	}
	for name, value := range map[string]group{
		"missing collection":   {title: "present"},
		"oversized collection": {title: "present", members: make([]int64, 3), present: true},
	} {
		t.Run(name, func(t *testing.T) {
			if body, err := prepared.Encode(t.Context(), value); err == nil || body != nil {
				t.Fatal("bad collection published", err)
			}
		})
	}
	field, err := serializers.BooleanField("computed", serializers.WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	broken, err := encoder.WithComputed(serializers.ComputedField[group]{Field: field, Read: func(group) (serializers.Value, bool) { return serializers.Boolean(true), false }})
	if err != nil {
		t.Fatal(err)
	}
	brokenOutput := prepare(t, output.Model(broken), serializers.Limits{})
	if body, err := brokenOutput.Encode(t.Context(), value); err == nil || body != nil {
		t.Fatal("missing computed field published", err)
	}
	if _, err := output.New(output.Model(serializers.ModelEncoder[group]{}), serializers.Limits{}); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatal("zero model encoder accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reads := 0
	cancelling, err := serializers.NewModelEncoder(spec, model,
		func(group, ir.Field) (query.Value, bool) { reads++; cancel(); return query.String("title"), true },
		func(group, ir.ManyToManyField) ([]int64, bool) { reads++; return nil, true },
	)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := prepare(t, output.Model(cancelling), serializers.Limits{}).Encode(ctx, value); !errors.Is(err, context.Canceled) || body != nil || reads != 1 {
		t.Fatal("cancelled model read later fields", err, reads)
	}
}

func TestModelOpaqueJSONSharesParentDepthAndValueBudget(t *testing.T) {
	model := ir.Model{Fields: []ir.Field{{Name: "payload", Kind: ir.FieldJSON}}}
	spec, err := serializers.FromModel(model, serializers.ModelField{Name: "payload"})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, model, func(value jsonvalue.Value, _ ir.Field) (query.Value, bool) { return query.JSON(value), true })
	if err != nil {
		t.Fatal(err)
	}
	shape := output.Object(output.Field("model", output.Model(encoder), func(value jsonvalue.Value) jsonvalue.Value { return value }))
	payload, err := jsonvalue.Parse([]byte(`{"nested":[0]}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, limits := range map[string]serializers.Limits{"depth": {MaxDepth: 4}, "values": {MaxValues: 4}} {
		t.Run(name, func(t *testing.T) {
			if body, err := prepare(t, shape, limits).Encode(t.Context(), payload); !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) || body != nil {
				t.Fatalf("nested model budget = %s %v", body, err)
			}
		})
	}
	body, err := prepare(t, shape, serializers.Limits{MaxDepth: 5, MaxValues: 5}).Encode(t.Context(), payload)
	if err != nil || string(body) != `{"model":{"payload":{"nested":[0]}}}` {
		t.Fatalf("exact nested model bound = %s %v", body, err)
	}
}
