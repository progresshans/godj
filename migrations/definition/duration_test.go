package definition_test

import (
	"bytes"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"testing"
)

func TestDurationHistoricalDefaultsRoundTripAndRejectNoncanonicalValues(t *testing.T) {
	for _, instant := range []duration.Duration{{}, {Days: duration.MinDays}, {Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}} {
		model, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "event", GoName: "Event", Fields: []schema.Field{schema.DurationField("elapsed", "Elapsed", schema.Nullable(), schema.Default(instant))}}}})
		if err != nil {
			t.Fatal(err)
		}
		producer := definition.Producer{Name: "time-test", Version: "1"}
		migration := migrations.Migration{App: "events", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "events", Model: model.Models[0]}}}
		encoded, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "time-test", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		again, err := definition.Encode(producer, loaded.Definitions()[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("time historical default lost canonical identity")
		}
		canonical := model.Models[0].Fields[1].Default.Duration
		for _, invalid := range []string{"12:34", "12:34:56.123456T00:00:00Z", "24:00:00", "12:34:60", "124:00:00"} {
			bad := bytes.ReplaceAll(encoded, []byte(canonical), []byte(invalid))
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid-time", Document: bad}); err == nil {
				t.Fatal("noncanonical historical time accepted")
			}
		}
	}
}
