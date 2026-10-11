package definition_test

import (
	"bytes"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"testing"
	"time"
)

func TestDateTimeHistoricalDefaultsRoundTripAndRejectNoncanonicalValues(t *testing.T) {
	for _, instant := range []time.Time{time.Time{}, time.Date(1969, 12, 31, 23, 59, 59, 999999000, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)} {
		model, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "event", GoName: "Event", Fields: []schema.Field{schema.DateTimeField("at", "At", schema.Nullable(), schema.Default(instant))}}}})
		if err != nil {
			t.Fatal(err)
		}
		producer := definition.Producer{Name: "datetime-test", Version: "1"}
		migration := migrations.Migration{App: "events", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "events", Model: model.Models[0]}}}
		encoded, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "datetime-test", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		again, err := definition.Encode(producer, loaded.Definitions()[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("datetime historical default lost canonical identity")
		}
		canonical := model.Models[0].Fields[1].Default.DateTime
		for _, invalid := range []string{"2026-09-19T03:34:56Z", "2026-09-19T12:34:56.000000+09:00", "0000-01-01T00:00:00.000000Z"} {
			bad := bytes.ReplaceAll(encoded, []byte(canonical), []byte(invalid))
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid-datetime", Document: bad}); err == nil {
				t.Fatal("noncanonical historical datetime accepted")
			}
		}
	}
}
