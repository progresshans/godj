package definition_test

import (
	"bytes"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"testing"
)

func TestDateHistoricalDefaultsRoundTripAndRejectNoncanonicalValues(t *testing.T) {
	for _, instant := range []calendar.Date{{Year: 1, Month: 1, Day: 1}, {Year: 2000, Month: 2, Day: 29}, {Year: 9999, Month: 12, Day: 31}} {
		model, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "event", GoName: "Event", Fields: []schema.Field{schema.DateField("at", "At", schema.Nullable(), schema.Default(instant))}}}})
		if err != nil {
			t.Fatal(err)
		}
		producer := definition.Producer{Name: "date-test", Version: "1"}
		migration := migrations.Migration{App: "events", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "events", Model: model.Models[0]}}}
		encoded, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "date-test", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		again, err := definition.Encode(producer, loaded.Definitions()[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("date historical default lost canonical identity")
		}
		canonical := model.Models[0].Fields[1].Default.Date
		for _, invalid := range []string{"2000-2-29", "2000-02-29T00:00:00Z", "0000-01-01", "1900-02-29", "10000-01-01"} {
			bad := bytes.ReplaceAll(encoded, []byte(canonical), []byte(invalid))
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid-date", Document: bad}); err == nil {
				t.Fatal("noncanonical historical date accepted")
			}
		}
	}
}
