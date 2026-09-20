package forms_test

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/jsoninput"
	"github.com/progresshans/godj/internal/jsontest"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func jsonDocument(t *testing.T, raw string) jsonvalue.Value {
	t.Helper()
	value, err := jsonvalue.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestJSONModelFormsAgainstPinnedDjango(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.JSONField("payload", "Payload", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	for index, observed := range jsontest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(built.Models[0], formmodel.OverrideField("payload", formmodel.WithRequired(observed.Required)))
			if err != nil {
				t.Fatal(err)
			}
			if spec.Fields()[0].Widget() != forms.Textarea || observed.Widget != "Textarea" {
				t.Fatal("JSON form lost textarea")
			}
			data := map[string][]string{}
			raw := ""
			if observed.Input != nil {
				raw = *observed.Input
				data["payload"] = []string{raw}
			}
			strictInvalid := raw == `NaN` || raw == `Infinity` || raw == `-Infinity` || raw == `{"a":1,"a":2}` || raw == `"\ud800"`
			exactDifference := raw == `9007199254740993.0` || raw == `9007199254740993.00` || raw == `1e-400` || raw == `{"v":1e400}`
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "one": forms.JSON(jsonDocument(t, `1`)), "float_one": forms.JSON(jsonDocument(t, `1.0`)), "true": forms.JSON(jsonDocument(t, `true`)), "object": forms.JSON(jsonDocument(t, `{"a":1,"b":2}`))} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"payload": initial})
				if err != nil {
					t.Fatal(err)
				}
				codes := []string{}
				for _, violation := range bound.Errors().All() {
					if violation.Field() != "payload" {
						t.Fatal("unexpected JSON error field")
					}
					codes = append(codes, string(violation.Code()))
				}
				wantCodes := observed.Cleaned.Codes
				if strictInvalid {
					wantCodes = []string{"invalid"}
				}
				if bound.Valid() != (len(wantCodes) == 0) || !reflect.DeepEqual(codes, wantCodes) {
					t.Fatalf("JSON %q codes %v want %v", raw, codes, wantCodes)
				}
				value, present := bound.Cleaned().Get("payload")
				if present != bound.Valid() {
					t.Fatal("invalid JSON leaked a cleaned value")
				}
				if present {
					if observed.Cleaned.Value.Kind == "null" {
						if !value.IsNull() {
							t.Fatal("form empty/null value became structured JSON")
						}
					} else {
						got, ok := value.AsJSON()
						if !ok {
							t.Fatal("form JSON lost its type")
						}
						if got != jsonDocument(t, raw) {
							t.Fatal("form JSON discarded exact numeric tokens")
						}
						if !exactDifference && !jsoninput.Equal(got, observed.Cleaned.Value.JSON(t)) {
							t.Fatalf("JSON clean %q disagrees with independent value", raw)
						}
					}
				}
				changed, exists := observed.Changed[name]
				if !exists || changed.Exception != "" || changed.Value.Kind != "bool" {
					t.Fatal("missing JSON change observation")
				}
				var want bool
				if err := json.Unmarshal(changed.Value.Value, &want); err != nil {
					t.Fatal(err)
				}
				if strictInvalid {
					want = true
				}
				if (len(bound.Changed()) == 1) != want {
					t.Fatalf("JSON %q change from %s differs", raw, name)
				}
			}
		})
	}
}

func TestJSONFormDefaultsOwnershipAndNullInitial(t *testing.T) {
	value := jsonDocument(t, `{"": [340282366920938463463374607431768211455,null]}`)
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.JSONField("payload", "Payload", schema.Nullable(), schema.Default(value))}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := built.Models[0]
	spec, err := formmodel.NewSpec(metadata)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := formmodel.InitialValues(metadata, spec, value, func(v jsonvalue.Value, field ir.Field) (query.Value, bool) {
		field.Default.JSON = `"changed"`
		return query.JSON(v), true
	})
	if err != nil {
		t.Fatal(err)
	}
	value.Text = `"changed"`
	unbound, err := spec.Unbound(initial)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := unbound.Initial().JSON("payload")
	if !ok || got.Text == value.Text || metadata.Fields[1].Default.JSON != got.Text {
		t.Fatal("JSON form retained caller or metadata storage")
	}
	for _, provided := range []forms.Value{forms.String(`{}`), forms.JSON(jsonvalue.Value{})} {
		if _, err := spec.Unbound(map[string]forms.Value{"payload": provided}); err == nil {
			t.Fatal("invalid JSON form initial accepted")
		}
	}
	for _, options := range [][]forms.FieldOption{{nil}, {forms.WithRequired(false)}, {forms.WithMaxLength(10)}, {forms.WithWidget(forms.NumberInput)}, {forms.WithDefault(forms.String(`{}`))}, {forms.WithDefault(forms.JSON(jsonvalue.Value{}))}} {
		if _, err := forms.JSONField("payload", options...); err == nil {
			t.Fatal("invalid JSON form config accepted")
		}
	}
	for _, initial := range []forms.Value{forms.Null(), forms.JSON(jsonvalue.Null())} {
		bound, err := spec.Bind(forms.NewData(map[string][]string{"payload": {"null"}}), map[string]forms.Value{"payload": initial})
		if err != nil || !bound.Valid() || len(bound.Changed()) != 0 {
			t.Fatal("unchanged JSON null forced a write", err)
		}
	}
}
