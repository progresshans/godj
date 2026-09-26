package definition_test

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestChoicesAndAlterFieldDefinitionOwnRoundTripAndSemanticDigest(t *testing.T) {
	model, err := schema.Build(schema.Definition{AppLabel: "choices", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.IntegerField("priority", "Priority", schema.Choices(schema.Choice(int64(math.MinInt64), "Low"), schema.Choice(int64(0), "Zero"), schema.Choice(int64(math.MaxInt64), "High"))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	before := model.Models[0].Fields[1]
	after := before.Clone()
	after.Choices[0].Label = "Updated"
	change := migrations.Migration{App: "choices", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "choices", Model: model.Models[0]},
		&migrations.AlterField{AppLabel: "choices", ModelName: "entry", Before: before, After: after},
	}}
	producer := definition.Producer{Name: "choices-test", Version: "1"}
	encoded, err := definition.Encode(producer, change)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"integer":0`)) || !bytes.Contains(encoded, []byte(`9223372036854775807`)) {
		t.Fatal("integer choices lost exact payloads")
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "choices", Document: encoded})
	if err != nil {
		t.Fatal(err)
	}
	again, err := definition.Encode(producer, loaded.Definitions()[0])
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("choice round trip changed bytes: %v", err)
	}
	snapshot := loaded.Definitions()[0]
	operation := snapshot.Operations[1].(migrations.AlterField)
	operation.After.Choices[0].Label = "mutated snapshot"
	again, err = definition.Encode(producer, loaded.Definitions()[0])
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatal("published history retained consumer choices slice")
	}
	for _, mutate := range []func(*ir.Field){
		func(field *ir.Field) { field.Choices[0].Label = "Another" },
		func(field *ir.Field) { field.Choices[0], field.Choices[1] = field.Choices[1], field.Choices[0] },
		func(field *ir.Field) { field.Choices[0].Value.Integer++ },
	} {
		modified := loaded.Definitions()[0]
		op := modified.Operations[1].(migrations.AlterField)
		mutate(&op.After)
		modified.Operations[1] = op
		wire, err := definition.Encode(producer, modified)
		if err != nil {
			t.Fatal(err)
		}
		set, _, err := definition.Load(definition.Source{SourceID: "choices", Document: wire})
		if err != nil || set.Digest() == loaded.Digest() {
			t.Fatalf("choice semantics did not change historical digest: %v", err)
		}
	}
	operation = loaded.Definitions()[0].Operations[1].(migrations.AlterField)
	operation.After.Nullable = !operation.After.Nullable
	change.Operations[1] = operation
	if _, err := definition.Encode(producer, change); err == nil {
		t.Fatal("physical AlterField encoded as choices-only")
	}
}

func TestChoicesDefinitionRejectsMalformedAndOversizedMetadata(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "choices", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.CharField("status", "Status", 12, schema.Choices(schema.Choice("open", "Open"))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	change := migrations.Migration{App: "choices", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "choices", Model: s.Models[0]}}}
	producer := definition.Producer{Name: "choices-test", Version: "1"}
	encoded, err := definition.Encode(producer, change)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`null`, `[]`, `[{}]`, `[{"label":"Open","value":null}]`,
		`[{"label":"Open","value":{"kind":"integer","integer":0}}]`,
		`[{"label":"Open","value":{"kind":"string","string":"open","integer":0}}]`,
		`[{"label":"Open","value":{"kind":"string","string":"open"},"unknown":true}]`,
		`[{"label":false,"value":{"kind":"string","string":"open"}}]`,
		`[{"label":"Open","value":{"kind":"string","string":"open"}},{"label":"Again","value":{"kind":"string","string":"open"}}]`,
		`[{"label":"Long","value":{"kind":"string","string":"more-than-twelve"}}]`,
	} {
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatal(err)
		}
		var choices any
		if err := json.Unmarshal([]byte(invalid), &choices); err != nil {
			t.Fatal(err)
		}
		field := document["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
		field["choices"] = choices
		wire, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: wire}); err == nil {
			t.Fatalf("malformed choices loaded: %s", invalid)
		}
	}
	s.Models[0].Fields[1].Choices[0].Label = strings.Repeat("x", definition.MaxDocumentBytes+1)
	change.Operations[0] = migrations.CreateModel{AppLabel: "choices", Model: s.Models[0]}
	if _, err := definition.Encode(producer, change); err == nil {
		t.Fatal("oversized label escaped encoding resource bounds")
	}
}
