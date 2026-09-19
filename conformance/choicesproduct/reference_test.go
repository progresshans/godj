package choicesproduct_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

type choiceResult struct {
	Value  json.RawMessage `json:"value"`
	Errors []string        `json:"errors"`
}

type choiceObservation struct {
	Field    string `json:"field"`
	Widget   string `json:"widget"`
	Required bool   `json:"required"`
	Cases    []struct {
		Input      json.RawMessage `json:"input"`
		Form       choiceResult    `json:"form"`
		Serializer choiceResult    `json:"serializer"`
	} `json:"cases"`
}

func choiceModel(t *testing.T) ir.Model {
	t.Helper()
	definition, err := schema.Build(schema.Definition{AppLabel: "choice_reference", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.CharField("status", "Status", 12, schema.Default("open"), schema.Choices(schema.Choice("open", "Open"), schema.Choice("closed", "Closed"))),
		schema.IntegerField("priority", "Priority", schema.Nullable(), schema.Choices(schema.Choice(int64(-1), "Low"), schema.Choice(int64(0), "Normal"), schema.Choice(int64(1), "High"))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return definition.Models[0]
}

// The Python capture owns its model and expectations. The one deliberate
// serializer boundary is GoDj's strict JSON scalar types, already shared by
// parsing, OpenAPI and clients; DRF's numeric-string coercion is not implied.
func TestChoicesFormAndSerializerAgainstIndependentDjango(t *testing.T) {
	data, err := os.ReadFile("../../schema/testdata/choices-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, DRF  string
		Observations []choiceObservation
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" {
		t.Fatal("reference is not pinned")
	}
	model := choiceModel(t)
	count := 0
	for _, observation := range reference.Observations {
		if observation.Field == "" {
			continue
		}
		formSpec, err := formmodel.NewSpecForFields(model, []string{observation.Field})
		if err != nil {
			t.Fatal(err)
		}
		field := formSpec.Fields()[0]
		if observation.Widget != "Select" || field.Widget() != forms.Select || field.Required() != observation.Required {
			t.Fatal("model choices did not select the matching form")
		}
		serializer, err := serializers.FromModel(model, serializers.ModelField{Name: observation.Field})
		if err != nil {
			t.Fatal(err)
		}
		for index, item := range observation.Cases {
			count++
			t.Run(observation.Field+"/"+strconv.Itoa(index), func(t *testing.T) {
				raw := string(item.Input)
				if raw == "null" {
					raw = ""
				} else if len(raw) > 0 && raw[0] == '"' {
					if err := json.Unmarshal(item.Input, &raw); err != nil {
						t.Fatal(err)
					}
				}
				bound, err := formSpec.Bind(forms.NewData(map[string][]string{observation.Field: {raw}}), nil)
				if err != nil {
					t.Fatal(err)
				}
				assertChoiceCodes(t, bound.Errors(), item.Form.Errors)
				value, found := bound.Cleaned().Get(observation.Field)
				if found != (len(item.Form.Errors) == 0) {
					t.Fatal("form published or omitted invalid data")
				}
				if found {
					var actual any
					if value.Kind() == forms.ValueString {
						actual, _ = value.AsString()
					} else if value.Kind() == forms.ValueInteger {
						actual, _ = value.AsInteger()
					}
					encoded, err := json.Marshal(actual)
					if err != nil || !bytes.Equal(encoded, item.Form.Value) {
						t.Fatalf("form %s: %s, want %s: %v", item.Input, encoded, item.Form.Value, err)
					}
				}
				var native any
				decoder := json.NewDecoder(bytes.NewReader(item.Input))
				decoder.UseNumber()
				if err := decoder.Decode(&native); err != nil {
					t.Fatal(err)
				}
				input := serializers.Null()
				switch v := native.(type) {
				case string:
					input = serializers.String(v)
				case bool:
					input = serializers.Boolean(v)
				case json.Number:
					n, err := v.Int64()
					if err != nil {
						t.Fatal(err)
					}
					input = serializers.Integer(n)
				}
				object, err := serializers.NewObject(serializers.MemberOf(observation.Field, input))
				if err != nil {
					t.Fatal(err)
				}
				result, err := serializer.Bind(object, serializers.ModeFull)
				if err != nil {
					t.Fatal(err)
				}
				expected := item.Serializer.Errors
				if !input.IsNull() && (observation.Field == "status" && input.Kind() != serializers.ValueString || observation.Field == "priority" && input.Kind() != serializers.ValueInteger) {
					expected = []string{"type"}
				}
				assertChoiceCodes(t, result.Errors(), expected)
				output, found := result.Values().Get(observation.Field)
				if found != (len(expected) == 0) {
					t.Fatal("serializer publication differs from validation")
				}
				if found {
					var actual any
					if output.Kind() == serializers.ValueString {
						actual, _ = output.AsString()
					} else if output.Kind() == serializers.ValueInteger {
						actual, _ = output.AsInteger()
					}
					encoded, err := json.Marshal(actual)
					if err != nil || !bytes.Equal(encoded, item.Serializer.Value) {
						t.Fatalf("serializer %s: %s, want %s: %v", item.Input, encoded, item.Serializer.Value, err)
					}
				}
			})
		}
	}
	if count != 19 {
		t.Fatalf("reference coverage = %d", count)
	}
}

func assertChoiceCodes(t *testing.T, failures validation.Errors, want []string) {
	t.Helper()
	var got []string
	for _, failure := range failures.All() {
		got = append(got, string(failure.Code()))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("validation codes = %v, want %v", got, want)
	}
}

func TestChoiceProjectionOwnershipAndUnrestrictedModelOutput(t *testing.T) {
	model := choiceModel(t)
	spec, err := serializers.FromModel(model, serializers.ModelField{Name: "status"}, serializers.ModelField{Name: "priority"})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, model, func(row map[string]query.Value, field ir.Field) (query.Value, bool) {
		if len(field.Choices) > 0 {
			field.Choices[0].Label = "callback mutation"
		}
		value, found := row[field.Name]
		return value, found
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range model.Fields {
		if len(model.Fields[i].Choices) > 0 {
			model.Fields[i].Choices[0].Value = ir.Scalar{Kind: ir.ScalarString, String: "mutated"}
		}
	}
	for _, field := range spec.Fields() {
		if choices := field.Choices(); len(choices) > 0 {
			choices[0].Value = serializers.String("mutated")
		}
	}
	output, err := encoder.Encode(map[string]query.Value{"status": query.String("bad"), "priority": query.Integer(99)})
	if err != nil {
		t.Fatal(err)
	}
	object, _ := output.AsObject()
	status, _ := object.Get("status")
	priority, _ := object.Get("priority")
	if value, _ := status.AsString(); value != "bad" {
		t.Fatal("stored unknown string was replaced")
	}
	if value, _ := priority.AsInteger(); value != 99 {
		t.Fatal("stored unknown integer was replaced")
	}
	input, _ := serializers.NewObject(serializers.MemberOf("status", serializers.String("open")))
	bound, err := spec.Bind(input, serializers.ModePartial)
	if err != nil || !bound.Valid() {
		t.Fatalf("caller mutated published choices: %v", err)
	}
}
