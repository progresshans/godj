package definition_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestIntegerDefaultsRoundTripThroughHistoricalDefinition(t *testing.T) {
	for _, value := range []int64{math.MinInt64, 0, math.MaxInt64} {
		model, err := schema.Build(schema.Definition{AppLabel: "numbers", Models: []schema.Model{{Name: "counter", GoName: "Counter", Fields: []schema.Field{
			schema.IntegerField("required", "Required", schema.Default(value)),
			schema.IntegerField("nullable", "Nullable", schema.Nullable(), schema.Default(value)),
		}}}})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := definition.Encode(definition.Producer{Name: "integer-test", Version: "1"}, migrations.Migration{
			App: "numbers", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "numbers", Model: model.Models[0]}},
		})
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "integer-test", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		// Loader publication can be re-encoded without loss through its public
		// immutable migration snapshot; no floating-point decoder is involved.
		definitions := loaded.Definitions()
		if len(definitions) != 1 {
			t.Fatal("integer definition disappeared")
		}
		again, err := definition.Encode(definition.Producer{Name: "integer-test", Version: "1"}, definitions[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatalf("int64 historical roundtrip changed: %v", err)
		}
	}
}
