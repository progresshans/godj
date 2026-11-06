package definition_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestFloatHistoricalDefaultsRoundTripAndRejectNoncanonicalBits(t *testing.T) {
	for _, number := range []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, math.MaxFloat64, math.NaN(), math.Inf(1), math.Inf(-1)} {
		model, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "event", GoName: "Event", Fields: []schema.Field{schema.FloatField("amount", "Amount", schema.Nullable(), schema.Default(number))}}}})
		if err != nil {
			t.Fatal(err)
		}
		producer := definition.Producer{Name: "float-test", Version: "1"}
		migration := migrations.Migration{App: "events", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "events", Model: model.Models[0]}}}
		encoded, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "float-test", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		again, err := definition.Encode(producer, loaded.Definitions()[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("float historical default lost canonical identity")
		}
		canonical := model.Models[0].Fields[1].Default.FloatBits
		for _, invalid := range []string{"0", "1.5", "7FF0000000000000", "7ff0000000000001", "fff8000000000000", "000000000000000g"} {
			bad := bytes.ReplaceAll(encoded, []byte(canonical), []byte(invalid))
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid-float", Document: bad}); err == nil {
				t.Fatal("noncanonical historical float accepted")
			}
		}
	}
}
