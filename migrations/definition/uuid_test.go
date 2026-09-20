package definition_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/uuid"
)

func TestUUIDHistoricalDefaultsRoundTripAndRejectNoncanonicalValues(t *testing.T) {
	producer := definition.Producer{Name: "uuid-test", Version: "1"}
	for _, raw := range []string{"00000000-0000-0000-0000-000000000000", "12345678-9abc-4def-8123-456789abcdef", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		value, err := uuid.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.UUIDField("reference", "Reference", schema.Nullable(), schema.Default(value))}}}})
		if err != nil {
			t.Fatal(err)
		}
		model := built.Models[0]
		migration := migrations.Migration{App: "refs", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "refs", Model: model}}}
		encoded, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "uuid", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		again, err := definition.Encode(producer, loaded.Definitions()[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("UUID historical round trip changed identity", err)
		}
		for _, replacement := range []string{`""`, `"` + value.Hex() + `"`, `"FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"`, `null`, `0`, `true`, `[]`, `{}`, `"` + raw + `","string":"foreign"`, `"` + raw + `","uuid":"` + raw + `"`} {
			bad := bytes.Replace(encoded, []byte(`"uuid":"`+raw+`"`), []byte(`"uuid":`+replacement), 1)
			if bytes.Equal(encoded, bad) {
				t.Fatal("invalid UUID control did not change wire")
			}
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: bad}); err == nil {
				t.Fatalf("invalid historical UUID accepted: %s", replacement)
			}
		}
		operation := loaded.Definitions()[0].Operations[0].(migrations.CreateModel)
		operation.Model.Fields[1].Default.UUID = "00000000-0000-0000-0000-000000000001"
		if loaded.Definitions()[0].Operations[0].(migrations.CreateModel).Model.Fields[1].Default.UUID != raw {
			t.Fatal("historical UUID default aliases caller")
		}
		changed, err := definition.Encode(producer, migrations.Migration{App: "refs", Name: migration.Name, Operations: []migrations.Operation{operation}})
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := definition.Load(definition.Source{SourceID: "changed", Document: changed})
		if err != nil || other.Digest() == loaded.Digest() {
			t.Fatal("definition digest ignores UUID default", err)
		}
	}
}
